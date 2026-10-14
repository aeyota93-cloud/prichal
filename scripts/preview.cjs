// Local UI preview with synthetic data. No Docker, SSH or production API calls.
// Run: node scripts/preview.cjs, open http://127.0.0.1:19444
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../web');
const now = Math.floor(Date.now() / 1000);
const mb = 1024 * 1024;
const containers = [
  ['vpn-panel', 'VPN-панель', 'Управление подключениями', 'app', 186],
  ['telemt', 'Telegram', 'Прокси для Telegram', 'proxy', 42],
  ['n8n', 'n8n', 'Автоматизация', 'app', 220],
  ['prichal', 'Причал', 'Панель сервера', 'self', 8],
].map(([name, title, sub, kind, mem], i) => ({
  id: `preview-${i}`, name, title, sub, kind, image: `${name}:latest`, project: name,
  state: 'running', health: '', startedAt: new Date((now - 86400 * 12) * 1000).toISOString(),
  restartPolicy: 'always', ports: [], cpu: i + 0.4, mem: mem * mb, memLimit: 2048 * mb,
  restartCount: 0, self: kind === 'self', history: Array.from({ length: 20 }, (_, j) => ({ t: now - (20 - j) * 3, cpu: (j % 5) + i, mem: mem * mb })),
}));
const overview = {
  label: 'Мой сервер', zone: 'МСК', updated: now,
  host: { cpu: 12, cpus: 2, memUsed: 840 * mb, memTotal: 2048 * mb, swapTotal: 0, diskUsed: 9200 * mb, diskTotal: 40960 * mb, uptime: 86400 * 12 + 7200 },
  containers, telegram: { state: 'off' },
  events: [{ t: now - 600, text: 'Проверка обновлений завершена', tone: 'ok' }, { t: now - 7200, text: 'n8n: обновление установлено', tone: 'ok' }, { t: now - 86400, text: 'Telegram: связь восстановлена', tone: 'ok' }],
};
const connections = { services: [{ container: 'telemt', title: 'Telegram', kind: 'telemt', state: 'running', clients: [{ name: 'amnezia', label: 'Основная ссылка', devices: 3, online: true, conns: 6, total: 32 * mb, ips: [] }] }] };
const images = { images: containers.map(c => ({ id: c.id, tags: [c.image], size: 80 * mb, created: now - 86400, usedBy: [c.name] })).concat([{ id: 'unused', tags: ['n8n:previous'], size: 120 * mb, created: now - 86400 * 30, usedBy: [] }]), buildCache: 36 * mb };
const updates = { system: { supported: true, os: 'Ubuntu 24.04 LTS', pm: 'apt', init: 'systemd', listsAt: now - 600, rebootRequired: false, packages: [{ name: 'openssl', from: '3.0.13', to: '3.0.14', group: 'security', security: true }, { name: 'curl', from: '8.5.0', to: '8.5.1', group: 'other' }] }, apps: { checkedAt: now - 600, apps: [] } };
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  if (req.method !== 'GET') {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    return res.end(JSON.stringify({ error: 'Предпросмотр: действия с сервером отключены' }));
  }
  if (url.pathname === '/mobile') {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    return res.end('<!doctype html><html lang="ru"><title>Причал — мобильный предпросмотр</title><body style="margin:0;background:#141311;display:grid;place-items:center;min-height:100vh"><iframe title="Причал на телефоне" src="/" style="width:390px;height:844px;border:1px solid #4a4642;border-radius:24px"></iframe></body></html>');
  }
  if (url.pathname.startsWith('/api/')) {
    const data = { '/api/overview': overview, '/api/session': { auth: true, valid: false, setup: false }, '/api/connections': connections, '/api/images': images, '/api/updates': updates, '/api/telegram': { state: 'off' } }[url.pathname];
    res.writeHead(data || url.pathname.endsWith('/logs') ? 200 : 404, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
    return res.end(JSON.stringify(data || (url.pathname.endsWith('/logs') ? { lines: [{ t: now, text: 'Демонстрационный журнал: сервис работает' }] } : { error: 'Нет данных в предпросмотре' })));
  }
  const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
  if (!file.startsWith(root + path.sep)) { res.writeHead(403); return res.end(); }
  fs.readFile(file, (error, body) => {
    if (error) { res.writeHead(404); return res.end(); }
    const mime = { '.html': 'text/html; charset=utf-8', '.css': 'text/css', '.js': 'text/javascript', '.svg': 'image/svg+xml', '.woff2': 'font/woff2' }[path.extname(file)];
    res.writeHead(200, { 'Content-Type': mime || 'application/octet-stream', 'Cache-Control': 'no-store' });
    res.end(body);
  });
});
server.listen(19444, '127.0.0.1', () => console.log('Local preview: http://127.0.0.1:19444 (synthetic data, mutations disabled)'));
