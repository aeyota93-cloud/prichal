import { h, icon, toast, confirmBox } from './dom.js';
import { api } from './api.js';
import { refresh } from './poll.js';

// Окно «Telegram»: подключить бота, изменить, проверить, отключить.
// Токен живёт только в поле формы: ни в переменных модуля, ни в браузере его нет.

const dlg = h('dialog', { class: 'modal glass', 'aria-label': 'Telegram' });
document.body.append(dlg);

let status = null;  // последнее известное состояние бота
let view = '';      // form | wait | on | off
let gen = 0;        // номер показа: ответы, пришедшие после смены вида, выбрасываем
let timer = null;   // опрос в виде «ждём Start»
let noteEl = null;  // заметка про .env в форме
let linkUpdate = null; // обновляет ссылку на бота в виде «ждём Start»

const POLL = 3000;
const LOST = 'Нет связи с панелью. Проверьте подключение и попробуйте ещё раз.';

const errText = e => (e.status ? e.message : LOST);

// ---------- Общие части ----------

function head(title, headingEl) {
  return h('div', { class: 'tg-head' },
    headingEl,
    h('button', { class: 'tg-x', type: 'button', 'aria-label': 'Закрыть', onclick: () => dlg.close() }, icon('x')));
}

function heading(text) {
  return h('h2', { class: 'tg-title', tabindex: '-1', text });
}

// Сообщение в окне: итог действия или ошибка. role=status читают вслух.
function msgLine(text, bad) {
  const p = h('p', { class: 'tg-msg', role: 'status', 'data-tone': bad ? 'bad' : null });
  setMsg(p, text, bad);
  return p;
}

function setMsg(p, text, bad) {
  p.hidden = !text;
  p.dataset.tone = bad ? 'bad' : '';
  p.replaceChildren(...(text ? [icon(bad ? 'warning-circle' : 'check-circle'), h('span', { text })] : []));
}

function busyBtn(btn, busy, label, idle) {
  btn.disabled = busy;
  btn.classList.toggle('is-busy', busy);
  btn.replaceChildren(...(busy ? [icon('arrows-clockwise'), label] : [idle]));
}

// ---------- Опрос «ждём Start» ----------

function stopWait() {
  clearTimeout(timer);
  timer = null;
}

function waitTick(my, delay) {
  timer = setTimeout(async () => {
    if (my !== gen) return;
    try {
      const s = await api('/api/telegram');
      if (my !== gen) return;
      status = s;
      if (s.state === 'on') { refresh(); show('on', { msg: 'Готово! Бот прислал приветствие.' }); return; }
      if (s.state === 'off') { refresh(); show('form'); return; }
      if (linkUpdate) linkUpdate();
    } catch { /* следующая попытка через 3 секунды */ }
    if (my === gen) waitTick(my, POLL);
  }, delay);
}

// ---------- Вид а): форма ----------

function formView({ edit }) {
  const title = edit ? 'Изменить Telegram' : 'Подключить Telegram';
  const titleEl = heading(title);

  const tokenIn = h('input', {
    id: 'tg-token', class: 'tg-input', type: 'password', autocomplete: 'off', spellcheck: 'false',
    placeholder: edit ? 'оставьте пустым, чтобы не менять' : null,
  });
  const userIn = h('input', {
    id: 'tg-user', class: 'tg-input', type: 'text', autocomplete: 'off', spellcheck: 'false',
    autocapitalize: 'off', 'aria-describedby': 'tg-user-hint',
  });
  if (edit) userIn.value = status.user || '';
  const err = h('p', { id: 'tg-err', class: 'tg-err', role: 'alert', hidden: true });

  noteEl = h('p', { class: 'tg-note', hidden: status.source !== 'env',
    text: 'Сейчас настройки взяты из файла .env на сервере. После сохранения здесь панель будет использовать свои, а строки TG_ в .env можно удалить.' });

  const how = h('details', { class: 'tg-how', open: !edit },
    h('summary', { text: 'Где взять токен' }),
    h('ol', {},
      h('li', {}, 'Откройте в Telegram ', h('a', { href: 'https://t.me/BotFather', target: '_blank', rel: 'noopener', text: '@BotFather' }), ' и отправьте /newbot.'),
      h('li', { text: 'Придумайте боту имя и адрес, BotFather пришлёт токен.' }),
      h('li', { text: 'Скопируйте токен целиком и вставьте ниже.' })));

  const submit = h('button', { class: 'pbtn white', type: 'submit' }, edit ? 'Сохранить' : 'Подключить');
  const cancel = h('button', { class: 'pbtn ghost', type: 'button',
    onclick: () => (edit ? show(status.state === 'on' ? 'on' : 'wait') : dlg.close()) }, 'Отмена');

  function clearErr() {
    err.hidden = true;
    err.textContent = '';
    for (const el of [tokenIn, userIn]) el.removeAttribute('aria-invalid');
    tokenIn.removeAttribute('aria-describedby');
    userIn.setAttribute('aria-describedby', 'tg-user-hint');
  }

  function fail(text) {
    err.textContent = text;
    err.hidden = false;
    const bad = /^Ник/.test(text) ? userIn : /токен/i.test(text) ? tokenIn : null;
    if (bad) {
      bad.setAttribute('aria-invalid', 'true');
      bad.setAttribute('aria-describedby', bad === userIn ? 'tg-user-hint tg-err' : 'tg-err');
    }
    (bad || tokenIn).focus();
  }

  tokenIn.addEventListener('input', clearErr);
  userIn.addEventListener('input', clearErr);

  const form = h('form', { class: 'tg-form', novalidate: true, onsubmit: async e => {
    e.preventDefault();
    if (submit.disabled) return;
    clearErr();
    const my = gen;
    const fields = [tokenIn, userIn, cancel];
    for (const el of fields) el.disabled = true;
    busyBtn(submit, true, 'Проверяю токен…');
    try {
      const s = await api('/api/telegram', { method: 'POST', body: { token: tokenIn.value.trim(), username: userIn.value.trim() } });
      status = s;
      refresh();
      if (my === gen) show(s.state === 'on' ? 'on' : 'wait', s.state === 'on' ? { msg: 'Сохранено.' } : {});
    } catch (e2) {
      if (my !== gen) return;
      for (const el of fields) el.disabled = false;
      busyBtn(submit, false, '', edit ? 'Сохранить' : 'Подключить');
      fail(errText(e2));
    }
  } },
    h('div', { class: 'tg-field' },
      h('label', { class: 'tg-label', for: 'tg-token', text: 'Токен бота' }), tokenIn),
    h('div', { class: 'tg-field' },
      h('label', { class: 'tg-label', for: 'tg-user', text: 'Ваш ник в Telegram' }),
      h('div', { class: 'tg-at' }, h('span', { 'aria-hidden': 'true', text: '@' }), userIn),
      h('p', { id: 'tg-user-hint', class: 'tg-hint', text: 'Бот будет писать только этому человеку. Если ника нет, задайте его в настройках Telegram.' })),
    err,
    h('div', { class: 'tg-btns' }, submit, cancel));

  return { title, focus: tokenIn, node: h('div', { class: 'tg-box' }, head(title, titleEl), how, noteEl, form) };
}

// ---------- Вид б): ждём Start ----------

function waitView({ msg, bad }) {
  const title = 'Остался один шаг';
  const titleEl = heading(title);
  const open = h('a', { class: 'pbtn white tg-big', target: '_blank', rel: 'noopener' });
  const pending = h('button', { class: 'pbtn white tg-big', type: 'button', disabled: true }, icon('arrows-clockwise'), 'Определяю бота…');
  pending.classList.add('is-busy');
  // Имя бота при настройке из .env узнаётся не сразу: пока его нет, ссылки нет.
  linkUpdate = () => {
    const has = !!status.bot;
    open.hidden = !has;
    pending.hidden = has;
    if (has) {
      open.href = `https://t.me/${encodeURIComponent(status.bot)}`;
      open.textContent = `Открыть @${status.bot}`;
    }
  };
  linkUpdate();
  waitTick(gen, status.bot ? POLL : 1000);

  return { title, focus: titleEl, node: h('div', { class: 'tg-box' },
    head(title, titleEl),
    ...(msg ? [msgLine(msg, bad)] : []),
    h('p', { class: 'tg-text', text: 'Откройте бота и нажмите Start. Он пришлёт приветствие, и уведомления заработают.' }),
    open, pending,
    h('p', { class: 'tg-live is-busy', role: 'status' }, h('span', { class: 'sdot', 'data-tone': 'warn' }), `жду нажатия… (ник @${status.user})`),
    h('div', { class: 'tg-btns' },
      h('button', { class: 'pbtn ghost sm', type: 'button', onclick: () => show('form', { edit: true }) }, 'Изменить'),
      h('button', { class: 'pbtn ghost sm', type: 'button', onclick: () => show('off') }, 'Отключить'))) };
}

// ---------- Вид в): подключено ----------

function onView({ msg, bad }) {
  const title = 'Telegram подключён';
  const titleEl = heading(title);
  const res = msgLine(msg, bad);
  const test = h('button', { class: 'pbtn white sm', type: 'button' }, 'Прислать проверку');
  test.addEventListener('click', async () => {
    const my = gen;
    setMsg(res, '');
    busyBtn(test, true, 'Отправляю…');
    try {
      await api('/api/telegram/test', { method: 'POST' });
      if (my === gen) setMsg(res, 'Отправил, проверьте Telegram');
    } catch (e) {
      if (my === gen) setMsg(res, errText(e), true);
    }
    if (my === gen) busyBtn(test, false, '', 'Прислать проверку');
  });
  const who = status.bot ? `Бот @${status.bot} пишет вам (@${status.user}).` : `Бот пишет вам (@${status.user}).`;

  return { title, focus: titleEl, node: h('div', { class: 'tg-box' },
    head(title, titleEl),
    h('p', { class: 'tg-text', text: who }),
    res,
    h('div', { class: 'tg-btns' },
      test,
      h('button', { class: 'pbtn ghost sm', type: 'button', onclick: () => show('form', { edit: true }) }, 'Изменить'),
      h('button', { class: 'pbtn ghost sm', type: 'button', onclick: () => show('off') }, 'Отключить'),
      h('button', { class: 'pbtn ghost sm', type: 'button', onclick: () => dlg.close() }, 'Закрыть'))) };
}

// ---------- Вид г): отключить? ----------

function back() {
  show(status && status.state === 'on' ? 'on' : 'wait', {});
}

function offView() {
  const my = gen;
  const node = confirmBox({
    q: 'Отключить уведомления в Telegram?',
    text: 'Бот перестанет писать о сбоях и присылать сводку. Подключить снова можно здесь же.',
    yes: 'Да, отключить', danger: true, onNo: back,
    onYes: async () => {
      node.querySelectorAll('button').forEach(b => { b.disabled = true; });
      try {
        status = await api('/api/telegram/off', { method: 'POST' });
        refresh();
        if (my === gen) dlg.close();
        toast('Уведомления отключены');
      } catch (e) {
        if (my === gen) show(status && status.state === 'on' ? 'on' : 'wait', { msg: `Не получилось отключить: ${errText(e)}`, bad: true });
      }
    },
  });
  return { title: 'Отключить Telegram', focus: null, node };
}

// ---------- Окно ----------

const VIEWS = { form: formView, wait: waitView, on: onView, off: () => offView() };

function show(name, opts = {}) {
  stopWait();
  linkUpdate = null;
  noteEl = null;
  gen++;
  view = name;
  const v = VIEWS[name](opts);
  dlg.setAttribute('aria-label', v.title);
  dlg.replaceChildren(v.node);
  if (!dlg.open) dlg.showModal();
  if (v.focus) v.focus.focus();
}

export function openTelegram(st) {
  status = { ...st };
  show(st.state === 'on' ? 'on' : st.state === 'waiting' ? 'wait' : 'form');
  // Источник настроек и свежее состояние спрашиваем уже при открытом окне.
  const my = gen;
  api('/api/telegram').then(s => {
    if (my !== gen) return;
    status = { ...status, ...s };
    if (view === 'form') { if (noteEl) noteEl.hidden = s.source !== 'env'; return; }
    const now = s.state === 'on' ? 'on' : s.state === 'waiting' ? 'wait' : 'form';
    if ((view === 'on' ? 'on' : 'wait') !== now) show(now);
  }).catch(() => {});
}

// Esc в «Отключить?» возвращает назад, а не закрывает окно целиком.
dlg.addEventListener('keydown', e => {
  if (e.key === 'Escape' && view === 'off') { e.preventDefault(); e.stopPropagation(); back(); }
}, true);
dlg.addEventListener('cancel', e => { if (view === 'off') e.preventDefault(); });

// Щелчок мимо окна: отступ самого окна за «мимо» не считаем.
dlg.addEventListener('click', e => {
  if (e.target !== dlg) return;
  const r = dlg.getBoundingClientRect();
  if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) dlg.close();
});

dlg.addEventListener('close', () => {
  stopWait();
  linkUpdate = null;
  noteEl = null;
  gen++; // ответы, которые ещё в пути, окну уже не нужны
  view = '';
  // Строка на Обзоре могла перерисоваться, поэтому кнопку ищем заново.
  document.getElementById('tg-open')?.focus();
});
