import { $, h, icon, toast } from './dom.js';
import { fmtBytes, fmtAgo, dateFmt, plural } from './format.js';
import { api } from './api.js';
import { about } from './model.js';
import { onlineTotal, renderOverview } from './overview.js';
import { rows, updateRow } from './containers.js';
import { lastOverview } from './poll.js';
import { currentView, go } from './nav.js';

// ---------- Подключения ----------

const CONN_MS = 10000;
export let conns = new Map(); // имя контейнера -> сервис
let connTimer = null;
let connSel = null; // какой сервис открыт в разделе

function linkName(u) {
  if (u === 'amnezia') return 'Основная ссылка';
  const m = /^extra_(\d+)$/.exec(u);
  return m ? `Дополнительная ссылка ${m[1]}` : u;
}

// Сколько на связи: для VPN это клиенты, для прокси это устройства.
export function connCount(s) {
  if (s.kind === 'telemt') {
    return { online: s.clients.reduce((n, c) => n + (c.devices || 0), 0), total: null };
  }
  return { online: s.clients.filter(c => c.online).length, total: s.clients.length };
}

export function connBadge(name) {
  const s = conns.get(name);
  if (!s || s.state !== 'running' || s.error) return '';
  const { online, total } = connCount(s);
  if (total == null) return online ? `на связи ${online} ${plural(online, 'устройство', 'устройства', 'устройств')}` : 'никого на связи';
  return `на связи ${online} из ${total}`;
}

const CONN_NOTES = {
  wg: '«На связи» значит, что устройство обменивалось данными с сервером в последние 3 минуты. Трафик считается с последнего перезапуска контейнера.',
  openvpn: 'OpenVPN обновляет список раз в минуту. Трафик показан за текущее подключение.',
  telemt: 'Одной ссылкой могут пользоваться несколько человек. Устройства считаются по разным IP-адресам; один Telegram обычно держит несколько соединений.',
};

function bytesCell(iconName, value, title) {
  return h('span', { title }, iconName ? icon(iconName) : '', fmtBytes(value));
}

let editing = null; // пока открыта подпись, списки не перерисовываем, чтобы не сбить ввод

function connTitle(s, c) {
  if (s.kind !== 'telemt') return { main: c.name, sub: '' };
  return c.label ? { main: c.label, sub: linkName(c.name) } : { main: linkName(c.name), sub: '' };
}

// Имя, карандаш сразу за ним, под именем — какая это ссылка.
function nameCell(s, c) {
  const t = connTitle(s, c);
  const cell = h('div', { class: 'cl-name-cell' });
  const name = h('p', { class: 'cl-name' }, h('span', { title: t.main, text: t.main }));
  if (s.kind === 'telemt') {
    name.append(h('button', { class: 'cl-edit', title: 'Подписать', 'aria-label': `Подписать: ${t.main}`, onclick: () => editLabel(cell, s, c) }, icon('pencil-simple')));
  }
  cell.append(name, t.sub ? h('p', { class: 'cl-sub', text: t.sub }) : '');
  return cell;
}

function editLabel(cell, s, c) {
  editing = { container: s.container, name: c.name };
  const input = h('input', { class: 'cl-input', type: 'text', maxlength: '40', value: c.label || '', placeholder: 'Например, Мама', 'aria-label': `Подпись для «${linkName(c.name)}»` });
  const cancel = () => { editing = null; renderAllConns(); };
  const save = async () => {
    try {
      await api('/api/labels', { method: 'POST', body: { container: s.container, name: c.name, label: input.value } });
      const v = input.value.trim();
      toast(v ? `${linkName(c.name)} подписана: ${v}` : `Подпись у «${linkName(c.name)}» убрана`);
      editing = null;
      await loadConns();
    } catch (e) {
      toast(`Не получилось сохранить подпись: ${e.message}`, true);
    }
  };
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter') save();
    if (e.key === 'Escape') cancel();
  });
  cell.closest('.cl')?.classList.add('is-editing');
  cell.replaceChildren(h('div', { class: 'cl-form' }, input,
    h('button', { class: 'pbtn white sm', onclick: save }, 'Сохранить'),
    h('button', { class: 'pbtn ghost sm', onclick: cancel }, 'Отмена')));
  input.focus();
  input.select();
}

function connRow(s, c, big) {
  const now = Date.now() / 1000;
  let tone, label, seen = '', traffic = [];
  if (s.kind === 'telemt') {
    [tone, label] = c.disabled ? ['idle', 'отключена'] : c.online ? ['ok', 'на связи'] : ['idle', 'никого'];
    if (c.online) seen = `${c.devices} ${plural(c.devices, 'устройство', 'устройства', 'устройств')} · ${c.conns} ${plural(c.conns, 'соединение', 'соединения', 'соединений')}`;
    else if (!c.total) seen = 'ещё не использовалась';
    else seen = label;
    if (c.total) traffic = [bytesCell(null, c.total, 'Трафик в обе стороны с запуска прокси')];
  } else {
    [tone, label] = c.online ? ['ok', 'на связи'] : (c.lastSeen || s.kind === 'openvpn') ? ['idle', 'не на связи'] : ['idle', 'не подключался'];
    seen = label;
    if (s.kind === 'openvpn' && c.online && c.since) seen = `подключён с ${dateFmt.format(c.since * 1000)}`;
    else if (c.lastSeen && !c.online) seen = `был ${fmtAgo(now - c.lastSeen)}`;
    if (c.online || c.down || c.up) {
      traffic = [bytesCell('arrow-down', c.down, 'Скачал через VPN'), bytesCell('arrow-up', c.up, 'Отправил через VPN')];
    }
  }
  // Адреса — второй строкой под «кто на связи», а не отдельной строкой.
  const ips = c.ips || [], recent = c.recentIps || [];
  const addr = ips.length ? ips : recent;
  const li = h('li', { class: 'cl' + (big ? ' glass' : ''), 'data-tone': tone },
    h('span', { class: 'sdot', title: label }),
    nameCell(s, c),
    h('div', { class: 'cl-seen' },
      h('p', { text: seen }),
      addr.length ? h('p', { title: addr.join(', ') }, ips.length ? '' : 'недавно: ', h('code', { text: addr.join(', ') })) : ''),
    h('span', { class: 'cl-tr num' }, traffic));
  return li;
}

// Тело одного сервиса: список клиентов или объяснение, почему его нет.
function connBody(s, big) {
  if (s.state !== 'running') {
    return h('p', { class: 'cn-empty', text: s.state === 'paused' ? 'Контейнер на паузе, данных нет.' : 'Контейнер не работает, данных нет.' });
  }
  if (s.error) return h('p', { class: 'cn-empty', text: `Не удалось получить данные: ${s.error}` });
  if (!s.clients.length) return h('p', { class: 'cn-empty', text: 'Клиентов пока нет.' });
  return h('ul', { class: 'cn-list' }, s.clients.map(c => connRow(s, c, big)));
}

function connCountText(s) {
  if (s.state !== 'running' || s.error) return '';
  const { online, total } = connCount(s);
  return total == null
    ? `${online} ${plural(online, 'устройство', 'устройства', 'устройств')} на связи`
    : `${online} из ${total} на связи`;
}

function connNote(s) {
  return s.state === 'running' && !s.error && s.clients.length ? h('p', { class: 'note', text: CONN_NOTES[s.kind] }) : '';
}

function renderConns() {
  const services = [...conns.values()];
  const total = onlineTotal();
  $('#n-conns').textContent = total || '';
  if (!services.length) {
    $('#cn-sum').textContent = 'сервисов Amnezia на сервере нет';
    $('#cn-seg').replaceChildren();
    $('#cn-list').replaceChildren();
    return;
  }
  $('#cn-sum').textContent = total ? `на связи ${total} ${plural(total, 'устройство', 'устройства', 'устройств')}` : 'сейчас никого нет на связи';
  if (!conns.has(connSel)) {
    connSel = (services.find(s => s.state === 'running' && !s.error && connCount(s).online) || services[0]).container;
  }
  $('#cn-seg').replaceChildren(...services.map(s => {
    const { online, total: t } = s.state === 'running' && !s.error ? connCount(s) : { online: null };
    return h('button', { 'aria-pressed': String(s.container === connSel), onclick: () => { connSel = s.container; renderConns(); } },
      about({ name: s.container }).title,
      online == null ? '' : h('span', { class: 'n', text: t == null ? String(online) : `${online}/${t}` }));
  }));
  const s = conns.get(connSel);
  $('#cn-list').replaceChildren(connBody(s, true), connNote(s));
}

// Клиенты внутри раскрытой строки контейнера.
export function renderRowClients(r) {
  if (!r.clients) return;
  const s = conns.get(r.c.name);
  r.clients.hidden = !s;
  if (!s) return;
  r.clients.replaceChildren(
    h('div', { class: 'sub-h' },
      h('h3', { text: s.kind === 'telemt' ? 'Ссылки и устройства' : 'Клиенты' }),
      h('span', { class: 'faint', text: connCountText(s) })),
    connBody(s, false), connNote(s));
}

function renderAllConns() {
  if (editing) return;
  renderConns();
  for (const r of rows.values()) if (r.open) renderRowClients(r);
}

export async function loadConns() {
  clearTimeout(connTimer);
  try {
    const data = await api('/api/connections');
    conns = new Map(data.services.map(s => [s.container, s]));
    const none = !data.services.length;
    $('#nav-conns').hidden = none;
    if (none && currentView === 'conns') go('overview');
    renderAllConns();
    for (const r of rows.values()) updateRow(r, r.c);
    if (lastOverview) renderOverview(lastOverview);
  } catch (e) {
    $('#cn-sum').textContent = `Не удалось получить данные: ${e.message}`;
  }
  if (document.visibilityState === 'visible') connTimer = setTimeout(loadConns, CONN_MS);
}
