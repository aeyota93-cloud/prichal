import { $, h, icon, toast, confirmBox } from './dom.js';
import { fmtBytes, fmtDur, fmtAgo, dateFmt, plural, cap, shortVer } from './format.js';
import { api } from './api.js';
import { about } from './model.js';
import { rows } from './containers.js';
import { renderHostLine } from './poll.js';

// ---------- Обновления ----------

const GROUPS = [
  { key: 'security', title: 'Безопасность', note: 'Закрывают известные уязвимости. Ставить стоит всегда.', open: true },
  { key: 'docker', title: 'Docker', note: 'Не отмечено заранее: на время установки все контейнеры и панель пропадут примерно на полминуты. Отметьте, когда будет удобно.', open: true },
  { key: 'kernel', title: 'Ядро Linux', note: 'Новое ядро заработает после перезагрузки сервера.', open: true },
  { key: 'other', title: 'Остальное', note: 'Обычные обновления программ системы. Обычно безопасны.', open: false },
];

export const upd = {
  view: null, error: '', checking: false, selected: new Set(), touched: false, log: '', offset: 0, taskId: null, timer: null, confirm: null,
  openGroups: new Set(GROUPS.filter(g => g.open).map(g => g.key)),
  backup: new Map(), // приложение -> делать ли копию данных
};

function taskRunning() {
  return upd.view?.task?.state === 'running';
}

// Рекомендуемое: всё, кроме Docker (его установка на время кладёт все контейнеры).
function recommended(sys) {
  return new Set((sys?.packages || []).filter(p => p.group !== 'docker').map(p => p.name));
}

export async function loadUpdates(fresh = false) {
  if (fresh) $('#up-sum').textContent = 'Проверяю…';
  try {
    const v = await api('/api/updates' + (fresh ? '?fresh=1' : ''));
    upd.view = v;
    upd.error = '';
    const names = new Set((v.system?.packages || []).map(p => p.name));
    // Пока пользователь сам ничего не менял, выбор пересчитывается как «рекомендуемое».
    if (!upd.touched) upd.selected = recommended(v.system);
    else for (const n of [...upd.selected]) if (!names.has(n)) upd.selected.delete(n);
    renderUpdates();
    if (v.task) followTask(v.task);
  } catch (e) {
    upd.error = e.message;
    $('#up-sum').textContent = `Не удалось получить данные: ${e.message}`;
    renderUpdBadge();
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
  renderRebootBtn();
  const busy = upd.checking || taskRunning();
  const btn = $('#up-check');
  btn.disabled = busy;
  btn.classList.toggle('is-busy', upd.checking);
  btn.lastElementChild.textContent = upd.checking ? 'Проверяю…' : 'Проверить обновления';
  renderChecked();
}

// Одна строка времени под кнопкой: берём самое старое из двух времён,
// то есть насколько устарела хотя бы одна из частей.
function renderChecked() {
  const v = upd.view;
  const times = [v?.system?.listsAt, v?.apps?.checkedAt].filter(Boolean);
  $('#up-checked').textContent = upd.checking ? 'идёт проверка'
    : times.length ? `Проверено ${fmtAgo(Date.now() / 1000 - Math.min(...times))}` : '';
}

$('#up-check').addEventListener('click', checkAll);

function appUpdates(v) {
  return (v?.apps?.apps || []).filter(a => a.update).length;
}

// Часть данных не получена: «ничего нет» тогда не значит «всё обновлено».
function checkFailed(v) {
  return !!(v?.error || v?.appsError || (v?.apps?.apps || []).some(a => a.error));
}

// Разбивка обновлений для подсказки бейджа «Обновления» в меню.
// sys — раздел system из /api/updates, apps — число приложений с новой версией.
// Берём только те части, что есть: «8 пакетов системы и 1 приложение · есть
// обновления безопасности · серверу нужна перезагрузка».
function updBadgeTitle(sys, apps) {
  const pk = sys?.supported ? sys.packages.length : 0;
  const what = [];
  if (pk) what.push(`${pk} ${plural(pk, 'пакет', 'пакета', 'пакетов')} системы`);
  if (apps) what.push(`${apps} ${plural(apps, 'приложение', 'приложения', 'приложений')}`);
  const parts = [];
  if (what.length) parts.push(what.join(' и '));
  if (pk && sys.packages.some(p => p.security)) parts.push('есть обновления безопасности');
  if (sys?.rebootRequired) parts.push('серверу нужна перезагрузка');
  return parts.join(' · ');
}

// Бейдж в меню: сколько всего можно обновить. Жёлтый, если есть обновления
// безопасности или нужна перезагрузка.
function renderUpdBadge() {
  const v = upd.view;
  const badge = $('#n-updates');
  const btn = $('#nav-updates');
  if (!v) {
    badge.textContent = upd.error ? '' : '…';
    badge.dataset.tone = '';
    btn.removeAttribute('title');
    btn.removeAttribute('aria-label');
    return;
  }
  const sys = v.system;
  const pk = sys?.supported ? sys.packages.length : 0;
  const total = pk + appUpdates(v);
  const warn = (pk && sys.packages.some(p => p.security)) || !!sys?.rebootRequired;
  badge.textContent = taskRunning() ? '…' : (total || '');
  badge.dataset.tone = warn ? 'warn' : '';
  const tip = updBadgeTitle(sys, appUpdates(v));
  if (tip) {
    btn.title = tip;
    btn.setAttribute('aria-label', `Обновления, ${tip}`);
  } else {
    btn.removeAttribute('title');
    btn.removeAttribute('aria-label');
  }
  renderHostLine();
}

function renderUpdates() {
  const v = upd.view;
  if (!v) return;
  const sys = v.system;
  const pk = sys?.supported ? sys.packages.length : 0;
  const ap = appUpdates(v);
  $('#up-os').textContent = sys ? [sys.os, sys.kernel && `ядро ${sys.kernel.replace(/-generic$/, '')}`].filter(Boolean).join(' · ') : 'Система';
  // Если есть и пакеты, и приложения, заголовок даёт ту же цифру, что и бейдж в меню.
  $('#up-title').textContent = pk && ap ? `Можно обновить ${pk + ap}`
    : pk ? `Можно обновить ${pk} ${plural(pk, 'пакет', 'пакета', 'пакетов')}`
    : ap ? `Можно обновить ${ap} ${plural(ap, 'приложение', 'приложения', 'приложений')}`
    : checkFailed(v) ? 'Проверено не всё' : 'Всё обновлено';
  const parts = [];
  if (v.error) parts.push(`Пакеты: ${v.error}`);
  else if (sys && !sys.supported) parts.push(`Пакетный менеджер ${sys.pm} пока не поддерживается`);
  if (v.appsError) parts.push('приложения не проверены');
  if (pk && ap) parts.push(`${pk} ${plural(pk, 'пакет', 'пакета', 'пакетов')} системы и ${ap} ${plural(ap, 'приложение', 'приложения', 'приложений')}`);
  else if (v.apps && !ap && !checkFailed(v)) parts.push('Приложения на последних версиях');
  if (sys?.rebootRequired) parts.push('нужна перезагрузка');
  $('#up-sum').textContent = parts.join(' · ');
  renderTaskBox();
  renderApps(v);
  renderPkgs(v);
  renderUpdBadge();
  renderCheckButtons();
}

function askUpd(kind) {
  upd.confirm = kind;
  renderUpdates();
}

function cancelUpd() {
  const was = upd.confirm;
  upd.confirm = null;
  renderUpdates();
  if (was === 'install') $('#up-install')?.focus();
}

// ----- Перезагрузка -----

// Контейнеры, которые после перезагрузки сами не поднимутся.
function noAutostart() {
  return [...rows.values()].map(r => r.c)
    .filter(c => (c.state === 'running' || c.state === 'paused') && !['always', 'unless-stopped', 'on-failure'].includes(c.restartPolicy))
    .map(c => about(c).title);
}

// Перезагрузка живёт в боковой панели; подтверждение открывается поверх
// страницы, в узкой колонке ему тесно.
const rebootDialog = h('dialog', { class: 'modal glass', 'aria-label': 'Перезагрузка сервера' });
document.body.append(rebootDialog);

function askReboot() {
  const manual = noAutostart();
  const sys = upd.view?.system;
  const what = sys?.rebootRequired && sys.rebootPkgs?.length ? `Её ждут обновления: ${sys.rebootPkgs.join(', ')}. ` : '';
  const text = what + 'Сервер будет недоступен пару минут. Контейнеры с автозапуском поднимутся сами. SSH-туннель оборвётся: через пару минут подключитесь снова.'
    + (manual.length ? ` Без автозапуска, их придётся запустить вручную: ${manual.join(', ')}.` : '');
  rebootDialog.replaceChildren(confirmBox({
    q: 'Перезагрузить сервер сейчас?', text, yes: 'Да, перезагрузить', danger: true,
    onYes: () => { rebootDialog.close(); doReboot(); },
    onNo: () => rebootDialog.close(),
  }));
  rebootDialog.showModal();
}

rebootDialog.addEventListener('click', e => { if (e.target === rebootDialog) rebootDialog.close(); }); // щелчок мимо окна

async function doReboot() {
  try {
    await api('/api/updates/reboot', { method: 'POST' });
    toast('Сервер перезагружается. Через пару минут подключитесь снова.');
  } catch (e) {
    toast(`Не получилось перезагрузить: ${e.message}`, true);
  }
}

// Две кнопки с одним действием: в боковой панели (на телефоне её нет)
// и в блоке «Сервер» внизу раздела «Обновления».
const rebootBtns = [$('#reboot-btn'), $('#reboot-btn-up')];

function renderRebootBtn() {
  const need = !!upd.view?.system?.rebootRequired;
  for (const btn of rebootBtns) {
    btn.classList.toggle('white', need);
    btn.classList.toggle('ghost', !need);
    btn.disabled = taskRunning();
    btn.lastElementChild.textContent = need ? 'Нужна перезагрузка' : 'Перезагрузить сервер';
    btn.title = need ? 'Новые версии части пакетов заработают после перезагрузки' : '';
  }
  $('#up-server-note').textContent = need
    ? 'Новые версии части пакетов заработают после перезагрузки.'
    : 'Сервер будет недоступен пару минут, контейнеры с автозапуском поднимутся сами.';
}

for (const btn of rebootBtns) btn.addEventListener('click', askReboot);

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
  renderUpdBadge();
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
  // Установка закончилась, а серверу нужна перезагрузка: предлагаем её сразу под задачей.
  const need = t.state === 'done' && upd.view?.system?.rebootRequired;
  box.replaceChildren(wrap, need ? h('div', { class: 'task-reboot glass' },
    h('p', { text: 'Чтобы обновления заработали, серверу нужна перезагрузка' }),
    h('button', { class: 'pbtn white sm', id: 'task-reboot-btn', disabled: taskRunning(), onclick: askReboot }, icon('power'), 'Перезагрузить сейчас')) : '');
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
    h('h2', { text: 'Приложения', title: 'docker compose' }));
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

function groupCheckbox(pkgs, key) {
  const n = pkgs.filter(p => upd.selected.has(p.name)).length;
  const cb = h('input', { type: 'checkbox', class: 'rc', id: `grp-${key}`, 'aria-label': 'Выбрать всю группу' });
  cb.checked = n > 0 && n === pkgs.length;
  cb.indeterminate = n > 0 && n < pkgs.length;
  cb.disabled = taskRunning();
  cb.addEventListener('click', e => e.stopPropagation()); // не сворачивать группу
  cb.addEventListener('change', () => {
    const on = n < pkgs.length;
    for (const p of pkgs) on ? upd.selected.add(p.name) : upd.selected.delete(p.name);
    upd.touched = true;
    upd.confirm = null;
    renderUpdates();
  });
  return cb;
}


function pkgRow(p) {
  const cb = h('input', { type: 'checkbox', class: 'rc', id: `pkg-${p.name}` });
  cb.checked = upd.selected.has(p.name);
  cb.disabled = taskRunning();
  cb.addEventListener('change', () => {
    cb.checked ? upd.selected.add(p.name) : upd.selected.delete(p.name);
    upd.touched = true;
    upd.confirm = null;
    renderUpdates();
  });
  // Описание пакета приходит по-английски из apt: прячем его в подсказку.
  return h('li', { class: 'li', title: p.summary || null },
    cb,
    h('label', { for: `pkg-${p.name}` },
      h('span', { class: 'li-name' }, p.name, p.security && p.group !== 'security' ? h('span', { class: 'tagline', text: 'безопасность' }) : '')),
    h('span', { class: 'li-v', title: `${p.from} → ${p.to}` }, h('span', { text: shortVer(p.from) }), icon('arrow-right'), h('b', { text: shortVer(p.to) })));
}

function selectOnly(pred, touched = true) {
  upd.selected = new Set(upd.view.system.packages.filter(pred).map(p => p.name));
  upd.touched = touched;
  upd.confirm = null;
  renderUpdates();
}

function renderPkgs(v) {
  const card = $('#up-pkgs');
  // Список перестраивается целиком: возвращаем фокус, чтобы с клавиатуры можно было отмечать подряд.
  const focusId = card.contains(document.activeElement) ? document.activeElement.id : '';
  renderPkgsBody(v, card);
  if (focusId) document.getElementById(focusId)?.focus();
}

function renderPkgsBody(v, card) {
  const sys = v.system;
  const head = h('div', { class: 'block-h' }, h('h2', { text: 'Пакеты системы', title: sys?.pm || null }));
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
    h('button', { class: 'textbtn', id: 'q-rec', disabled: taskRunning(), onclick: () => selectOnly(p => p.group !== 'docker', false) }, 'рекомендуемое'),
    h('button', { class: 'textbtn', id: 'q-sec', disabled: taskRunning(), onclick: () => selectOnly(p => p.security) }, 'только безопасность'),
    h('button', { class: 'textbtn', id: 'q-all', disabled: taskRunning(), onclick: () => selectOnly(() => true) }, 'всё'),
    upd.selected.size ? h('button', { class: 'textbtn', id: 'q-none', disabled: taskRunning(), onclick: () => selectOnly(() => false) }, 'снять выбор') : '');
  const groups = GROUPS.map(g => {
    const pkgs = sys.packages.filter(p => p.group === g.key);
    if (!pkgs.length) return '';
    const n = pkgs.filter(p => upd.selected.has(p.name)).length;
    const det = h('details', { class: 'grp', open: upd.openGroups.has(g.key) },
      h('summary', {},
        groupCheckbox(pkgs, g.key),
        h('span', { class: 'grp-title', text: g.title }),
        h('span', { class: 'faint', text: n ? `выбрано ${n} из ${pkgs.length}` : `${pkgs.length}` }),
        h('span', { class: 'ct-caret' }, icon('caret-down'))),
      g.note ? h('p', { class: 'grp-note', text: g.note }) : '',
      h('ul', {}, pkgs.map(pkgRow)));
    det.addEventListener('toggle', () => { det.open ? upd.openGroups.add(g.key) : upd.openGroups.delete(g.key); });
    return det;
  });
  const sel = sys.packages.filter(p => upd.selected.has(p.name));
  // Docker есть в списке, но ни один его пакет не отмечен.
  const dockerPkgs = sys.packages.filter(p => p.group === 'docker');
  const dockerLeft = dockerPkgs.length > 0 && !dockerPkgs.some(p => upd.selected.has(p.name));
  let bar;
  if (upd.confirm === 'install' && sel.length) {
    const notes = [];
    if (sel.some(p => p.group === 'docker')) {
      notes.push('Docker перезапустится: все контейнеры и эта панель пропадут примерно на полминуты.');
      if (sys.init !== 'systemd') notes.push('На этом сервере нет systemd, поэтому обновление Docker может оборвать саму установку. Надёжнее обновить Docker из терминала.');
    }
    if (sel.some(p => p.group === 'kernel')) notes.push('Новое ядро заработает после перезагрузки сервера.');
    if (dockerLeft) notes.push('Docker не будет обновлён, его можно поставить отдельно.');
    notes.push('Установка займёт несколько минут, её ход будет виден вверху страницы.');
    bar = h('div', { class: 'bar' }, confirmBox({
      q: `Установить ${sel.length} ${plural(sel.length, 'пакет', 'пакета', 'пакетов')}?`,
      text: notes.join(' '), yes: 'Да, установить',
      onYes: () => startTask('/api/updates/install', { packages: sel.map(p => p.name) }), onNo: cancelUpd,
    }));
  } else {
    bar = h('div', { class: 'bar' },
      h('p', { class: 'faint', text: sel.length ? `выбрано ${sel.length} из ${sys.packages.length}${dockerLeft ? ' · Docker не отмечен' : ''}` : 'отметьте, что установить' }),
      h('button', { class: 'pbtn white', id: 'up-install', disabled: !sel.length || taskRunning(), onclick: () => askUpd('install') }, 'Установить', icon('arrow-right')));
  }
  card.replaceChildren(head, quick, ...groups, bar);
}
