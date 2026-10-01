import { $, icon } from './dom.js';
import { api } from './api.js';
import { renderTrack } from './overview.js';
import { conns, loadConns } from './conns.js';
import { loadImages } from './images.js';
import { loadUpdates } from './updates.js';
import { refresh, schedule } from './poll.js';
import { go, viewFromHash, currentView } from './nav.js';

document.querySelectorAll('[data-view]').forEach(b => b.addEventListener('click', () => go(b.dataset.view)));

// ---------- Старт ----------

document.querySelectorAll('[data-icon]').forEach(el => el.replaceChildren(icon(el.dataset.icon)));
renderTrack('m-mem');
renderTrack('m-disk');
renderTrack('m-cpu');
const startView = viewFromHash();
go(startView, { push: false });
// «Назад/Вперёд» браузера. При ручной правке адреса приходят оба события:
// hashchange пропускаем, если popstate уже открыл этот раздел.
window.addEventListener('popstate', () => go(viewFromHash(), { push: false }));
window.addEventListener('hashchange', () => { if (viewFromHash() !== currentView) go(viewFromHash(), { push: false }); });
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
