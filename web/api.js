// Запросы к серверу панели.

export const POLL_MS = 3000;


export async function api(path, opts = {}) {
  const init = { method: opts.method || 'GET', headers: {} };
  if (init.method !== 'GET') init.headers['X-Prichal'] = '1';
  if (opts.body) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(opts.body);
  }
  const res = await fetch(path, init);
  if (res.status === 401 && path !== '/api/login') {
    location.href = '/login.html';
    throw new Error('нужно войти');
  }
  let data = null;
  try { data = await res.json(); } catch { /* пустой ответ */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `ошибка ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return data;
}
