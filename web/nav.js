import { conns, loadConns } from './conns.js';
import { loadImages } from './images.js';
import { loadUpdates } from './updates.js';

// ---------- Разделы ----------

export const VIEWS = { overview: '#', containers: '#containers', conns: '#connections', updates: '#updates', images: '#images' };
export let currentView = 'overview';

export function go(view) {
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
