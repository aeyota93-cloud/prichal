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

export function toast(text, bad = false) {
  const el = h('div', { class: 'toast' + (bad ? ' is-bad' : '') }, icon(bad ? 'warning-circle' : 'check-circle'), h('p', { text }));
  $('#toasts').append(el);
  setTimeout(() => el.remove(), bad ? 9000 : 4500);
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
