import { conns, loadConns } from './conns.js';
import { loadImages } from './images.js';
import { loadUpdates } from './updates.js';
import { VIEWS } from './views.js';

// ---------- Разделы ----------

export let currentView = 'overview';

export const viewFromHash = () => Object.keys(VIEWS).find(v => VIEWS[v] === location.hash) || 'overview';

// push: смена раздела добавляет запись в историю, чтобы «Назад» шёл по разделам.
// При старте, по «Назад/Вперёд» и при автопереходе пишем поверх текущей записи.
export function go(view, { push = true } = {}) {
  if (!VIEWS[view]) view = 'overview';
  const changed = view !== currentView;
  currentView = view;
  for (const v of Object.keys(VIEWS)) document.getElementById(`view-${v}`).hidden = v !== view;
  document.querySelectorAll('.nav button').forEach(b => b.dataset.view === view ? b.setAttribute('aria-current', 'page') : b.removeAttribute('aria-current'));
  const url = VIEWS[view] === '#' ? location.pathname : VIEWS[view];
  if (push && changed) history.pushState(null, '', url);
  else history.replaceState(null, '', url);
  window.scrollTo(0, 0);
  // На узком экране меню и сегменты прокручиваются вбок: выбранное должно быть видно.
  const inView = { inline: 'nearest', block: 'nearest' };
  document.querySelector('.nav button[aria-current]')?.scrollIntoView(inView);
  document.querySelector(`#view-${view} > .seg [aria-pressed="true"]`)?.scrollIntoView(inView);
  if (view === 'conns') loadConns();
  if (view === 'updates') loadUpdates();
  if (view === 'images') loadImages();
}

// Затухание у края прокручиваемого ряда: подсказывает, что там есть ещё пункты.
// Классы is-scroll-start / is-scroll-end ставим по прокрутке, размеру и составу ряда.
export function fadeEdges(el) {
  const update = () => {
    const max = el.scrollWidth - el.clientWidth;
    el.classList.toggle('is-scroll-start', el.scrollLeft > 1);
    el.classList.toggle('is-scroll-end', el.scrollLeft < max - 1);
  };
  el.addEventListener('scroll', update, { passive: true });
  window.addEventListener('resize', update);
  new ResizeObserver(update).observe(el);
  new MutationObserver(update).observe(el, { childList: true, subtree: true, characterData: true });
  document.fonts?.ready.then(update);
  update();
}
