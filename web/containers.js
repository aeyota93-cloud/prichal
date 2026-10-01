import { $, h, icon, toast, confirmBox } from './dom.js';
import { nf0, fmtBytes, fmtPct, fmtDur, timeFmt, fullFmt, parseTime, cap } from './format.js';
import { POLL_MS, api } from './api.js';
import { identities, about, crashed, statusOf, ACTIONS, availableActions, confirmText, isNet } from './model.js';
import { hostMem, sortContainers } from './overview.js';
import { connBadge, renderRowClients } from './conns.js';
import { refresh } from './poll.js';
import { go } from './nav.js';

// ---------- Мини-график процессора ----------

function spark(history) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.classList.add('spark');
  svg.setAttribute('viewBox', '0 0 72 24');
  svg.setAttribute('preserveAspectRatio', 'none');
  const base = document.createElementNS(ns, 'line');
  base.setAttribute('class', 'base');
  base.setAttribute('x1', 0); base.setAttribute('x2', 72); base.setAttribute('y1', 23); base.setAttribute('y2', 23);
  svg.append(base);
  if (history.length < 2) return svg;
  const vals = history.map(p => p.cpu);
  const max = Math.max(5, ...vals) * 1.15;
  const step = 72 / 39; // 40 точек (около 2 минут) на всю ширину
  const x0 = 72 - (vals.length - 1) * step;
  const pts = vals.map((v, i) => [x0 + i * step, 22 - (v / max) * 20]);
  const line = pts.map(p => p.map(n => n.toFixed(1)).join(',')).join(' ');
  const area = document.createElementNS(ns, 'polygon');
  area.setAttribute('class', 'area');
  area.setAttribute('points', `${pts[0][0].toFixed(1)},23 ${line} ${pts.at(-1)[0].toFixed(1)},23`);
  const pl = document.createElementNS(ns, 'polyline');
  pl.setAttribute('points', line);
  const dot = document.createElementNS(ns, 'circle');
  dot.setAttribute('cx', pts.at(-1)[0]); dot.setAttribute('cy', pts.at(-1)[1]); dot.setAttribute('r', 1.8);
  const title = document.createElementNS(ns, 'title');
  const mins = Math.max(1, Math.round((history.at(-1).t - history[0].t) / 60000));
  title.textContent = `Процессор за ${mins} мин: сейчас ${fmtPct(vals.at(-1))}, максимум ${fmtPct(Math.max(...vals))}`;
  svg.append(title, area, pl, dot);
  return svg;
}

// ---------- Контейнеры ----------

export const rows = new Map(); // id -> {el, c, open, mode, ...}
let ctFilter = 'all';


function makeRow(c) {
  const r = { c, open: false, mode: 'idle', actionsKey: '' };
  r.title = h('p', { class: 'ct-title' });
  r.tech = h('p', { class: 'ct-tech' });
  r.cpu = h('div', { class: 'ct-cpu' });
  r.mem = h('div', { class: 'ct-mem num' });
  r.time = h('div', { class: 'ct-time num' });
  r.toggle = h('button', { class: 'ct-row', 'aria-expanded': 'false', onclick: () => setOpen(r, !r.open) },
    h('span', { class: 'sdot' }),
    h('div', {}, r.title, r.tech),
    r.cpu, r.mem, r.time,
    h('span', { class: 'ct-caret' }, icon('caret-down')));
  r.body = h('div', { class: 'ct-body', hidden: true });
  r.el = h('li', { class: 'ct glass' }, r.toggle, r.body);
  return r;
}

export function updateRow(r, c) {
  r.c = c;
  const a = about(c);
  const st = statusOf(c);
  r.el.dataset.tone = st.tone;
  r.toggle.dataset.tone = st.tone;
  const sub = [a.sub, connBadge(c.name)].filter(Boolean).join(' · ');
  r.title.replaceChildren(a.title, sub ? h('small', { text: sub }) : '');
  const tech = [...new Set([c.name, c.image.replace(/:latest$/, '')])].filter(s => s !== a.title).join(' · ') || c.image;
  r.tech.replaceChildren(st.tone !== 'ok' ? h('span', { class: 'ct-state', text: `${st.label} · ` }) : '', tech);
  r.toggle.setAttribute('aria-label', `${a.title}, ${st.label.toLowerCase()}. Показать подробности`);

  const live = c.state === 'running' || c.state === 'paused';
  if (live) {
    r.cpu.replaceChildren(spark(c.history), h('span', { class: 'num', text: c.cpu == null ? '…' : fmtPct(c.cpu) }));
    r.mem.textContent = fmtBytes(c.mem);
  } else {
    r.cpu.replaceChildren();
    r.mem.textContent = '';
  }
  const started = parseTime(c.startedAt), finished = parseTime(c.finishedAt);
  if (live && started) r.time.textContent = fmtDur((Date.now() - started) / 1000);
  else if (finished) r.time.textContent = `стоит ${fmtDur((Date.now() - finished) / 1000)}`;
  else r.time.textContent = '';

  if (r.open) {
    renderDetails(r);
    renderActions(r);
  }
}

function setOpen(r, open) {
  r.open = open;
  r.el.classList.toggle('is-open', open);
  r.toggle.setAttribute('aria-expanded', String(open));
  r.body.hidden = !open;
  if (open) {
    if (!r.built) buildBody(r);
    renderDetails(r);
    renderActions(r, true);
    renderRowClients(r);
    loadLogs(r);
  } else {
    r.mode = 'idle';
    clearTimeout(r.logTimer);
  }
}

// С обзора: открыть раздел и раскрыть нужный контейнер.
export function openContainer(id) {
  ctFilter = 'all';
  applyFilter();
  go('containers');
  const r = rows.get(id);
  if (!r) return;
  if (!r.open) setOpen(r, true);
  r.el.scrollIntoView({ block: 'start', behavior: 'smooth' });
}

function buildBody(r) {
  r.built = true;
  // Esc сворачивает ряд «Ещё» (в подтверждении Esc обрабатывает само подтверждение).
  r.actions = h('div', { class: 'ct-actions', onkeydown: e => {
    if (e.key !== 'Escape' || !r.moreOpen || r.mode !== 'idle' || e.target.closest('.confirm')) return;
    r.moreOpen = false;
    renderActions(r, true);
    r.actions.querySelector('.act-more')?.focus();
  } });
  r.details = h('dl', { class: 'details' });
  r.logBox = h('div', { class: 'logs', tabindex: '0', 'aria-label': 'Журнал контейнера' });
  r.tail = 300;
  r.follow = true;
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Сколько строк показать' },
    [100, 300, 1000].map(n => h('button', {
      'aria-pressed': String(n === r.tail), text: nf0.format(n),
      onclick: e => {
        r.tail = n;
        seg.querySelectorAll('button').forEach(b => b.setAttribute('aria-pressed', String(b === e.currentTarget)));
        loadLogs(r, true);
      },
    })));
  const follow = h('input', { type: 'checkbox', checked: true, onchange: e => { r.follow = e.target.checked; if (r.follow) loadLogs(r, true); } });
  r.clients = h('section', { hidden: true });
  r.logHead = h('div', { class: 'logs-head' },
    h('h3', { text: 'Журнал' }),
    seg,
    h('label', { class: 'check' }, follow, 'обновлять сам'),
    h('button', { class: 'textbtn', onclick: () => loadLogs(r, true) }, icon('arrows-clockwise'), 'Обновить'));
  r.body.append(r.actions, r.details, r.clients, h('div', {}, r.logHead, r.logBox));
}

function renderDetails(r) {
  const c = r.c;
  const items = [];
  const add = (dt, dd, cls) => items.push(h('div', { class: cls }, h('dt', { text: dt }), h('dd', { text: dd })));
  // Образ без :latest часто совпадает с именем: одна строка вместо двух одинаковых.
  if (c.image.replace(/:latest$/, '') === c.name) add('Образ и имя', c.image);
  else { add('Образ', c.image); add('Имя в Docker', c.name); }
  const started = parseTime(c.startedAt), finished = parseTime(c.finishedAt);
  if ((c.state === 'running' || c.state === 'paused') && started) add('Запущен', fullFmt.format(started));
  else if (finished) add('Остановлен', fullFmt.format(finished));
  const policy = { always: 'запустится снова сам', 'unless-stopped': 'запустится снова сам', 'on-failure': 'перезапустится, если упадёт' }[c.restartPolicy] || 'останется выключенным';
  add('Если упадёт', policy);
  add('Автоперезапусков', nf0.format(c.restartCount));
  add('Порты наружу', c.ports.length ? c.ports.join(', ') : 'нет, доступен только внутри сервера');
  if (c.state === 'running' || c.state === 'paused') {
    const limited = c.memLimit && hostMem && c.memLimit < hostMem * 0.95;
    add('Память', limited ? `${fmtBytes(c.mem)} из ${fmtBytes(c.memLimit)} (лимит)` : fmtBytes(c.mem));
  }
  if (crashed(c)) add('Код выхода', String(c.exitCode), 'is-bad');
  if (c.oomKilled) add('Причина остановки', 'Закончилась память, Docker выключил контейнер', 'wide is-bad');
  if (c.healthOutput) add('Проверка здоровья', c.healthOutput, 'wide is-bad');
  r.details.replaceChildren(...items);
}

function actButton(d, cls, onclick, label, hint) {
  return h('button', { class: 'act' + (cls ? ' ' + cls : '') + (hint ? ' has-hint' : ''), onclick },
    h('span', { class: 'rb' }, icon(d.icon)), label || d.label, hint ? h('span', { class: 'act-hint', text: hint }) : '');
}

function renderActions(r, force = false) {
  if (r.mode !== 'idle' && !force) return;
  const { main, more } = availableActions(r.c);
  const showMore = !!r.moreOpen && more.length > 0;
  // В ключе и раскрытый ряд: опрос раз в 3 секунды не должен его схлопывать.
  const key = `${main.join(',')}|${more.join(',')}|${showMore}`;
  if (!force && key === r.actionsKey) return;
  r.actionsKey = key;
  r.mode = 'idle';
  const actBtn = (act, cls, hint) => {
    const b = actButton(ACTIONS[act], cls, () => ask(r, act), null, hint);
    b.dataset.act = act;
    return b;
  };
  const kids = main.map((act, i) => actBtn(act, i === 0 ? 'primary' : ''));
  kids.push(h('span', { class: 'act-sep', 'aria-hidden': 'true' }),
    actButton({ icon: 'scroll' }, '', () => { r.logBox.scrollIntoView({ block: 'center', behavior: 'smooth' }); r.logBox.focus({ preventScroll: true }); }, 'Журнал'));
  if (more.length) {
    const btn = actButton({ icon: 'dots-three' }, 'act-more', () => { r.moreOpen = !r.moreOpen; renderActions(r, true); r.actions.querySelector('.act-more')?.focus(); }, 'Ещё');
    btn.setAttribute('aria-expanded', String(showMore));
    kids.push(btn);
  }
  if (showMore) {
    kids.push(h('div', { class: 'ct-more' }, more.map(act => actBtn(act, act === 'kill' ? 'danger' : '', act === 'kill' ? 'если обычная остановка не помогает' : ''))));
  }
  if (r.c.self) kids.push(h('p', { class: 'ct-note', text: 'Это сама панель: выключить её отсюда нельзя, только перезапустить.' }));
  r.actions.replaceChildren(...kids);
}

function ask(r, act) {
  const d = ACTIONS[act];
  if (!d.confirm) return run(r, act);
  r.mode = 'confirm';
  const a = about(r.c);
  const cancel = () => { renderActions(r, true); (r.actions.querySelector(`[data-act="${act}"]`) || r.actions.querySelector('button'))?.focus(); };
  r.actions.replaceChildren(confirmBox({
    q: `${cap(d.verb)} ${a.title}?`, text: confirmText(r.c, act), yes: d.confirm, danger: d.danger,
    onYes: () => run(r, act), onNo: cancel,
  }));
}

async function run(r, act) {
  const d = ACTIONS[act];
  const a = about(r.c);
  r.mode = 'busy';
  const busy = actButton({ icon: 'arrows-clockwise' }, 'is-busy', null, d.busy);
  busy.disabled = true;
  r.actions.replaceChildren(busy);
  try {
    await api(`/api/containers/${r.c.id}/${act}`, { method: 'POST' });
    toast(r.c.self ? 'Панель перезапускается, через пару секунд всё вернётся.' : `${a.title} ${d.done}`);
  } catch (e) {
    toast(`Не получилось ${d.verb} ${a.title}. Docker ответил: ${e.message}`, true);
  }
  r.mode = 'idle';
  r.actionsKey = '';
  await refresh();
  renderActions(r, true);
  if (r.open) loadLogs(r, true);
}

// ---------- Журнал ----------

export async function loadLogs(r, now = false) {
  clearTimeout(r.logTimer);
  if (!r.open) return;
  const box = r.logBox;
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  try {
    const data = await api(`/api/containers/${r.c.id}/logs?tail=${r.tail}`);
    const lines = data.lines;
    if (!lines.length) {
      box.replaceChildren(h('p', { class: 'logs-empty', text: 'Журнал пуст.' }));
    } else {
      box.replaceChildren(...lines.map(l => {
        const t = parseTime(l.time);
        return h('p', { class: l.stream === 'stderr' ? 'err' : null },
          h('time', { datetime: l.time, title: t ? fullFmt.format(t) : null, text: t ? timeFmt.format(t) : '' }),
          h('span', { text: l.text }));
      }));
    }
    if (atBottom || now) box.scrollTop = box.scrollHeight;
  } catch (e) {
    const noLog = /logging driver does not support reading/.test(e.message);
    box.replaceChildren(h('p', { class: 'logs-empty', text: noLog
      ? 'Этот контейнер не ведёт журнал: так его настроил Amnezia (или тот, кто его создал). Смотреть здесь нечего.'
      : `Не удалось загрузить журнал: ${e.message}` }));
    if (noLog) return; // и обновлять незачем
  }
  if (r.follow && r.open && document.visibilityState === 'visible') {
    r.logTimer = setTimeout(() => loadLogs(r), POLL_MS);
  }
}

// ---------- Список и фильтр ----------

function renderSummary(list) {
  const n = { ok: 0, paused: 0, idle: 0, bad: 0 };
  for (const c of list) {
    const st = statusOf(c);
    if (st.tone === 'bad') n.bad++;
    else if (c.state === 'paused') n.paused++;
    else if (st.tone === 'idle') n.idle++;
    else n.ok++;
  }
  const parts = [];
  if (n.ok) parts.push(`${n.ok} из ${list.length} работают`);
  if (n.paused) parts.push(`на паузе: ${n.paused}`);
  if (n.idle) parts.push(`остановлены: ${n.idle}`);
  if (n.bad) parts.push(`с проблемами: ${n.bad}`);
  $('#ct-sum').textContent = list.length ? parts.join(', ') : 'Контейнеров нет';
  $('#ct-chip').dataset.tone = n.bad ? 'bad' : n.paused ? 'warn' : '';
  $('#n-containers').textContent = list.length || '';
}

function applyFilter() {
  const all = [...rows.values()];
  const net = all.filter(r => isNet(r.c)).length;
  $('#f-all').textContent = all.length;
  $('#f-net').textContent = net;
  $('#f-apps').textContent = all.length - net;
  $('#ct-filter').hidden = !net || net === all.length;
  if ($('#ct-filter').hidden) ctFilter = 'all';
  $('#ct-filter').querySelectorAll('button').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.filter === ctFilter)));
  for (const r of all) r.el.hidden = ctFilter === 'net' ? !isNet(r.c) : ctFilter === 'apps' ? isNet(r.c) : false;
}

$('#ct-filter').querySelectorAll('button').forEach(b => b.addEventListener('click', () => { ctFilter = b.dataset.filter; applyFilter(); }));

export function renderContainers(list) {
  for (const c of list) identities.set(c.name, { title: c.title, sub: c.sub, kind: c.kind });
  const sorted = sortContainers(list);
  const ul = $('#ct-list');
  const seen = new Set();
  for (const c of sorted) {
    seen.add(c.id);
    let r = rows.get(c.id);
    if (!r) { r = makeRow(c); rows.set(c.id, r); }
    updateRow(r, c);
  }
  for (const [id, r] of rows) if (!seen.has(id)) { clearTimeout(r.logTimer); r.el.remove(); rows.delete(id); }
  const want = sorted.map(c => rows.get(c.id).el);
  if (want.some((el, i) => ul.children[i] !== el)) ul.replaceChildren(...want);
  renderSummary(list);
  applyFilter();
}
