import { ICONS } from './icons.js';

// Мелкие помощники для DOM, всплывающие сообщения и подтверждения.

export const $ = (s, root = document) => root.querySelector(s);

export function h(tag, attrs = {}, ...kids) {
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

export function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 256 256');
  svg.setAttribute('fill', 'currentColor');
  svg.setAttribute('aria-hidden', 'true');
  svg.classList.add('ico');
  svg.innerHTML = ICONS[name] || '';
  return svg;
}

// ---------- Всплывающие сообщения и подтверждения ----------

// Обычные сообщения гаснут сами. Ошибки висят, пока их не закроют (кнопкой
// или Esc, когда фокус внутри); одновременно не больше трёх, старые уходят.
const MAX_ERRORS = 3;

export function toast(text, bad = false) {
  const box = $('#toasts');
  if (!bad) {
    const el = h('div', { class: 'toast' }, icon('check-circle'), h('p', { text }));
    box.append(el);
    setTimeout(() => el.remove(), 4500);
    return;
  }
  const close = () => el.remove();
  const el = h('div', { class: 'toast is-bad', role: 'alert', onkeydown: e => { if (e.key === 'Escape') close(); } },
    icon('warning-circle'), h('p', { text }),
    h('button', { class: 'toast-x', 'aria-label': 'Закрыть', title: 'Закрыть', onclick: close }, icon('x')));
  box.append(el);
  const errs = box.querySelectorAll('.toast.is-bad');
  for (let i = 0; i < errs.length - MAX_ERRORS; i++) errs[i].remove();
}

export function confirmBox({ q, text, yes, danger, onYes, onNo }) {
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
export function pillRow(lead, end, tone) {
  return h('div', { class: 'pill-row glass', 'data-tone': tone || null },
    h('span', { class: 'lead', text: lead }), dots(6), h('div', { class: 'end' }, end));
}
