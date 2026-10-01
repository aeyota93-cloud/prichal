import { $, icon } from './dom.js';
import { api } from './api.js';
import { renderTrack } from './overview.js';
import { conns, loadConns } from './conns.js';
import { loadImages } from './images.js';
import { loadUpdates } from './updates.js';
import { refresh, schedule } from './poll.js';
import { VIEWS, go } from './nav.js';

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
