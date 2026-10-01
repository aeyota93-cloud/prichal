import { ICONS } from './icons.js';

const POLL_MS = 3000;

// ---------- Мелкие помощники ----------

const $ = (s, root = document) => root.querySelector(s);

function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'text') el.textContent = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid);
  return el;
}

function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 256 256');
  svg.setAttribute('fill', 'currentColor');
  svg.setAttribute('aria-hidden', 'true');
  svg.classList.add('ico');
  svg.innerHTML = ICONS[name] || '';
  return svg;
}

const nf1 = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 0 });

function fmtBytes(b) {
  if (!b) return '0 Б';
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
  return `${(b < 10 && i > 0 ? nf1 : nf0).format(b)} ${units[i]}`;
}

function fmtPct(p) {
  if (p == null) return '';
  return `${(p < 10 ? nf1 : nf0).format(p)} %`;
}

function fmtDur(sec) {
  sec = Math.max(0, Math.floor(sec));
  if (sec < 60) return `${sec} с`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} мин`;
  const hrs = Math.floor(m / 60);
  if (hrs < 10) return m % 60 ? `${hrs} ч ${m % 60} мин` : `${hrs} ч`;
  if (hrs < 24) return `${hrs} ч`;
  const d = Math.floor(hrs / 24);
  if (d < 3 && hrs % 24) return `${d} дн ${hrs % 24} ч`;
  return `${d} дн`;
}

function fmtAgo(sec) {
  if (sec < 60) return 'только что';
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} мин назад`;
  const hrs = Math.floor(m / 60);
  if (hrs < 24) return `${hrs} ч назад`;
  const d = Math.floor(hrs / 24);
  if (d < 14) return `${d} дн назад`;
  if (d < 60) return `${Math.floor(d / 7)} нед назад`;
  if (d < 730) return `${Math.floor(d / 30)} мес назад`;
  return `${Math.floor(d / 365)} г назад`;
}

const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
const timeFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
const fullFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'medium' });

function parseTime(s) {
  if (!s || s.startsWith('0001-')) return null;
  const t = Date.parse(s);
  return Number.isNaN(t) ? null : t;
}

function plural(n, one, few, many) {
  const a = n % 10, b = n % 100;
  if (a === 1 && b !== 11) return one;
  if (a >= 2 && a <= 4 && (b < 12 || b > 14)) return few;
  return many;
}

// ---------- Что за контейнер ----------

// Названия приходят с сервера: он узнаёт контейнеры по имени и образу.
const identities = new Map(); // имя контейнера -> { title, sub, kind }

function about(c) {
  if (c.title) return { title: c.title, sub: c.sub || '', kind: c.kind || 'other' };
  return identities.get(c.name) || { title: c.name, sub: '', kind: 'other' };
}

// Остановлен вручную или упал? Упавший контейнер с политикой «всегда» Docker
// поднял бы сам, так что если такой стоит, его выключили руками.
function crashed(c) {
  if (c.state !== 'exited' || c.manualStop) return false;
  if (c.restartPolicy === 'always' || c.restartPolicy === 'unless-stopped') return false;
  return ![0, 137, 143].includes(c.exitCode);
}

function statusOf(c) {
  switch (c.state) {
    case 'running':
      if (c.health === 'unhealthy') return { tone: 'bad', icon: 'warning-circle', label: 'Нездоров' };
      if (c.health === 'starting') return { tone: 'warn', icon: 'clock', label: 'Запускается' };
      return { tone: 'ok', icon: 'play-circle', label: 'Работает' };
    case 'paused': return { tone: 'warn', icon: 'pause-circle', label: 'На паузе' };
    case 'restarting': return { tone: 'bad', icon: 'arrows-clockwise', label: 'Перезапускается' };
    case 'exited':
      if (c.oomKilled) return { tone: 'bad', icon: 'warning-circle', label: 'Не хватило памяти' };
      if (crashed(c)) return { tone: 'bad', icon: 'warning-circle', label: 'Упал' };
      return { tone: 'idle', icon: 'stop-circle', label: 'Остановлен' };
    case 'created': return { tone: 'idle', icon: 'stop-circle', label: 'Не запускался' };
    case 'dead': return { tone: 'bad', icon: 'warning-circle', label: 'Сломан' };
    default: return { tone: 'idle', icon: 'clock', label: c.state };
  }
}

const ACTIONS = {
  start:   { label: 'Запустить', icon: 'play', busy: 'Запускаю…', done: 'запущен', verb: 'запустить' },
  unpause: { label: 'Продолжить', icon: 'play', busy: 'Снимаю с паузы…', done: 'снова работает', verb: 'снять с паузы' },
  restart: { label: 'Перезапустить', icon: 'arrow-clockwise', busy: 'Перезапускаю…', done: 'перезапущен', verb: 'перезапустить', confirm: 'Да, перезапустить' },
  pause:   { label: 'Пауза', icon: 'pause', busy: 'Ставлю на паузу…', done: 'на паузе', verb: 'поставить на паузу', confirm: 'Да, на паузу' },
  stop:    { label: 'Остановить', icon: 'stop', busy: 'Останавливаю…', done: 'остановлен', verb: 'остановить', confirm: 'Да, остановить', danger: true },
  kill:    { label: 'Принудительно остановить', icon: 'lightning', busy: 'Останавливаю…', done: 'остановлен принудительно', verb: 'принудительно остановить', confirm: 'Да, остановить сразу', danger: true },
};

function availableActions(c) {
  if (c.self) return ['restart'];
  switch (c.state) {
    case 'running': return ['restart', 'pause', 'stop', 'kill'];
    case 'paused': return ['unpause', 'stop', 'kill'];
    case 'restarting': return ['stop', 'kill'];
    default: return ['start'];
  }
}

function confirmText(c, act) {
  const a = about(c);
  const base = {
    restart: 'Контейнер остановится и сразу запустится снова. Обычно это занимает несколько секунд.',
    pause: 'Всё внутри замрёт, но останется в памяти. Кнопка «Продолжить» вернёт как было.',
    stop: 'Контейнер штатно завершит работу. Запустить его снова можно здесь же.',
    kill: 'Контейнер выключится мгновенно, без штатного завершения. Это нужно, только если обычная остановка не помогает: несохранённые данные могут потеряться.',
  }[act];
  const down = act !== 'restart';
  const impact = {
    vpn: down
      ? `Пока он не работает, VPN через ${a.title} не будет работать ни у кого. Если ваш компьютер сейчас подключён через этот VPN, связь может пропасть.`
      : `Все, кто подключён через ${a.title}, на несколько секунд потеряют связь.`,
    proxy: down ? 'Прокси для Telegram перестанет работать у всех, кто им пользуется.' : 'Прокси для Telegram пропадёт на несколько секунд.',
    app: down ? `Пока ${a.title} не работает, им нельзя пользоваться.` : `${a.title} пропадёт на несколько секунд.`,
    self: 'Панель пропадёт на пару секунд и вернётся сама.',
  }[a.kind] || '';
  const after = (act === 'stop' || act === 'kill') && c.restartPolicy === 'always'
    ? 'После перезагрузки сервера он запустится снова сам.' : '';
  return [base, impact, after].filter(Boolean).join(' ');
}

// ---------- Сеть ----------

async function api(path, opts = {}) {
  const init = { method: opts.method || 'GET', headers: {} };
  if (init.method !== 'GET') init.headers['X-Prichal'] = '1';
  if (opts.body) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(opts.body);
  }
  const res = await fetch(path, init);
  if (res.status === 401 && path !== '/api/login') {
    location.href = '/login.html';
    throw new Error('нужно войти');
  }
  let data = null;
  try { data = await res.json(); } catch { /* пустой ответ */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `ошибка ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return data;
}

// ---------- Уведомления ----------

function toast(text, bad = false) {
  const el = h('div', { class: 'toast' + (bad ? ' is-bad' : '') }, icon(bad ? 'warning-circle' : 'check-circle'), h('p', { text }));
  $('#toasts').append(el);
  setTimeout(() => el.remove(), bad ? 9000 : 4500);
}

// ---------- Состояние сервера ----------

let hostMem = 0;

function setMeter(id, value, frac, sub) {
  const m = document.getElementById(id);
  m.querySelector('.meter-value').textContent = value;
  m.querySelector('.meter-sub').textContent = sub;
  const bar = m.querySelector('.meter-bar i');
  if (bar) bar.style.transform = `scaleX(${Math.min(1, Math.max(0, frac))})`;
  m.classList.toggle('is-high', frac >= 0.8 && frac < 0.92);
  m.classList.toggle('is-crit', frac >= 0.92);
}

function renderHost(hv) {
  const box = $('#host');
  if (!hv) { box.hidden = true; return; }
  box.hidden = false;
  hostMem = hv.memTotal;
  const cpu = hv.cpu;
  setMeter('m-cpu', cpu == null ? '…' : fmtPct(cpu), (cpu || 0) / 100,
    `${hv.cpus} ${plural(hv.cpus, 'ядро', 'ядра', 'ядер')}`);
  setMeter('m-mem', fmtPct(hv.memUsed / hv.memTotal * 100), hv.memUsed / hv.memTotal,
    `${fmtBytes(hv.memUsed)} из ${fmtBytes(hv.memTotal)}` + (hv.swapTotal ? ` · подкачка ${fmtBytes(hv.swapUsed)}` : ''));
  if (hv.diskTotal) {
    setMeter('m-disk', fmtPct(hv.diskUsed / hv.diskTotal * 100), hv.diskUsed / hv.diskTotal,
      `${fmtBytes(hv.diskUsed)} из ${fmtBytes(hv.diskTotal)}`);
  }
  const up = document.getElementById('m-up');
  up.querySelector('.meter-value').textContent = fmtDur(hv.uptime);
  up.querySelector('.meter-sub').textContent = `с ${dateFmt.format(Date.now() - hv.uptime * 1000)}`;
}

// ---------- Мини-график процессора ----------

function spark(history) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.classList.add('spark');
  svg.setAttribute('viewBox', '0 0 72 24');
  svg.setAttribute('preserveAspectRatio', 'none');
  const ns = 'http://www.w3.org/2000/svg';
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
  dot.setAttribute('cx', pts.at(-1)[0]); dot.setAttribute('cy', pts.at(-1)[1]); dot.setAttribute('r', 2);
  const title = document.createElementNS(ns, 'title');
  const mins = Math.max(1, Math.round((history.at(-1).t - history[0].t) / 60000));
  title.textContent = `Процессор за ${mins} мин: сейчас ${fmtPct(vals.at(-1))}, максимум ${fmtPct(Math.max(...vals))}`;
  svg.append(title, area, pl, dot);
  return svg;
}

// ---------- Контейнеры ----------

const rows = new Map(); // id -> {el, c, parts, open, mode, logs}
let lastOverview = null;

function makeRow(c) {
  const r = { c, open: false, mode: 'idle', actionsKey: '' };
  r.pill = h('span', { class: 'pill' });
  r.title = h('p', { class: 'ct-title' });
  r.tech = h('p', { class: 'ct-tech' });
  r.cpu = h('div', { class: 'ct-cpu' });
  r.mem = h('div', { class: 'num' });
  r.time = h('div', { class: 'ct-time' });
  r.toggle = h('button', { class: 'ct-row', 'aria-expanded': 'false', onclick: () => setOpen(r, !r.open) },
    h('div', {}, r.pill),
    h('div', {}, r.title, r.tech),
    r.cpu, r.mem, r.time,
    h('span', { class: 'ct-caret' }, icon('caret-down')));
  r.body = h('div', { class: 'ct-body', hidden: true });
  r.el = h('li', { class: 'ct' }, r.toggle, r.body);
  return r;
}

function updateRow(r, c) {
  r.c = c;
  const a = about(c);
  const st = statusOf(c);
  r.el.dataset.tone = st.tone;
  r.pill.dataset.tone = st.tone;
  r.pill.replaceChildren(icon(st.icon), st.label);
  const sub = [a.sub, connBadge(c.name)].filter(Boolean).join(' · ');
  r.title.replaceChildren(a.title, sub ? h('small', { text: sub }) : '');
  r.tech.textContent = [...new Set([c.name, c.image.replace(/:latest$/, '')])].filter(s => s !== a.title).join(' · ') || c.image;
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

function buildBody(r) {
  r.built = true;
  r.actions = h('div', { class: 'ct-actions' });
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
  r.clients = h('section', { class: 'ct-clients', hidden: true });
  r.body.append(
    r.actions, r.details, r.clients,
    h('div', { class: 'logs-head' },
      h('h2', { text: 'Журнал' }),
      seg,
      h('label', { class: 'toggle' }, follow, 'Обновлять сам'),
      h('button', { class: 'btn btn-quiet', onclick: () => loadLogs(r, true) }, icon('arrows-clockwise'), 'Обновить')),
    r.logBox);
}

function renderDetails(r) {
  const c = r.c;
  const items = [];
  const add = (dt, dd, cls) => items.push(h('div', { class: cls }, h('dt', { text: dt }), h('dd', { text: dd })));
  add('Образ', c.image);
  add('Имя в Docker', c.name);
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

function renderActions(r, force = false) {
  if (r.mode !== 'idle' && !force) return;
  const acts = availableActions(r.c);
  const key = acts.join(',');
  if (!force && key === r.actionsKey) return;
  r.actionsKey = key;
  r.mode = 'idle';
  const kids = acts.map((act, i) => {
    const d = ACTIONS[act];
    const primary = i === 0 && (act === 'start' || act === 'unpause');
    return h('button', { class: 'btn' + (primary ? ' btn-primary' : ''), onclick: () => ask(r, act) }, icon(d.icon), d.label);
  });
  if (r.c.self) kids.push(h('p', { class: 'ct-note', text: 'Это сама панель: выключить её отсюда нельзя, только перезапустить.' }));
  r.actions.replaceChildren(...kids);
}

function ask(r, act) {
  const d = ACTIONS[act];
  if (!d.confirm) return run(r, act);
  r.mode = 'confirm';
  const a = about(r.c);
  const q = h('p', { class: 'confirm-q', tabindex: '-1', text: `${d.verb[0].toUpperCase()}${d.verb.slice(1)} ${a.title}?` });
  const cancel = () => { renderActions(r, true); r.actions.querySelector('button')?.focus(); };
  const box = h('div', { class: 'confirm', 'data-tone': d.danger ? 'bad' : null, onkeydown: e => { if (e.key === 'Escape') cancel(); } },
    q,
    h('p', { class: 'confirm-text', text: confirmText(r.c, act) }),
    h('div', { class: 'row' },
      h('button', { class: 'btn ' + (d.danger ? 'btn-danger' : 'btn-primary'), onclick: () => run(r, act) }, icon(d.icon), d.confirm),
      h('button', { class: 'btn', onclick: cancel }, 'Отмена')));
  r.actions.replaceChildren(box);
  q.focus();
}

async function run(r, act) {
  const d = ACTIONS[act];
  const a = about(r.c);
  r.mode = 'busy';
  r.actions.replaceChildren(h('button', { class: 'btn is-busy', disabled: true }, icon('arrows-clockwise'), d.busy));
  try {
    await api(`/api/containers/${r.c.id}/${act}`, { method: 'POST' });
    if (r.c.self) {
      toast('Панель перезапускается, через пару секунд всё вернётся.');
    } else {
      toast(`${a.title} ${d.done}`);
    }
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

async function loadLogs(r, now = false) {
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

// ---------- Сводка и обновление ----------

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
  if (n.ok) parts.push(`работают: ${n.ok}`);
  if (n.paused) parts.push(`на паузе: ${n.paused}`);
  if (n.idle) parts.push(`остановлены: ${n.idle}`);
  if (n.bad) parts.push(`с проблемами: ${n.bad}`);
  $('#ct-sum').textContent = list.length ? parts.join(', ') : 'Контейнеров нет';
}

function renderContainers(list) {
  for (const c of list) identities.set(c.name, { title: c.title, sub: c.sub, kind: c.kind });
  const collator = new Intl.Collator('ru');
  const sorted = [...list].sort((x, y) => (x.self - y.self) || collator.compare(about(x).title, about(y).title));
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
}

let pollTimer = null;
let inflight = null;

function setOffline(off) {
  $('#offline').hidden = !off;
  document.body.classList.toggle('is-offline', off);
}

async function refresh() {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const ov = await api('/api/overview');
      lastOverview = ov;
      setOffline(false);
      if (ov.label) {
        $('#host-label').textContent = ov.label;
        document.title = `Причал · ${ov.label}`;
      }
      renderHost(ov.host);
      renderContainers(ov.containers);
    } catch (e) {
      setOffline(true);
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

function schedule() {
  clearTimeout(pollTimer);
  if (document.visibilityState !== 'visible') return;
  pollTimer = setTimeout(async () => { await refresh(); schedule(); }, POLL_MS);
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') {
    refresh().then(schedule);
    loadConns();
    for (const r of rows.values()) if (r.open && r.follow) loadLogs(r);
  }
});

// ---------- Образы ----------

let imgState = { mode: 'idle' };

async function loadImages() {
  try {
    const data = await api('/api/images');
    renderImages(data);
  } catch (e) {
    $('#im-sum').textContent = `Не удалось получить список: ${e.message}`;
  }
}

function imgName(im) {
  return im.tags[0] || 'без имени';
}

function renderImages(data) {
  const { images, buildCache } = data;
  const total = images.reduce((s, i) => s + i.size, 0);
  const unused = images.filter(i => !i.usedBy.length);
  const unusedSize = unused.reduce((s, i) => s + i.size, 0);
  $('#im-sum').textContent = `${images.length} ${plural(images.length, 'образ', 'образа', 'образов')}, всего ${fmtBytes(total)}`;

  // Блок очистки
  const prune = $('#prune');
  if (imgState.mode !== 'busy') {
    const free = unusedSize + buildCache;
    const what = [];
    if (unused.length) what.push(`${plural(unused.length, 'неиспользуемый образ', 'неиспользуемых образа', 'неиспользуемых образов')} (${unused.length})`);
    if (buildCache) what.push('кэш сборки');
    const text = free
      ? `Можно освободить ${fmtBytes(free)}: ${what.join(' и ')}.`
      : 'Всё, что лежит на диске, используется. Чистить нечего.';
    const btn = h('button', { class: 'btn', disabled: !free, onclick: () => askPrune(unused, buildCache) }, icon('broom'), 'Очистить');
    prune.replaceChildren(h('p', { text }), btn);
  }

  const ul = $('#im-list');
  ul.replaceChildren(...images.map(im => {
    const used = im.usedBy.length
      ? `нужен: ${im.usedBy.map(n => about({ name: n }).title).join(', ')}`
      : 'не используется';
    const li = h('li', { class: 'im' },
      h('div', {}, h('p', { class: 'im-name', title: im.tags.join(', ') || null, text: imgName(im) }),
        h('p', { class: 'im-id', text: im.id.replace('sha256:', '').slice(0, 12) })),
      h('div', { class: 'num', text: fmtBytes(im.size) }),
      h('div', { class: 'muted', text: fmtAgo(Date.now() / 1000 - im.created) }),
      h('div', { class: 'im-used' + (im.usedBy.length ? '' : ' is-free'), text: used }),
      im.usedBy.length ? h('div') : h('button', { class: 'btn btn-quiet', onclick: () => askRemove(li, im) }, icon('trash'), 'Удалить'));
    return li;
  }));
}

function askPrune(unused, buildCache) {
  const list = unused.map(i => `${imgName(i)} (${fmtBytes(i.size)})`);
  if (buildCache) list.push(`кэш сборки (${fmtBytes(buildCache)})`);
  const prune = $('#prune');
  const q = h('p', { class: 'confirm-q', tabindex: '-1', text: 'Очистить неиспользуемое?' });
  const box = h('div', { class: 'confirm', onkeydown: e => { if (e.key === 'Escape') loadImages(); } },
    q,
    h('p', { class: 'confirm-text', text: `Будет удалено: ${list.join(', ')}. Образы, которые нужны контейнерам, останутся, даже если контейнер сейчас остановлен.` }),
    h('div', { class: 'row' },
      h('button', { class: 'btn btn-danger', onclick: doPrune }, icon('broom'), 'Да, очистить'),
      h('button', { class: 'btn', onclick: loadImages }, 'Отмена')));
  prune.replaceChildren(box);
  q.focus();
}

async function doPrune() {
  imgState.mode = 'busy';
  $('#prune').replaceChildren(h('button', { class: 'btn is-busy', disabled: true }, icon('arrows-clockwise'), 'Очищаю…'));
  try {
    const res = await api('/api/prune', { method: 'POST' });
    toast(`Готово, освобождено ${fmtBytes(res.reclaimed)}`);
  } catch (e) {
    toast(`Очистка не удалась: ${e.message}`, true);
  }
  imgState.mode = 'idle';
  loadImages();
}

function askRemove(li, im) {
  li.querySelector('.confirm')?.remove();
  const q = h('p', { class: 'confirm-q', tabindex: '-1', text: `Удалить образ ${imgName(im)}?` });
  const box = h('div', { class: 'confirm', onkeydown: e => { if (e.key === 'Escape') box.remove(); } },
    q,
    h('p', { class: 'confirm-text', text: 'Он не нужен ни одному контейнеру. Если понадобится снова, Docker скачает его заново.' }),
    h('div', { class: 'row' },
      h('button', { class: 'btn btn-danger', onclick: () => removeImage(im) }, icon('trash'), 'Да, удалить'),
      h('button', { class: 'btn', onclick: () => box.remove() }, 'Отмена')));
  li.append(box);
  q.focus();
}

async function removeImage(im) {
  try {
    await api(`/api/images/${encodeURIComponent(im.id)}/remove`, { method: 'POST' });
    toast(`Образ ${imgName(im)} удалён`);
  } catch (e) {
    toast(`Не получилось удалить ${imgName(im)}: ${e.message}`, true);
  }
  loadImages();
}

// ---------- Подключения ----------

const CONN_MS = 10000;
let conns = new Map(); // имя контейнера -> сервис
let connTimer = null;

function linkName(u) {
  if (u === 'amnezia') return 'Основная ссылка';
  const m = /^extra_(\d+)$/.exec(u);
  return m ? `Дополнительная ссылка ${m[1]}` : u;
}

// Сколько на связи: для VPN это клиенты, для прокси это устройства.
function connCount(s) {
  if (s.kind === 'telemt') {
    return { online: s.clients.reduce((n, c) => n + (c.devices || 0), 0), total: null };
  }
  return { online: s.clients.filter(c => c.online).length, total: s.clients.length };
}

function connBadge(name) {
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
  return h('span', { class: 'cn-bytes', title }, iconName ? icon(iconName) : '', fmtBytes(value));
}

let editing = null; // пока открыта подпись, списки не перерисовываем, чтобы не сбить ввод

function connTitle(s, c) {
  if (s.kind !== 'telemt') return { main: c.name, sub: '' };
  return c.label ? { main: c.label, sub: linkName(c.name) } : { main: linkName(c.name), sub: '' };
}

function nameCell(s, c) {
  const t = connTitle(s, c);
  const cell = h('div', { class: 'cn-name-cell' },
    h('div', { class: 'cn-name-text' },
      h('p', { class: 'cn-name', title: t.main, text: t.main }),
      t.sub ? h('p', { class: 'cn-sub', text: t.sub }) : ''));
  if (s.kind === 'telemt') {
    cell.append(h('button', { class: 'cn-edit', title: 'Подписать', 'aria-label': `Подписать: ${t.main}`, onclick: () => editLabel(cell, s, c) }, icon('pencil-simple')));
  }
  return cell;
}

function editLabel(cell, s, c) {
  editing = { container: s.container, name: c.name };
  const input = h('input', { class: 'cn-input', type: 'text', maxlength: '40', value: c.label || '', placeholder: 'Например, Мама', 'aria-label': `Подпись для «${linkName(c.name)}»` });
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
  cell.closest('.cn-row')?.classList.add('is-editing');
  cell.replaceChildren(h('div', { class: 'cn-edit-form' }, input,
    h('button', { class: 'btn btn-primary', onclick: save }, 'Сохранить'),
    h('button', { class: 'btn', onclick: cancel }, 'Отмена')));
  input.focus();
  input.select();
}

function ipLine(label, ips) {
  return h('p', { class: 'cn-ip-line' }, h('span', { text: label }), ips.map(ip => h('code', { class: 'cn-ip', text: ip })));
}

function connRow(s, c) {
  const now = Date.now() / 1000;
  let st, seen = '', down = h('span'), up = h('span');
  if (s.kind === 'telemt') {
    st = c.disabled ? { tone: 'idle', icon: 'stop-circle', label: 'Отключена' }
      : c.online ? { tone: 'ok', icon: 'check-circle', label: 'На связи' }
      : { tone: 'idle', icon: 'clock', label: 'Никого' };
    if (c.online) seen = `${c.devices} ${plural(c.devices, 'устройство', 'устройства', 'устройств')} · ${c.conns} ${plural(c.conns, 'соединение', 'соединения', 'соединений')}`;
    if (c.total) down = bytesCell(null, c.total, 'Трафик в обе стороны с запуска прокси');
    else if (!c.online) seen = 'ещё не использовалась';
  } else {
    st = c.online ? { tone: 'ok', icon: 'check-circle', label: 'На связи' }
      : (c.lastSeen || s.kind === 'openvpn') ? { tone: 'idle', icon: 'clock', label: 'Не на связи' }
      : { tone: 'idle', icon: 'stop-circle', label: 'Не подключался' };
    if (s.kind === 'openvpn' && c.online && c.since) seen = `подключён с ${dateFmt.format(c.since * 1000)}`;
    else if (c.lastSeen && !c.online) seen = `был ${fmtAgo(now - c.lastSeen)}`;
    if (c.online || c.down || c.up) {
      down = bytesCell('arrow-down', c.down, 'Скачал через VPN');
      up = bytesCell('arrow-up', c.up, 'Отправил через VPN');
    }
  }
  const li = h('li', { class: 'cn-row', 'data-tone': st.tone },
    h('div', {}, h('span', { class: 'pill', 'data-tone': st.tone }, icon(st.icon), st.label)),
    nameCell(s, c),
    h('p', { class: 'cn-seen', text: seen }),
    down, up);
  const ips = c.ips || [], recent = c.recentIps || [];
  if (ips.length || recent.length) {
    li.append(h('div', { class: 'cn-ips' },
      ips.length ? ipLine('На связи:', ips) : '',
      recent.length ? ipLine('Недавно:', recent) : ''));
  }
  return li;
}

// Тело одного сервиса: список клиентов или объяснение, почему его нет.
function connBody(s) {
  if (s.state !== 'running') {
    return h('p', { class: 'cn-empty', text: s.state === 'paused' ? 'Контейнер на паузе, данных нет.' : 'Контейнер не работает, данных нет.' });
  }
  if (s.error) return h('p', { class: 'cn-empty', text: `Не удалось получить данные: ${s.error}` });
  if (!s.clients.length) return h('p', { class: 'cn-empty', text: 'Клиентов пока нет.' });
  return h('ul', { class: 'cn-rows' }, s.clients.map(c => connRow(s, c)));
}

function connCountText(s) {
  if (s.state !== 'running' || s.error) return '';
  const { online, total } = connCount(s);
  return total == null
    ? [h('strong', { text: String(online) }), ` ${plural(online, 'устройство', 'устройства', 'устройств')} на связи`]
    : [h('strong', { text: String(online) }), ` из ${total} на связи`];
}

function connNote(s) {
  return s.state === 'running' && !s.error && s.clients.length ? h('p', { class: 'cn-note', text: CONN_NOTES[s.kind] }) : '';
}

function renderConns() {
  const list = $('#cn-list');
  const services = [...conns.values()];
  if (!services.length) {
    $('#cn-sum').textContent = 'Сервисов Amnezia на сервере нет';
    list.replaceChildren();
    return;
  }
  const parts = [];
  for (const s of services) {
    const { online } = connCount(s);
    if (s.state === 'running' && !s.error && online) parts.push(`${about({ name: s.container }).title} ${online}`);
  }
  list.replaceChildren(...services.map(s => {
    const a = about({ name: s.container });
    return h('section', { class: 'cn', 'aria-label': a.title },
      h('header', { class: 'cn-head' },
        h('h2', {}, a.title, a.sub ? h('small', { text: a.sub }) : ''),
        h('p', { class: 'cn-count' }, connCountText(s))),
      connBody(s),
      connNote(s));
  }));
  $('#cn-sum').textContent = parts.length ? `на связи: ${parts.join(', ')}` : 'сейчас никого нет на связи';
}

// Клиенты внутри раскрытой строки контейнера.
function renderRowClients(r) {
  if (!r.clients) return;
  const s = conns.get(r.c.name);
  r.clients.hidden = !s;
  if (!s) return;
  r.clients.replaceChildren(
    h('div', { class: 'ct-clients-head' },
      h('h2', { text: s.kind === 'telemt' ? 'Ссылки и устройства' : 'Клиенты' }),
      h('p', { class: 'cn-count' }, connCountText(s))),
    h('div', { class: 'cn' }, connBody(s), connNote(s)));
}

function renderAllConns() {
  if (editing) return;
  renderConns();
  for (const r of rows.values()) if (r.open) renderRowClients(r);
}

async function loadConns() {
  clearTimeout(connTimer);
  try {
    const data = await api('/api/connections');
    conns = new Map(data.services.map(s => [s.container, s]));
    const none = !data.services.length;
    $('#tab-conns').hidden = none;
    if (none && $('#tab-conns').getAttribute('aria-selected') === 'true') selectTab(0);
    renderAllConns();
    for (const r of rows.values()) updateRow(r, r.c);
  } catch (e) {
    $('#cn-sum').textContent = `Не удалось получить данные: ${e.message}`;
  }
  if (document.visibilityState === 'visible') connTimer = setTimeout(loadConns, CONN_MS);
}

// ---------- Обновления ----------

const GROUPS = [
  { key: 'security', title: 'Безопасность', note: '', open: true },
  { key: 'docker', title: 'Docker', note: 'При установке Docker перезапустится: все контейнеры, включая эту панель, пропадут примерно на полминуты. Панель вернётся сама и покажет результат.', open: true },
  { key: 'kernel', title: 'Ядро Linux', note: 'Новое ядро заработает после перезагрузки сервера.', open: true },
  { key: 'other', title: 'Остальное', note: '', open: false },
];

const upd = {
  view: null, selected: new Set(), log: '', offset: 0, taskId: null, timer: null, confirm: null,
  openGroups: new Set(GROUPS.filter(g => g.open).map(g => g.key)),
  backup: new Map(), // приложение -> делать ли копию данных
};

function taskRunning() {
  return upd.view?.task?.state === 'running';
}

async function loadUpdates(fresh = false) {
  if (fresh) $('#up-sum').textContent = 'Проверяю…';
  try {
    const v = await api('/api/updates' + (fresh ? '?fresh=1' : ''));
    upd.view = v;
    const names = new Set((v.system?.packages || []).map(p => p.name));
    for (const n of [...upd.selected]) if (!names.has(n)) upd.selected.delete(n);
    renderUpdates();
    if (v.task) followTask(v.task);
  } catch (e) {
    $('#up-sum').textContent = `Не удалось получить данные: ${e.message}`;
  }
}

function renderUpdates() {
  const v = upd.view;
  if (!v) return;
  const sys = v.system;
  const parts = [];
  if (v.error) parts.push('пакеты: ошибка');
  else if (sys && !sys.supported) parts.push(`пакеты: ${sys.pm} пока не поддерживается`);
  else if (sys) parts.push(sys.packages.length ? `${sys.packages.length} ${plural(sys.packages.length, 'пакет', 'пакета', 'пакетов')} можно обновить` : 'пакеты свежие');
  const appUpd = (v.apps?.apps || []).filter(a => a.update).length;
  if (appUpd) parts.push(`${appUpd} ${plural(appUpd, 'приложение', 'приложения', 'приложений')} можно обновить`);
  if (sys?.rebootRequired) parts.push('нужна перезагрузка');
  $('#up-sum').textContent = parts.join(' · ');
  renderReboot(sys);
  renderTaskBox();
  renderApps(v);
  renderPkgs(v);
  renderFoot(sys);
}

// ----- Подтверждения (одно на странице) -----

function confirmBox({ q, text, yes, danger, onYes, onNo }) {
  const qEl = h('p', { class: 'confirm-q', tabindex: '-1', text: q });
  const box = h('div', { class: 'confirm', 'data-tone': danger ? 'bad' : null, onkeydown: e => { if (e.key === 'Escape') onNo(); } },
    qEl,
    h('p', { class: 'confirm-text', text }),
    h('div', { class: 'row' },
      h('button', { class: 'btn ' + (danger ? 'btn-danger' : 'btn-primary'), onclick: onYes }, yes),
      h('button', { class: 'btn', onclick: onNo }, 'Отмена')));
  setTimeout(() => qEl.focus());
  return box;
}

function askUpd(kind) {
  upd.confirm = kind;
  renderUpdates();
}

function cancelUpd() {
  upd.confirm = null;
  renderUpdates();
}

// ----- Перезагрузка -----

// Контейнеры, которые после перезагрузки сами не поднимутся.
function noAutostart() {
  return [...rows.values()].map(r => r.c)
    .filter(c => (c.state === 'running' || c.state === 'paused') && !['always', 'unless-stopped', 'on-failure'].includes(c.restartPolicy))
    .map(c => about(c).title);
}

function rebootConfirm() {
  const manual = noAutostart();
  const text = 'Сервер будет недоступен пару минут. Контейнеры с автозапуском поднимутся сами. SSH-туннель оборвётся: через пару минут подключитесь снова.'
    + (manual.length ? ` Без автозапуска, их придётся запустить вручную: ${manual.join(', ')}.` : '');
  return confirmBox({ q: 'Перезагрузить сервер сейчас?', text, yes: 'Да, перезагрузить', danger: true, onYes: doReboot, onNo: cancelUpd });
}

function renderReboot(sys) {
  const box = $('#up-reboot');
  if (!sys?.rebootRequired) { box.replaceChildren(); return; }
  const what = sys.rebootPkgs.length ? ` Обновились: ${sys.rebootPkgs.join(', ')}.` : '';
  box.replaceChildren(h('div', { class: 'up-banner' },
    h('span', { class: 'up-banner-ico' }, icon('arrows-clockwise')),
    h('div', {},
      h('p', { class: 'up-banner-title', text: 'Серверу нужна перезагрузка' }),
      h('p', { class: 'up-banner-text', text: `${what} Новые версии заработают после перезагрузки. Сделайте её, когда будет удобно.` }),
      upd.confirm === 'reboot-banner' ? rebootConfirm()
        : h('button', { class: 'btn', disabled: taskRunning(), onclick: () => askUpd('reboot-banner') }, icon('arrows-clockwise'), 'Перезагрузить сейчас'))));
}

async function doReboot() {
  upd.confirm = null;
  try {
    await api('/api/updates/reboot', { method: 'POST' });
    toast('Сервер перезагружается. Через пару минут подключитесь снова.');
  } catch (e) {
    toast(`Не получилось перезагрузить: ${e.message}`, true);
  }
  renderUpdates();
}

function renderFoot(sys) {
  const foot = $('#up-foot');
  if (sys?.rebootRequired) { foot.replaceChildren(); return; }
  foot.replaceChildren(upd.confirm === 'reboot-foot' ? rebootConfirm()
    : h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => askUpd('reboot-foot') }, icon('arrows-clockwise'), 'Перезагрузить сервер'));
}

// ----- Задача и её журнал -----

const TASK_STATE = {
  running: { tone: 'warn', icon: 'arrows-clockwise', label: 'Идёт' },
  done: { tone: 'ok', icon: 'check-circle', label: 'Готово' },
  failed: { tone: 'bad', icon: 'warning-circle', label: 'Ошибка' },
  interrupted: { tone: 'bad', icon: 'warning-circle', label: 'Прервано' },
};

function followTask(t) {
  if (upd.taskId !== t.id) {
    upd.taskId = t.id;
    upd.log = '';
    upd.offset = 0;
  }
  clearTimeout(upd.timer);
  pollTask();
}

async function pollTask() {
  let data;
  try {
    data = await api(`/api/updates/task?offset=${upd.offset}`);
  } catch {
    // Панель могла перезапуститься вместе с Docker; пробуем дальше.
    upd.timer = setTimeout(pollTask, 3000);
    return;
  }
  const t = data.task;
  if (!t) return;
  if (t.id !== upd.taskId) { followTask(t); return; }
  upd.log += data.log;
  upd.offset = data.offset;
  const wasRunning = taskRunning();
  if (upd.view) upd.view.task = t;
  renderTaskBox();
  if (t.state === 'running') {
    upd.timer = setTimeout(pollTask, 2000);
  } else if (wasRunning) {
    toast(t.state === 'done' ? `Готово: ${t.title}` : `${TASK_STATE[t.state].label}: ${t.title}`, t.state !== 'done');
    loadUpdates();
  }
}

function renderTaskBox() {
  const box = $('#up-task');
  const t = upd.view?.task;
  if (!t) { box.replaceChildren(); return; }
  const st = TASK_STATE[t.state] || TASK_STATE.running;
  let when = `начато ${dateFmt.format(t.started * 1000)}`;
  if (t.finished) when += `, заняло ${fmtDur(t.finished - t.started)}`;
  if (t.state === 'failed' && t.exit != null) when += `, код ${t.exit}`;
  if (t.state === 'interrupted') when += ', сервер перезагрузился посреди задачи';
  let pre = box.querySelector('.logs');
  const keepBottom = !pre || pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
  const head = h('div', { class: 'up-task-head' },
    h('span', { class: 'pill' + (t.state === 'running' ? ' is-busy' : ''), 'data-tone': st.tone }, icon(st.icon), st.label),
    h('p', { class: 'up-task-title', text: t.title[0].toUpperCase() + t.title.slice(1) }),
    h('p', { class: 'up-task-when', text: when }));
  pre = h('pre', { class: 'logs up-log', tabindex: '0', 'aria-label': 'Ход выполнения' }, upd.log || 'Жду первых строк…');
  const wrap = t.state === 'running'
    ? h('div', { class: 'up-task' }, head, pre)
    : h('details', { class: 'up-task' }, h('summary', {}, head), pre);
  box.replaceChildren(wrap);
  if (keepBottom) pre.scrollTop = pre.scrollHeight;
}

async function startTask(url, body) {
  upd.confirm = null;
  try {
    const res = await api(url, { method: 'POST', body });
    upd.view.task = res.task;
    renderUpdates();
    followTask(res.task);
  } catch (e) {
    toast(e.message, true);
    renderUpdates();
  }
}

// ----- Приложения (docker compose) -----

const BACKUP_DEFAULT_MAX = 1 << 30;

function wantBackup(a) {
  if (upd.backup.has(a.key)) return upd.backup.get(a.key);
  const total = a.volumes.reduce((n, v) => n + Math.max(0, v.size), 0);
  return total <= BACKUP_DEFAULT_MAX;
}

// Где живёт приложение: проект/сервис docker compose, без повторов названия.
function appWhere(a) {
  const where = a.project === a.service ? a.service : `${a.project}/${a.service}`;
  return where.toLowerCase() === a.title.toLowerCase() ? '' : where;
}

function appRow(a) {
  const vers = a.current || '';
  let status, action = '', note = '';
  if (a.error) {
    status = h('p', { class: 'up-muted', text: a.error });
  } else if (a.git && a.git.behind > 0) {
    const n = a.git.behind;
    status = h('div', {},
      h('p', {}, 'В git ', h('strong', { text: `${n} ${plural(n, 'новый коммит', 'новых коммита', 'новых коммитов')}` }),
        h('span', { class: 'up-muted', text: ` (${a.git.current} → ${a.git.latest})` })),
      h('ul', { class: 'up-commits' }, a.git.commits.map(c => h('li', { text: c }))),
      a.git.dirty ? h('p', { class: 'up-app-note', text: 'На сервере изменены файлы проекта: если они пересекаются с новыми, обновление остановится с ошибкой.' }) : '',
      a.git.diverged ? h('p', { class: 'up-app-note', text: 'На сервере есть собственные коммиты: обновить отсюда нельзя, нужно слияние вручную.' }) : '');
    action = h('div', { class: 'up-app-actions' },
      h('button', { class: 'btn btn-primary', disabled: taskRunning() || a.git.diverged, onclick: () => askUpd('git:' + a.key) }, icon('arrow-down'), 'Обновить'));
  } else if (a.git) {
    status = h('p', { class: 'up-muted', text: `Установлена последняя версия из git (${a.git.current}).` });
  } else if (a.gitError) {
    status = h('p', { class: 'up-muted', text: `Образ собран на этом сервере. Проверить git не вышло: ${a.gitError}` });
  } else if (a.local) {
    status = h('p', { class: 'up-muted', text: 'Образ собран на этом сервере: сравнивать не с чем.' });
  } else if (a.update) {
    status = h('p', {}, 'Вышла ', h('strong', { text: a.latest && a.latest !== a.current ? `версия ${a.latest}` : 'новая сборка' }),
      a.built ? h('span', { class: 'up-muted', text: ` от ${dateFmt.format(Date.parse(a.built)).replace(/,.*$/, '')}` }) : '');
    const size = a.volumes.reduce((n, v) => n + Math.max(0, v.size), 0);
    const cb = h('input', { type: 'checkbox', id: `bk-${a.key}` });
    cb.checked = wantBackup(a);
    cb.disabled = taskRunning();
    cb.addEventListener('change', () => { upd.backup.set(a.key, cb.checked); });
    action = h('div', { class: 'up-app-actions' },
      a.volumes.length ? h('label', { class: 'toggle', for: `bk-${a.key}` }, cb, `копия данных${size ? ` (${fmtBytes(size)})` : ''}`) : '',
      h('button', { class: 'btn btn-primary', disabled: taskRunning(), onclick: () => askUpd('app:' + a.key) }, icon('arrow-down'), 'Обновить'));
  } else {
    status = h('p', { class: 'up-muted', text: 'Установлена последняя версия.' });
  }
  if (a.pinned) {
    const tag = a.image.split('@')[0].split(':').pop();
    note = h('p', { class: 'up-app-note', text: `Версия закреплена тегом «${tag}»: новые версии придут, только если поменять тег в docker-compose.` });
  }
  const li = h('li', { class: 'up-app' },
    h('div', { class: 'up-app-name' },
      h('p', { class: 'up-pkg-name', text: a.title }),
      h('p', { class: 'up-muted', text: [appWhere(a), vers && `у вас ${vers}`].filter(Boolean).join(' · ') })),
    h('div', { class: 'up-app-status' }, status, note),
    action);
  if (upd.confirm === 'git:' + a.key) {
    li.append(confirmBox({
      q: `Обновить ${a.title} из git?`,
      text: [
        'Скачаю новые коммиты, соберу образ заново и перезапущу.',
        `${a.title} остановится примерно на минуту.`,
        a.self ? 'Это сама панель: она пропадёт на минуту и вернётся уже новой. Страница обновится сама, журнал можно открыть снова.' : '',
        'Если новая версия не запустится, верну прежнюю.',
      ].filter(Boolean).join(' '),
      yes: 'Да, обновить',
      onYes: () => startTask('/api/updates/git', { key: a.key }),
      onNo: cancelUpd,
    }));
  }
  if (upd.confirm === 'app:' + a.key) {
    const backup = wantBackup(a) && a.volumes.length;
    li.append(confirmBox({
      q: `Обновить ${a.title}?`,
      text: [
        `${a.title} остановится примерно на минуту, то, что выполняется в нём прямо сейчас, прервётся.`,
        backup ? `Перед обновлением сохраню копию данных (${a.volumes.map(v => v.name).join(', ')}) в /var/backups/prichal/${a.project}, хранятся 5 последних.` : 'Копию данных делать не буду.',
        a.self ? 'Это сама панель: она пропадёт на минуту и вернётся уже новой.' : '',
        'Если что-то пойдёт не так, запущу его обратно.',
      ].filter(Boolean).join(' '),
      yes: 'Да, обновить',
      onYes: () => startTask('/api/updates/app', { key: a.key, backup: !!backup }),
      onNo: cancelUpd,
    }));
  }
  return li;
}

function renderApps(v) {
  const card = $('#up-apps');
  const apps = v.apps?.apps || [];
  const manual = v.apps?.manual || [];
  card.hidden = !v.appsError && !apps.length && !manual.length;
  if (card.hidden) return;
  const head = h('div', { class: 'up-card-head' },
    h('h2', {}, 'Приложения', h('small', { text: 'docker compose' })),
    v.apps ? h('p', { class: 'up-muted', text: `проверено ${fmtAgo(Date.now() / 1000 - v.apps.checkedAt)}` }) : '',
    h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => loadUpdates(true) }, icon('arrows-clockwise'), 'Проверить'));
  const parts = [head];
  if (v.appsError) parts.push(h('p', { class: 'cn-empty', text: `Не удалось проверить: ${v.appsError}` }));
  if (apps.length) parts.push(h('ul', { class: 'up-apps' }, apps.map(appRow)));
  else if (!v.appsError) parts.push(h('p', { class: 'cn-empty', text: 'Приложений на docker compose нет.' }));
  if (manual.length) {
    parts.push(h('details', { class: 'up-group' },
      h('summary', {},
        h('span', { class: 'up-group-title', text: `Обновляются не отсюда: ${manual.length}` }),
        h('span', { class: 'ct-caret' }, icon('caret-down'))),
      h('ul', { class: 'up-list up-manual' }, manual.map(m => h('li', {},
        h('span', { class: 'up-pkg-name', text: m.title }), h('span', { class: 'up-muted', text: ` ${m.name !== m.title ? m.name + ', ' : ''}${m.reason}` }))))));
  }
  card.replaceChildren(...parts);
}

// ----- Пакеты -----

function groupCheckbox(pkgs) {
  const n = pkgs.filter(p => upd.selected.has(p.name)).length;
  const cb = h('input', { type: 'checkbox', 'aria-label': 'Выбрать всю группу' });
  cb.checked = n > 0 && n === pkgs.length;
  cb.indeterminate = n > 0 && n < pkgs.length;
  cb.disabled = taskRunning();
  cb.addEventListener('click', e => e.stopPropagation()); // не сворачивать группу
  cb.addEventListener('change', () => {
    const on = n < pkgs.length;
    for (const p of pkgs) on ? upd.selected.add(p.name) : upd.selected.delete(p.name);
    upd.confirm = null;
    renderUpdates();
  });
  return cb;
}

function pkgRow(p) {
  const cb = h('input', { type: 'checkbox', id: `pkg-${p.name}` });
  cb.checked = upd.selected.has(p.name);
  cb.disabled = taskRunning();
  cb.addEventListener('change', () => {
    cb.checked ? upd.selected.add(p.name) : upd.selected.delete(p.name);
    upd.confirm = null;
    renderUpdates();
  });
  return h('li', { class: 'up-pkg' },
    cb,
    h('label', { for: `pkg-${p.name}`, class: 'up-pkg-main' },
      h('span', { class: 'up-pkg-name', text: p.name }),
      p.security && p.group !== 'security' ? h('span', { class: 'up-tag', text: 'безопасность' }) : '',
      h('span', { class: 'up-pkg-sum', text: p.summary || '' })),
    h('span', { class: 'up-ver', title: `${p.from} → ${p.to}` },
      h('span', { class: 'up-ver-from', text: p.from }), icon('arrow-right'), h('span', { text: p.to })));
}

function selectOnly(pred) {
  upd.selected = new Set(upd.view.system.packages.filter(pred).map(p => p.name));
  upd.confirm = null;
  renderUpdates();
}

function renderPkgs(v) {
  const card = $('#up-pkgs');
  const sys = v.system;
  const checked = sys?.listsAt ? `списки обновлены ${fmtAgo(Date.now() / 1000 - sys.listsAt)}` : '';
  const head = h('div', { class: 'up-card-head' },
    h('h2', {}, 'Пакеты системы', sys?.os ? h('small', { text: sys.os }) : ''),
    h('p', { class: 'up-muted', text: checked }),
    sys?.supported ? h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => startTask('/api/updates/check') }, icon('arrows-clockwise'), 'Проверить обновления') : '');
  if (v.error) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: `Не удалось получить список: ${v.error}` }));
    return;
  }
  if (!sys.supported) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: `Пакетный менеджер «${sys.pm}» пока не поддерживается. Причал умеет apt (Debian, Ubuntu), dnf и yum (Fedora, RHEL, Rocky, Alma) и apk (Alpine).` }));
    return;
  }
  v = sys;
  if (!v.packages.length) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: 'Все пакеты обновлены.' }));
    return;
  }
  const quick = h('div', { class: 'up-quick' },
    h('span', { class: 'up-muted', text: 'Выбрать:' }),
    h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => selectOnly(p => p.security) }, 'только безопасность'),
    h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => selectOnly(p => p.group !== 'docker') }, 'всё, кроме Docker'),
    h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => selectOnly(() => true) }, 'всё'),
    upd.selected.size ? h('button', { class: 'btn btn-quiet', disabled: taskRunning(), onclick: () => selectOnly(() => false) }, 'снять выбор') : '');
  const groups = GROUPS.map(g => {
    const pkgs = v.packages.filter(p => p.group === g.key);
    if (!pkgs.length) return '';
    const n = pkgs.filter(p => upd.selected.has(p.name)).length;
    const det = h('details', { class: 'up-group', open: upd.openGroups.has(g.key) },
      h('summary', {},
        groupCheckbox(pkgs),
        h('span', { class: 'up-group-title', text: g.title }),
        h('span', { class: 'up-muted', text: n ? `выбрано ${n} из ${pkgs.length}` : `${pkgs.length}` }),
        h('span', { class: 'ct-caret' }, icon('caret-down'))),
      g.note ? h('p', { class: 'up-group-note', text: g.note }) : '',
      h('ul', { class: 'up-list' }, pkgs.map(pkgRow)));
    det.addEventListener('toggle', () => { det.open ? upd.openGroups.add(g.key) : upd.openGroups.delete(g.key); });
    return det;
  });
  const sel = v.packages.filter(p => upd.selected.has(p.name));
  let bar;
  if (upd.confirm === 'install' && sel.length) {
    const notes = [];
    if (sel.some(p => p.group === 'docker')) {
      notes.push('Docker перезапустится: все контейнеры и эта панель пропадут примерно на полминуты.');
      if (v.init !== 'systemd') notes.push('На этом сервере нет systemd, поэтому обновление Docker может оборвать саму установку. Надёжнее обновить Docker из терминала.');
    }
    if (sel.some(p => p.group === 'kernel')) notes.push('Новое ядро заработает после перезагрузки сервера.');
    notes.push('Установка займёт несколько минут, её ход будет виден вверху страницы.');
    bar = confirmBox({
      q: `Установить ${sel.length} ${plural(sel.length, 'пакет', 'пакета', 'пакетов')}?`,
      text: notes.join(' '), yes: 'Да, установить',
      onYes: () => startTask('/api/updates/install', { packages: sel.map(p => p.name) }), onNo: cancelUpd,
    });
  } else {
    bar = h('div', { class: 'up-bar' },
      h('p', { class: 'up-muted', text: sel.length ? `Выбрано: ${sel.length}` : 'Отметьте, что установить' }),
      h('button', { class: 'btn btn-primary', disabled: !sel.length || taskRunning(), onclick: () => askUpd('install') }, icon('arrow-down'), 'Установить выбранное'));
  }
  card.replaceChildren(head, quick, ...groups, bar);
}

// ---------- Вкладки ----------

const tabs = [$('#tab-containers'), $('#tab-conns'), $('#tab-updates'), $('#tab-images')];
const views = [$('#view-containers'), $('#view-conns'), $('#view-updates'), $('#view-images')];
const hashes = ['#', '#connections', '#updates', '#images'];

function selectTab(i, focus = false) {
  tabs.forEach((t, j) => {
    t.setAttribute('aria-selected', String(i === j));
    t.tabIndex = i === j ? 0 : -1;
    views[j].hidden = i !== j;
  });
  if (focus) tabs[i].focus();
  history.replaceState(null, '', hashes[i]);
  if (i === 1) loadConns();
  if (i === 2) loadUpdates();
  if (i === 3) loadImages();
}

tabs.forEach((t, i) => {
  t.addEventListener('click', () => selectTab(i));
  t.addEventListener('keydown', e => {
    const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
    if (d) selectTab((i + d + tabs.length) % tabs.length, true);
  });
});

// ---------- Старт ----------

document.querySelectorAll('[data-icon]').forEach(el => el.replaceChildren(icon(el.dataset.icon)));
selectTab(Math.max(0, hashes.indexOf(location.hash)));
api('/api/session').then(s => {
  if (!s.auth) return;
  const btn = $('#logout');
  btn.hidden = false;
  btn.addEventListener('click', async () => {
    await api('/api/logout', { method: 'POST' }).catch(() => {});
    location.href = '/login.html';
  });
}).catch(() => {});
refresh().then(schedule);
if (hashes.indexOf(location.hash) !== 1) loadConns();
