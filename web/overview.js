import { $, h, pillRow } from './dom.js';
import { fmtBytes, fmtPct, fmtDur, fmtWhen, plural, cap } from './format.js';
import { about, statusOf } from './model.js';
import { openContainer } from './containers.js';
import { conns, connCount, connBadge } from './conns.js';

// ---------- Состояние сервера ----------

export let hostMem = 0;
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
export function renderTrack(id, values) {
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

export function renderHost(hv) {
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

export function onlineTotal() {
  let n = 0;
  for (const s of conns.values()) if (s.state === 'running' && !s.error) n += connCount(s).online;
  return n;
}

export function renderOverview(ov) {
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
  renderTelegram(ov.telegram, ov.zone || 'МСК');
  renderEvents(ov.events || [], hv);
}

export function sortContainers(list) {
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

const TG_REASONS = ['контейнер упал или перезапускается по кругу', 'сервер перезагрузился', 'диск заполнен на 85 %', 'свободной памяти меньше 8 %'];

let tgShown = null;
function renderTelegram(user, zone) {
  if (tgShown === `${user || ''} ${zone}`) return;
  tgShown = `${user || ''} ${zone}`;
  if (user) {
    $('#tg-row').replaceChildren(pillRow('Telegram', h('span', { class: 'state' }, `@${user}`, h('span', { class: 'knob', 'aria-label': 'включены' }))));
    $('#tg-tags').replaceChildren(...[...TG_REASONS, `сводка обновлений по воскресеньям в 12:00 ${zone}`].map(t => h('span', { class: 'tag', text: t })));
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
