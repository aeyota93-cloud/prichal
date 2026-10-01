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
const hmFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const timeFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
const fullFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'medium' });

// «09:56», «вчера, 23:10» или «28 сент., 12:00».
function fmtWhen(ms) {
  const d = new Date(ms), now = new Date();
  const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(now) - day(d)) / 86400000);
  if (diff === 0) return hmFmt.format(d);
  if (diff === 1) return `вчера, ${hmFmt.format(d)}`;
  return dateFmt.format(d);
}

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

const cap = s => s ? s[0].toUpperCase() + s.slice(1) : s;

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
      if (c.health === 'unhealthy') return { tone: 'bad', label: 'Нездоров' };
      if (c.health === 'starting') return { tone: 'warn', label: 'Запускается' };
      return { tone: 'ok', label: 'Работает' };
    case 'paused': return { tone: 'warn', label: 'На паузе' };
    case 'restarting': return { tone: 'bad', label: 'Перезапускается' };
    case 'exited':
      if (c.oomKilled) return { tone: 'bad', label: 'Не хватило памяти' };
      if (crashed(c)) return { tone: 'bad', label: 'Упал' };
      return { tone: 'idle', label: 'Остановлен' };
    case 'created': return { tone: 'idle', label: 'Не запускался' };
    case 'dead': return { tone: 'bad', label: 'Сломан' };
    default: return { tone: 'idle', label: c.state };
  }
}

const ACTIONS = {
  start:   { label: 'Запустить', icon: 'play', busy: 'Запускаю…', done: 'запущен', verb: 'запустить' },
  unpause: { label: 'Продолжить', icon: 'play', busy: 'Снимаю с паузы…', done: 'снова работает', verb: 'снять с паузы' },
  restart: { label: 'Перезапустить', icon: 'arrow-clockwise', busy: 'Перезапускаю…', done: 'перезапущен', verb: 'перезапустить', confirm: 'Да, перезапустить' },
  pause:   { label: 'Пауза', icon: 'pause', busy: 'Ставлю на паузу…', done: 'на паузе', verb: 'поставить на паузу', confirm: 'Да, на паузу' },
  stop:    { label: 'Остановить', icon: 'stop', busy: 'Останавливаю…', done: 'остановлен', verb: 'остановить', confirm: 'Да, остановить', danger: true },
  kill:    { label: 'Остановить принудительно', icon: 'lightning', busy: 'Останавливаю…', done: 'остановлен принудительно', verb: 'принудительно остановить', confirm: 'Да, остановить сразу', danger: true },
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

// ---------- Всплывающие сообщения и подтверждения ----------

function toast(text, bad = false) {
  const el = h('div', { class: 'toast' + (bad ? ' is-bad' : '') }, icon(bad ? 'warning-circle' : 'check-circle'), h('p', { text }));
  $('#toasts').append(el);
  setTimeout(() => el.remove(), bad ? 9000 : 4500);
}

function confirmBox({ q, text, yes, danger, onYes, onNo }) {
  const qEl = h('p', { class: 'confirm-q', tabindex: '-1', text: q });
  const box = h('div', { class: 'confirm', 'data-tone': danger ? 'bad' : null, onkeydown: e => { if (e.key === 'Escape') onNo(); } },
    qEl,
    h('p', { class: 'confirm-text', text }),
    h('div', { class: 'row' },
      h('button', { class: 'pbtn sm ' + (danger ? 'danger' : 'white'), onclick: onYes }, yes),
      h('button', { class: 'pbtn sm ghost', onclick: onNo }, 'Отмена')));
  setTimeout(() => qEl.focus());
  return box;
}

const dots = n => h('span', { class: 'dots', 'aria-hidden': 'true' }, Array.from({ length: n }, () => h('i')));

// Строка-таблетка: черта, подпись, точки и что-то справа.
function pillRow(lead, end, tone) {
  return h('div', { class: 'pill-row glass', 'data-tone': tone || null },
    h('span', { class: 'lead', text: lead }), dots(6), h('div', { class: 'end' }, end));
}

// ---------- Состояние сервера ----------

let hostMem = 0;
let lastOverview = null;
const cpuHist = []; // {t, cpu}: загрузка процессора за последние пару минут

function trackCpu(cpu) {
  const now = Date.now();
  if (cpuHist.length && now - cpuHist.at(-1).t > 30000) cpuHist.length = 0; // долго не смотрели
  if (cpu != null) cpuHist.push({ t: now, cpu });
  while (cpuHist.length > 40) cpuHist.shift();
}

function setMeter(id, frac, value, caption, tone) {
  const m = document.getElementById(id);
  m.style.setProperty('--v', Math.min(1, Math.max(0, frac)).toFixed(3));
  m.dataset.tone = tone || '';
  m.querySelector('.m-val').textContent = value;
  document.getElementById(id + '-cap').textContent = caption;
}

function meterTone(frac, warn, bad) {
  return frac >= bad ? 'bad' : frac >= warn ? 'warn' : '';
}

// У памяти и диска на дорожке просто точки, у процессора точки рисуют
// последние пару минут: чем выше точка, тем выше была загрузка.
function renderTrack(id, values) {
  const tr = document.querySelector(`#${id} .m-track`);
  const n = values ? Math.min(values.length, 24) : 7;
  if (tr.children.length !== n) tr.replaceChildren(...Array.from({ length: n }, () => h('i')));
  if (!values) return;
  const v = values.slice(-n);
  const max = Math.max(10, ...v);
  [...tr.children].forEach((d, i) => {
    d.style.transform = `translateY(${((0.5 - v[i] / max) * 18).toFixed(1)}px)`;
    d.style.opacity = (0.35 + 0.65 * (i + 1) / n).toFixed(2);
  });
}

function renderHost(hv) {
  if (!hv) return;
  hostMem = hv.memTotal;
  trackCpu(hv.cpu);
  const vals = cpuHist.map(p => p.cpu);
  const cpu = hv.cpu;
  setMeter('m-cpu', (cpu || 0) / 100, cpu == null ? '…' : fmtPct(cpu),
    vals.length > 3 ? `пик за ${Math.max(1, Math.round((cpuHist.at(-1).t - cpuHist[0].t) / 60000))} мин — ${fmtPct(Math.max(...vals))}` : `${hv.cpus} ${plural(hv.cpus, 'ядро', 'ядра', 'ядер')}`,
    meterTone((cpu || 0) / 100, 0.8, 0.95));
  renderTrack('m-cpu', vals.length > 1 ? vals : null);
  const mem = hv.memUsed / hv.memTotal;
  setMeter('m-mem', mem, fmtPct(mem * 100),
    `${fmtBytes(hv.memUsed)} из ${fmtBytes(hv.memTotal)}` + (hv.swapTotal ? ` · подкачка ${fmtBytes(hv.swapUsed)}` : ''),
    meterTone(mem, 0.8, 0.92));
  renderTrack('m-mem');
  if (hv.diskTotal) {
    const disk = hv.diskUsed / hv.diskTotal;
    setMeter('m-disk', disk, fmtPct(disk * 100), `${fmtBytes(hv.diskUsed)} из ${fmtBytes(hv.diskTotal)}`, meterTone(disk, 0.8, 0.85));
  } else {
    setMeter('m-disk', 0, '—', 'нет данных');
  }
  renderTrack('m-disk');
}

// ---------- Обзор ----------

function onlineTotal() {
  let n = 0;
  for (const s of conns.values()) if (s.state === 'running' && !s.error) n += connCount(s).online;
  return n;
}

function renderOverview(ov) {
  const list = ov.containers;
  const hv = ov.host;
  const bad = list.filter(c => statusOf(c).tone === 'bad');
  const warn = list.filter(c => statusOf(c).tone === 'warn');
  const running = list.filter(c => c.state === 'running').length;
  const mem = hv ? hv.memUsed / hv.memTotal : 0;
  const disk = hv && hv.diskTotal ? hv.diskUsed / hv.diskTotal : 0;

  let title, tone = '';
  if (bad.length === 1) { title = `${about(bad[0]).title}: ${statusOf(bad[0]).label.toLowerCase()}`; tone = 'bad'; }
  else if (bad.length) { title = `Проблемы у ${bad.length} ${plural(bad.length, 'контейнера', 'контейнеров', 'контейнеров')}`; tone = 'bad'; }
  else if (disk >= 0.85) { title = 'Заканчивается место на диске'; tone = 'warn'; }
  else if (mem >= 0.92) { title = 'Заканчивается память'; tone = 'warn'; }
  else if (warn.length) { title = `${about(warn[0]).title}: ${statusOf(warn[0]).label.toLowerCase()}`; tone = 'warn'; }
  else if (!list.length) title = 'Контейнеров пока нет';
  else title = 'Всё работает спокойно';
  $('#ov-title').textContent = title;
  $('#ov-live').dataset.tone = tone;

  const sub = [`${running} из ${list.length} ${plural(list.length, 'контейнера', 'контейнеров', 'контейнеров')} работают`];
  if (conns.size) {
    const n = onlineTotal();
    sub.push(n ? `${n} ${plural(n, 'устройство', 'устройства', 'устройств')} на связи` : 'никого на связи');
  }
  if (hv) sub.push(`${fmtDur(hv.uptime)} без перезагрузки`);
  $('#ov-sub').textContent = sub.join(' · ');

  renderTiles(list);
  renderTelegram(ov.telegram);
  renderEvents(ov.events || [], hv);
}

function sortContainers(list) {
  const collator = new Intl.Collator('ru');
  return [...list].sort((x, y) => (x.self - y.self) || collator.compare(about(x).title, about(y).title));
}

// Перерисовываем, только когда что-то поменялось: иначе каждые 3 секунды
// сбивался бы фокус с плитки.
let tilesKey = '';
function renderTiles(list) {
  const tiles = sortContainers(list).map(c => {
    const a = about(c), st = statusOf(c);
    return {
      id: c.id, tone: st.tone, title: a.title,
      sub: (st.tone === 'ok' ? (connBadge(c.name) || a.sub) : st.label.toLowerCase()) || ' ',
      mem: c.state === 'running' || c.state === 'paused' ? fmtBytes(c.mem) : '',
    };
  });
  const key = JSON.stringify(tiles);
  if (key === tilesKey) return;
  tilesKey = key;
  $('#tiles').replaceChildren(...tiles.map(t => h('button', { class: 'tile glass', 'data-tone': t.tone, onclick: () => openContainer(t.id) },
    h('span', { class: 'sdot' }),
    h('div', {}, h('p', { text: t.title }), h('p', { text: t.sub })),
    h('span', { class: 'num', text: t.mem }))));
}

const TG_REASONS = ['контейнер упал или перезапускается по кругу', 'сервер перезагрузился', 'диск заполнен на 85 %', 'свободной памяти меньше 8 %', 'сводка обновлений по воскресеньям в 12:00 МСК'];

let tgShown = null;
function renderTelegram(user) {
  if (tgShown === (user || '')) return;
  tgShown = user || '';
  if (user) {
    $('#tg-row').replaceChildren(pillRow('Telegram', h('span', { class: 'state' }, `@${user}`, h('span', { class: 'knob', 'aria-label': 'включены' }))));
    $('#tg-tags').replaceChildren(...TG_REASONS.map(t => h('span', { class: 'tag', text: t })));
  } else {
    $('#tg-row').replaceChildren(pillRow('Telegram', h('span', { class: 'state' }, 'выключены', h('span', { class: 'knob off' }))));
    $('#tg-tags').replaceChildren(h('p', { class: 'hint' }, 'Чтобы бот писал о сбоях, задайте ', h('code', { text: 'TG_TOKEN' }), ' и ', h('code', { text: 'TG_USERNAME' }), ' в файле .env на сервере.'));
  }
}

let eventsKey = '';
function renderEvents(events, hv) {
  const list = events.map(e => ({ t: e.t * 1000, text: e.text, tone: e.tone }));
  if (hv) {
    // Запуск сервера знаем всегда, даже если журнал его не застал.
    const boot = Date.now() - hv.uptime * 1000;
    if (!list.some(e => Math.abs(e.t - boot) < 15 * 60000 && /перезагру/.test(e.text))) {
      list.push({ t: boot, text: 'Сервер запущен', tone: '' });
    }
  }
  list.sort((a, b) => b.t - a.t);
  const key = JSON.stringify(list.slice(0, 6).map(e => [fmtWhen(e.t), e.text, e.tone]));
  if (key === eventsKey) return;
  eventsKey = key;
  const box = $('#events');
  if (!list.length) { box.replaceChildren(h('p', { class: 'ev-empty', text: 'Пока ничего не происходило.' })); return; }
  box.replaceChildren(...list.slice(0, 6).map(e => h('div', { class: 'ev', 'data-tone': e.tone || null },
    h('b', { text: fmtWhen(e.t) }), h('span', { text: e.text }))));
}

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

const rows = new Map(); // id -> {el, c, open, mode, ...}
let ctFilter = 'all';

const isNet = c => ['vpn', 'proxy'].includes(about(c).kind);

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

function updateRow(r, c) {
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
function openContainer(id) {
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

function actButton(d, cls, onclick, label) {
  return h('button', { class: 'act' + (cls ? ' ' + cls : ''), onclick }, h('span', { class: 'rb' }, icon(d.icon)), label || d.label);
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
    return actButton(d, i === 0 ? 'primary' : d.danger && act === 'kill' ? 'danger' : '', () => ask(r, act));
  });
  kids.push(h('span', { class: 'act-sep', 'aria-hidden': 'true' }),
    actButton({ icon: 'scroll' }, '', () => { r.logBox.scrollIntoView({ block: 'center', behavior: 'smooth' }); r.logBox.focus({ preventScroll: true }); }, 'Журнал'));
  if (r.c.self) kids.push(h('p', { class: 'ct-note', text: 'Это сама панель: выключить её отсюда нельзя, только перезапустить.' }));
  r.actions.replaceChildren(...kids);
}

function ask(r, act) {
  const d = ACTIONS[act];
  if (!d.confirm) return run(r, act);
  r.mode = 'confirm';
  const a = about(r.c);
  const cancel = () => { renderActions(r, true); r.actions.querySelector('button')?.focus(); };
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

function renderContainers(list) {
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

// ---------- Опрос ----------

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
        $('#ov-label').textContent = ov.label;
        document.title = `Причал · ${ov.label}`;
      }
      renderHost(ov.host);
      renderContainers(ov.containers);
      renderOverview(ov);
      if (imgState.data && !imgState.titled && imgState.mode !== 'busy') renderImages(imgState.data);
      renderHostLine();
    } catch (e) {
      setOffline(true);
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

function renderHostLine() {
  const hv = lastOverview?.host;
  const os = upd.view?.system?.os;
  const parts = [lastOverview?.label, os?.replace(/ LTS$/, ''), hv && `${hv.cpus} ${plural(hv.cpus, 'ядро', 'ядра', 'ядер')}`].filter(Boolean);
  $('#host-line').textContent = parts.join(' · ');
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

let imgState = { mode: 'idle', data: null };

async function loadImages() {
  try {
    const data = await api('/api/images');
    imgState.data = data;
    renderImages(data);
  } catch (e) {
    $('#im-sum').textContent = `Не удалось получить список: ${e.message}`;
  }
}

function imgName(im) {
  return im.tags[0] || 'без имени';
}

function renderPrune(unused, buildCache) {
  if (imgState.mode === 'busy') return;
  const free = unused.reduce((s, i) => s + i.size, 0) + buildCache;
  const what = [];
  if (unused.length) what.push(`${unused.length} ${plural(unused.length, 'неиспользуемый образ', 'неиспользуемых образа', 'неиспользуемых образов')}`);
  if (buildCache) what.push('кэш сборки');
  const end = free
    ? [h('span', { class: 'text', text: what.join(' и ') }), h('button', { class: 'pbtn white sm', onclick: () => askPrune(unused, buildCache) }, icon('broom'), `Очистить ${fmtBytes(free)}`)]
    : h('span', { class: 'state plain', text: 'чистить нечего' });
  $('#prune').replaceChildren(pillRow('Очистка', end));
}

function renderImages(data) {
  const { images, buildCache } = data;
  const total = images.reduce((s, i) => s + i.size, 0);
  const unused = images.filter(i => !i.usedBy.length);
  imgState.titled = identities.size > 0; // иначе вместо названий будут имена в Docker
  $('#im-sum').textContent = `${images.length} ${plural(images.length, 'образ', 'образа', 'образов')} · ${fmtBytes(total)}`;
  $('#n-images').textContent = images.length || '';
  renderPrune(unused, buildCache);

  $('#im-list').replaceChildren(...images.map(im => {
    const used = im.usedBy.length
      ? `нужен: ${im.usedBy.map(n => about({ name: n }).title).join(', ')}`
      : 'не используется';
    const li = h('li', { class: 'li', 'data-tone': im.usedBy.length ? 'ok' : 'idle' },
      h('span', { class: 'sdot' }),
      h('div', {}, h('p', { class: 'li-name', title: im.tags.join(', ') || null, text: imgName(im) }),
        h('p', { class: 'li-sub', text: im.id.replace('sha256:', '').slice(0, 12) })),
      h('p', { class: 'li-v num', text: fmtBytes(im.size) }),
      h('p', { class: 'li-sub', text: fmtAgo(Date.now() / 1000 - im.created) }),
      h('p', { class: 'im-used' + (im.usedBy.length ? '' : ' is-free'), text: used }),
      im.usedBy.length ? h('span') : h('button', { class: 'textbtn', onclick: () => askRemove(li, im) }, icon('trash'), 'Удалить'));
    return li;
  }));
}

function askPrune(unused, buildCache) {
  const list = unused.map(i => `${imgName(i)} (${fmtBytes(i.size)})`);
  if (buildCache) list.push(`кэш сборки (${fmtBytes(buildCache)})`);
  const back = () => renderImages(imgState.data);
  $('#prune').append(confirmBox({
    q: 'Очистить неиспользуемое?',
    text: `Будет удалено: ${list.join(', ')}. Образы, которые нужны контейнерам, останутся, даже если контейнер сейчас остановлен.`,
    yes: 'Да, очистить', danger: true, onYes: doPrune, onNo: back,
  }));
}

async function doPrune() {
  imgState.mode = 'busy';
  $('#prune').replaceChildren(pillRow('Очистка', h('button', { class: 'pbtn white sm is-busy', disabled: true }, icon('arrows-clockwise'), 'Очищаю…')));
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
  const box = confirmBox({
    q: `Удалить образ ${imgName(im)}?`,
    text: 'Он не нужен ни одному контейнеру. Если понадобится снова, Docker скачает его заново.',
    yes: 'Да, удалить', danger: true, onYes: () => removeImage(im), onNo: () => box.remove(),
  });
  li.append(box);
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
let connSel = null; // какой сервис открыт в разделе

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
function renderRowClients(r) {
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

async function loadConns() {
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

// ---------- Обновления ----------

const GROUPS = [
  { key: 'security', title: 'Безопасность', note: '', open: true },
  { key: 'docker', title: 'Docker', note: 'При установке Docker перезапустится: все контейнеры, включая эту панель, пропадут примерно на полминуты. Панель вернётся сама и покажет результат.', open: true },
  { key: 'kernel', title: 'Ядро Linux', note: 'Новое ядро заработает после перезагрузки сервера.', open: true },
  { key: 'other', title: 'Остальное', note: '', open: false },
];

const upd = {
  view: null, error: '', checking: false, selected: new Set(), log: '', offset: 0, taskId: null, timer: null, confirm: null,
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
    upd.error = '';
    const names = new Set((v.system?.packages || []).map(p => p.name));
    for (const n of [...upd.selected]) if (!names.has(n)) upd.selected.delete(n);
    renderUpdates();
    if (v.task) followTask(v.task);
  } catch (e) {
    upd.error = e.message;
    $('#up-sum').textContent = `Не удалось получить данные: ${e.message}`;
    renderUpdCard();
  }
}

// Одна кнопка проверяет всё: приложения и список пакетов сразу, а затем
// обновляет списки пакетов на сервере (это задача, её ход виден ниже).
async function checkAll() {
  if (upd.checking || taskRunning()) return;
  upd.checking = true;
  renderCheckButtons();
  await loadUpdates(true);
  if (upd.view?.system?.supported && !taskRunning()) await startTask('/api/updates/check');
  upd.checking = false;
  renderCheckButtons();
}

function renderCheckButtons() {
  const busy = upd.checking || taskRunning();
  const btn = $('#up-check');
  btn.disabled = busy;
  btn.classList.toggle('is-busy', upd.checking);
  btn.lastElementChild.textContent = upd.checking ? 'Проверяю…' : 'Проверить обновления';
  document.querySelectorAll('#view-updates .ibtn').forEach(b => { b.disabled = busy; });
}

$('#up-check').addEventListener('click', checkAll);

// Круглая кнопка без подписи: что она делает, видно в подсказке.
function iconBtn(title, onclick) {
  return h('button', { class: 'ibtn', title, 'aria-label': title, disabled: upd.checking || taskRunning(), onclick }, icon('arrows-clockwise'));
}

function appUpdates(v) {
  return (v?.apps?.apps || []).filter(a => a.update).length;
}

// Карточка в боковой панели: сколько всего можно обновить.
function renderUpdCard() {
  const v = upd.view;
  const card = $('#upd-card');
  if (!v) {
    $('#upd-num').textContent = upd.error ? '—' : '…';
    $('#upd-what').textContent = upd.error ? 'не удалось проверить' : 'проверяю';
    return;
  }
  const sys = v.system;
  const pk = sys?.supported ? sys.packages.length : 0;
  const ap = appUpdates(v);
  const total = pk + ap;
  const running = taskRunning();
  card.classList.toggle('is-due', total > 0 || !!sys?.rebootRequired);
  $('#upd-num').textContent = running ? '…' : String(total);
  $('#upd-what').replaceChildren(...(running ? ['идёт', h('br'), 'установка']
    : pk ? [plural(pk, 'пакет', 'пакета', 'пакетов'), h('br'), 'системы']
    : ap ? [plural(ap, 'приложение', 'приложения', 'приложений'), h('br'), 'обновить']
    : ['всё', h('br'), 'обновлено']));
  const note = [];
  if (sys?.packages?.some(p => p.group === 'docker')) note.push(`Docker ${shortVer(sys.packages.find(p => p.name === 'docker-ce')?.to || '').replace(/-.*$/, '')}`.trim());
  if (sys?.packages?.some(p => p.group === 'kernel')) note.push('ядро');
  if (sys?.packages?.some(p => p.security)) note.push('безопасность');
  let noteText = note.length ? `Среди них: ${note.join(', ')}.` : '';
  if (sys?.rebootRequired) noteText += (noteText ? ' ' : '') + 'Серверу нужна перезагрузка.';
  $('#upd-note').textContent = noteText;
  $('#upd-apps').textContent = v.apps ? (ap ? `Приложений с новой версией: ${ap}` : 'Приложения актуальны') : '';
  $('#n-updates').textContent = total || '';
  renderHostLine();
}

function renderUpdates() {
  const v = upd.view;
  if (!v) return;
  const sys = v.system;
  const pk = sys?.supported ? sys.packages.length : 0;
  const ap = appUpdates(v);
  $('#up-os').textContent = sys ? [sys.os, sys.kernel && `ядро ${sys.kernel.replace(/-generic$/, '')}`].filter(Boolean).join(' · ') : 'Система';
  $('#up-title').textContent = pk ? `Можно обновить ${pk} ${plural(pk, 'пакет', 'пакета', 'пакетов')}`
    : ap ? `Можно обновить ${ap} ${plural(ap, 'приложение', 'приложения', 'приложений')}`
    : v.error ? 'Обновления' : 'Всё обновлено';
  const parts = [];
  if (v.error) parts.push(`Пакеты: ${v.error}`);
  else if (sys && !sys.supported) parts.push(`Пакетный менеджер ${sys.pm} пока не поддерживается`);
  if (pk && ap) parts.push(`и ${ap} ${plural(ap, 'приложение', 'приложения', 'приложений')}`);
  else if (v.apps && !ap) parts.push('Приложения на последних версиях');
  if (sys?.rebootRequired) parts.push('нужна перезагрузка');
  $('#up-sum').textContent = parts.join(' · ');
  renderReboot(sys);
  renderTaskBox();
  renderApps(v);
  renderPkgs(v);
  renderFoot(sys);
  renderUpdCard();
  renderCheckButtons();
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
  const what = sys.rebootPkgs.length ? `Обновились: ${sys.rebootPkgs.join(', ')}. ` : '';
  box.replaceChildren(h('div', { class: 'banner glass' },
    h('span', { class: 'sdot', 'data-tone': 'warn' }),
    h('div', { class: 'text' },
      h('b', { text: 'Серверу нужна перезагрузка' }),
      h('span', { text: `${what}Новые версии заработают после неё. Сделайте её, когда будет удобно.` })),
    upd.confirm === 'reboot-banner' ? '' : h('button', { class: 'pbtn white sm', disabled: taskRunning(), onclick: () => askUpd('reboot-banner') }, icon('power'), 'Перезагрузить')),
  upd.confirm === 'reboot-banner' ? rebootConfirm() : '');
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
  foot.replaceChildren(
    pillRow('Перезагрузка', [h('span', { class: 'text', text: 'не требуется' }),
      upd.confirm === 'reboot-foot' ? '' : h('button', { class: 'pbtn ghost sm', disabled: taskRunning(), onclick: () => askUpd('reboot-foot') }, icon('power'), 'Перезагрузить сервер')]),
    upd.confirm === 'reboot-foot' ? rebootConfirm() : '');
}

// ----- Задача и её журнал -----

const TASK_STATE = {
  running: { tone: 'warn', label: 'идёт' },
  done: { tone: 'ok', label: 'готово' },
  failed: { tone: 'bad', label: 'ошибка' },
  interrupted: { tone: 'bad', label: 'прервано' },
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
  renderUpdCard();
  renderCheckButtons();
  if (t.state === 'running') {
    upd.timer = setTimeout(pollTask, 2000);
  } else if (wasRunning) {
    toast(t.state === 'done' ? `Готово: ${t.title}` : `${cap(TASK_STATE[t.state].label)}: ${t.title}`, t.state !== 'done');
    loadUpdates();
  }
}

function renderTaskBox() {
  const box = $('#up-task');
  const t = upd.view?.task;
  if (!t) { box.replaceChildren(); return; }
  const st = TASK_STATE[t.state] || TASK_STATE.running;
  let when = `${st.label} · ${dateFmt.format(t.started * 1000)}`;
  if (t.finished) when += `, заняло ${fmtDur(t.finished - t.started)}`;
  if (t.state === 'failed' && t.exit != null) when += `, код ${t.exit}`;
  if (t.state === 'interrupted') when += ', сервер перезагрузился посреди задачи';
  let pre = box.querySelector('.logs');
  const keepBottom = !pre || pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
  const wasOpen = box.querySelector('details')?.open;
  const head = [
    h('span', { class: 'sdot', 'data-tone': st.tone }),
    h('p', { class: 'task-title', text: cap(t.title) }),
    h('p', { class: 'task-when', text: when }),
  ];
  pre = h('pre', { class: 'logs', tabindex: '0', 'aria-label': 'Ход выполнения' }, upd.log || 'Жду первых строк…');
  const wrap = t.state === 'running'
    ? h('div', { class: 'task glass is-busy' }, h('div', { class: 'task-head' }, head), pre)
    : h('details', { class: 'task glass', open: wasOpen || null }, h('summary', { class: 'task-head' }, head, h('span', { class: 'ct-caret' }, icon('caret-down'))), pre);
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
  let status, action = '', note = '', tone = 'ok';
  if (a.error) {
    tone = 'bad';
    status = h('p', { class: 'li-sub', text: a.error });
  } else if (a.git && a.git.behind > 0) {
    tone = 'warn';
    const n = a.git.behind;
    status = h('div', {},
      h('p', {}, `В git ${n} ${plural(n, 'новый коммит', 'новых коммита', 'новых коммитов')}`, h('span', { class: 'faint', text: ` · ${a.git.current} → ${a.git.latest}` })),
      h('ul', { class: 'commits' }, a.git.commits.map(c => h('li', { text: c }))),
      a.git.dirty ? h('p', { class: 'app-note', text: 'На сервере изменены файлы проекта: если они пересекаются с новыми, обновление остановится с ошибкой.' }) : '',
      a.git.diverged ? h('p', { class: 'app-note', text: 'На сервере есть собственные коммиты: обновить отсюда нельзя, нужно слияние вручную.' }) : '');
    action = h('div', { class: 'app-actions' },
      h('button', { class: 'pbtn white sm', disabled: taskRunning() || a.git.diverged, onclick: () => askUpd('git:' + a.key) }, icon('arrow-down'), 'Обновить'));
  } else if (a.git) {
    status = h('p', { class: 'muted', text: `последняя версия из git (${a.git.current})` });
  } else if (a.gitError) {
    tone = 'idle';
    status = h('p', { class: 'li-sub', text: `Образ собран на этом сервере. Проверить git не вышло: ${a.gitError}` });
  } else if (a.local) {
    tone = 'idle';
    status = h('p', { class: 'li-sub', text: 'Образ собран на этом сервере: сравнивать не с чем.' });
  } else if (a.update) {
    tone = 'warn';
    status = h('p', {}, 'Вышла ', a.latest && a.latest !== a.current ? `версия ${a.latest}` : 'новая сборка',
      a.built ? h('span', { class: 'faint', text: ` от ${dateFmt.format(Date.parse(a.built)).replace(/,.*$/, '')}` }) : '');
    const size = a.volumes.reduce((n, v) => n + Math.max(0, v.size), 0);
    const cb = h('input', { type: 'checkbox', id: `bk-${a.key}` });
    cb.checked = wantBackup(a);
    cb.disabled = taskRunning();
    cb.addEventListener('change', () => { upd.backup.set(a.key, cb.checked); });
    action = h('div', { class: 'app-actions' },
      a.volumes.length ? h('label', { class: 'check', for: `bk-${a.key}` }, cb, `копия данных${size ? ` (${fmtBytes(size)})` : ''}`) : '',
      h('button', { class: 'pbtn white sm', disabled: taskRunning(), onclick: () => askUpd('app:' + a.key) }, icon('arrow-down'), 'Обновить'));
  } else {
    status = h('p', { class: 'muted', text: 'последняя версия' });
  }
  if (a.pinned) {
    const tag = a.image.split('@')[0].split(':').pop();
    note = h('p', { class: 'app-note', text: `Версия закреплена тегом «${tag}»: новые версии придут, только если поменять тег в docker-compose.` });
  }
  const li = h('li', { class: 'li app-row', 'data-tone': tone },
    h('span', { class: 'sdot' }),
    h('div', {},
      h('p', { class: 'li-name', text: a.title }),
      h('p', { class: 'li-sub', text: [appWhere(a), vers && `у вас ${vers}`].filter(Boolean).join(' · ') })),
    h('div', {}, status, note),
    action || h('span'));
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
  const head = h('div', { class: 'block-h' },
    h('h2', { text: 'Приложения' }),
    h('span', { class: 'faint', text: ['docker compose', v.apps && `проверено ${fmtAgo(Date.now() / 1000 - v.apps.checkedAt)}`].filter(Boolean).join(' · ') }),
    iconBtn('Проверить приложения', () => loadUpdates(true)));
  const parts = [head];
  if (v.appsError) parts.push(h('p', { class: 'cn-empty', text: `Не удалось проверить: ${v.appsError}` }));
  if (apps.length) parts.push(h('ul', {}, apps.map(appRow)));
  else if (!v.appsError) parts.push(h('p', { class: 'cn-empty', text: 'Приложений на docker compose нет.' }));
  if (manual.length) {
    parts.push(h('details', { class: 'grp' },
      h('summary', {},
        h('span', { class: 'grp-title', text: `Обновляются не отсюда: ${manual.length}` }),
        h('span', { class: 'ct-caret' }, icon('caret-down'))),
      h('ul', {}, manual.map(m => h('li', { class: 'li', 'data-tone': 'idle' },
        h('span', { class: 'sdot' }),
        h('div', {}, h('p', { class: 'li-name', text: m.title }), h('p', { class: 'li-sub', text: `${m.name !== m.title ? m.name + ', ' : ''}${m.reason}` })),
        h('span'))))));
  }
  card.replaceChildren(...parts);
}

// ----- Пакеты -----

function groupCheckbox(pkgs) {
  const n = pkgs.filter(p => upd.selected.has(p.name)).length;
  const cb = h('input', { type: 'checkbox', class: 'rc', 'aria-label': 'Выбрать всю группу' });
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

// «5:29.8.1-1~ubuntu.24.04~noble» -> «29.8.1-1»: эпоха и хвост дистрибутива
// только мешают читать. Полная версия остаётся в подсказке.
const shortVer = v => v.replace(/^\d+:/, '').replace(/[~+].*$/, '');

function pkgRow(p) {
  const cb = h('input', { type: 'checkbox', class: 'rc', id: `pkg-${p.name}` });
  cb.checked = upd.selected.has(p.name);
  cb.disabled = taskRunning();
  cb.addEventListener('change', () => {
    cb.checked ? upd.selected.add(p.name) : upd.selected.delete(p.name);
    upd.confirm = null;
    renderUpdates();
  });
  return h('li', { class: 'li' },
    cb,
    h('label', { for: `pkg-${p.name}` },
      h('span', { class: 'li-name' }, p.name, p.security && p.group !== 'security' ? h('span', { class: 'tagline', text: 'безопасность' }) : ''),
      h('span', { class: 'li-sub', text: p.summary || '' })),
    h('span', { class: 'li-v', title: `${p.from} → ${p.to}` }, h('span', { text: shortVer(p.from) }), icon('arrow-right'), h('b', { text: shortVer(p.to) })));
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
  const head = h('div', { class: 'block-h' },
    h('h2', { text: 'Пакеты системы' }),
    h('span', { class: 'faint', text: [sys?.pm, checked].filter(Boolean).join(' · ') }),
    sys?.supported ? iconBtn('Проверить пакеты: обновить их списки на сервере', () => startTask('/api/updates/check')) : '');
  if (v.error) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: `Не удалось получить список: ${v.error}` }));
    return;
  }
  if (!sys.supported) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: `Пакетный менеджер «${sys.pm}» пока не поддерживается. Причал умеет apt (Debian, Ubuntu), dnf и yum (Fedora, RHEL, Rocky, Alma) и apk (Alpine).` }));
    return;
  }
  if (!sys.packages.length) {
    card.replaceChildren(head, h('p', { class: 'cn-empty', text: 'Все пакеты обновлены.' }));
    return;
  }
  const quick = h('div', { class: 'quick' },
    h('span', { class: 'faint', text: 'Выбрать:' }),
    h('button', { class: 'textbtn', disabled: taskRunning(), onclick: () => selectOnly(p => p.security) }, 'только безопасность'),
    h('button', { class: 'textbtn', disabled: taskRunning(), onclick: () => selectOnly(p => p.group !== 'docker') }, 'всё, кроме Docker'),
    h('button', { class: 'textbtn', disabled: taskRunning(), onclick: () => selectOnly(() => true) }, 'всё'),
    upd.selected.size ? h('button', { class: 'textbtn', disabled: taskRunning(), onclick: () => selectOnly(() => false) }, 'снять выбор') : '');
  const groups = GROUPS.map(g => {
    const pkgs = sys.packages.filter(p => p.group === g.key);
    if (!pkgs.length) return '';
    const n = pkgs.filter(p => upd.selected.has(p.name)).length;
    const det = h('details', { class: 'grp', open: upd.openGroups.has(g.key) },
      h('summary', {},
        groupCheckbox(pkgs),
        h('span', { class: 'grp-title', text: g.title }),
        h('span', { class: 'faint', text: n ? `выбрано ${n} из ${pkgs.length}` : `${pkgs.length}` }),
        h('span', { class: 'ct-caret' }, icon('caret-down'))),
      g.note ? h('p', { class: 'grp-note', text: g.note }) : '',
      h('ul', {}, pkgs.map(pkgRow)));
    det.addEventListener('toggle', () => { det.open ? upd.openGroups.add(g.key) : upd.openGroups.delete(g.key); });
    return det;
  });
  const sel = sys.packages.filter(p => upd.selected.has(p.name));
  let bar;
  if (upd.confirm === 'install' && sel.length) {
    const notes = [];
    if (sel.some(p => p.group === 'docker')) {
      notes.push('Docker перезапустится: все контейнеры и эта панель пропадут примерно на полминуты.');
      if (sys.init !== 'systemd') notes.push('На этом сервере нет systemd, поэтому обновление Docker может оборвать саму установку. Надёжнее обновить Docker из терминала.');
    }
    if (sel.some(p => p.group === 'kernel')) notes.push('Новое ядро заработает после перезагрузки сервера.');
    notes.push('Установка займёт несколько минут, её ход будет виден вверху страницы.');
    bar = h('div', { class: 'bar' }, confirmBox({
      q: `Установить ${sel.length} ${plural(sel.length, 'пакет', 'пакета', 'пакетов')}?`,
      text: notes.join(' '), yes: 'Да, установить',
      onYes: () => startTask('/api/updates/install', { packages: sel.map(p => p.name) }), onNo: cancelUpd,
    }));
  } else {
    bar = h('div', { class: 'bar' },
      h('p', { class: 'faint', text: sel.length ? `выбрано ${sel.length} из ${sys.packages.length}` : 'отметьте, что установить' }),
      h('button', { class: 'pbtn white', disabled: !sel.length || taskRunning(), onclick: () => askUpd('install') }, 'Установить', icon('arrow-right')));
  }
  card.replaceChildren(head, quick, ...groups, bar);
}

// ---------- Разделы ----------

const VIEWS = { overview: '#', containers: '#containers', conns: '#connections', updates: '#updates', images: '#images' };
let currentView = 'overview';

function go(view) {
  if (!VIEWS[view]) view = 'overview';
  currentView = view;
  for (const v of Object.keys(VIEWS)) document.getElementById(`view-${v}`).hidden = v !== view;
  document.querySelectorAll('.nav button').forEach(b => b.dataset.view === view ? b.setAttribute('aria-current', 'page') : b.removeAttribute('aria-current'));
  history.replaceState(null, '', VIEWS[view] === '#' ? location.pathname : VIEWS[view]);
  window.scrollTo(0, 0);
  if (view === 'conns') loadConns();
  if (view === 'updates') loadUpdates();
  if (view === 'images') loadImages();
}

document.querySelectorAll('[data-view]').forEach(b => b.addEventListener('click', () => go(b.dataset.view)));

// ---------- Старт ----------

document.querySelectorAll('[data-icon]').forEach(el => el.replaceChildren(icon(el.dataset.icon)));
renderTrack('m-mem');
renderTrack('m-disk');
renderTrack('m-cpu');
const viewFromHash = () => Object.keys(VIEWS).find(v => VIEWS[v] === location.hash) || 'overview';
const startView = viewFromHash();
go(startView);
window.addEventListener('hashchange', () => go(viewFromHash()));
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
if (startView !== 'conns') loadConns();
if (startView !== 'updates') loadUpdates();
if (startView !== 'images') loadImages();
