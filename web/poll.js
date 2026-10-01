import { $ } from './dom.js';
import { plural } from './format.js';
import { POLL_MS, api } from './api.js';
import { renderHost, renderOverview } from './overview.js';
import { rows, loadLogs, renderContainers } from './containers.js';
import { loadConns } from './conns.js';
import { imgState, renderImages } from './images.js';
import { upd } from './updates.js';

// ---------- Опрос ----------

export let lastOverview = null;

let pollTimer = null;
let inflight = null;

function setOffline(off) {
  $('#offline').hidden = !off;
  document.body.classList.toggle('is-offline', off);
  // Значок у названия сервера: это связь панели с сервером, не клиенты VPN.
  $('#ov-live-t').textContent = off ? 'нет связи' : 'в сети';
  $('#ov-live').dataset.tone = off ? 'idle' : ''; // при связи тон вернёт renderOverview
}

export async function refresh() {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const ov = await api('/api/overview');
      lastOverview = ov;
      setOffline(false);
      if (ov.label) {
        $('#ov-label').textContent = ov.label;
        document.title = `Причал · ${ov.label}`;
      }
      renderHost(ov.host);
      renderContainers(ov.containers);
      renderOverview(ov);
      if (imgState.data && !imgState.titled && imgState.mode !== 'busy') renderImages(imgState.data);
      renderHostLine();
    } catch (e) {
      setOffline(true);
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

export function renderHostLine() {
  const hv = lastOverview?.host;
  const os = upd.view?.system?.os;
  const parts = [lastOverview?.label, os?.replace(/ LTS$/, ''), hv && `${hv.cpus} ${plural(hv.cpus, 'ядро', 'ядра', 'ядер')}`].filter(Boolean);
  $('#host-line').textContent = parts.join(' · ');
}

export function schedule() {
  clearTimeout(pollTimer);
  if (document.visibilityState !== 'visible') return;
  pollTimer = setTimeout(async () => { await refresh(); schedule(); }, POLL_MS);
}

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') {
    refresh().then(schedule);
    loadConns();
    for (const r of rows.values()) if (r.open && r.follow) loadLogs(r);
  }
});
