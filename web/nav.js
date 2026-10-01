import { conns, loadConns } from './conns.js';
import { loadImages } from './images.js';
import { loadUpdates } from './updates.js';

// ---------- Разделы ----------

export const VIEWS = { overview: '#', containers: '#containers', conns: '#connections', updates: '#updates', images: '#images' };
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
  if (view === 'conns') loadConns();
  if (view === 'updates') loadUpdates();
  if (view === 'images') loadImages();
}
