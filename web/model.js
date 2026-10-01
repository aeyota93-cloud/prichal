import { icon } from './dom.js';

// ---------- Что за контейнер ----------

// Названия приходят с сервера: он узнаёт контейнеры по имени и образу.
export const identities = new Map(); // имя контейнера -> { title, sub, kind }

export function about(c) {
  if (c.title) return { title: c.title, sub: c.sub || '', kind: c.kind || 'other' };
  return identities.get(c.name) || { title: c.name, sub: '', kind: 'other' };
}

// Остановлен вручную или упал? Упавший контейнер с политикой «всегда» Docker
// поднял бы сам, так что если такой стоит, его выключили руками.
export function crashed(c) {
  if (c.state !== 'exited' || c.manualStop) return false;
  if (c.restartPolicy === 'always' || c.restartPolicy === 'unless-stopped') return false;
  return ![0, 137, 143].includes(c.exitCode);
}

export function statusOf(c) {
  switch (c.state) {
    case 'running':
      if (c.health === 'unhealthy') return { tone: 'bad', label: 'Нездоров' };
      if (c.health === 'starting') return { tone: 'warn', label: 'Запускается' };
      return { tone: 'ok', label: 'Работает' };
    case 'paused': return { tone: 'warn', label: 'На паузе' };
    case 'restarting': return { tone: 'bad', label: 'Перезапускается' };
    case 'exited':
      if (c.oomKilled) return { tone: 'bad', label: 'Не хватило памяти' };
      if (crashed(c)) return { tone: 'bad', label: 'Упал' };
      return { tone: 'idle', label: 'Остановлен' };
    case 'created': return { tone: 'idle', label: 'Не запускался' };
    case 'dead': return { tone: 'bad', label: 'Сломан' };
    default: return { tone: 'idle', label: c.state };
  }
}

export const ACTIONS = {
  start:   { label: 'Запустить', icon: 'play', busy: 'Запускаю…', done: 'запущен', verb: 'запустить' },
  unpause: { label: 'Продолжить', icon: 'play', busy: 'Снимаю с паузы…', done: 'снова работает', verb: 'снять с паузы' },
  restart: { label: 'Перезапустить', icon: 'arrow-clockwise', busy: 'Перезапускаю…', done: 'перезапущен', verb: 'перезапустить', confirm: 'Да, перезапустить' },
  pause:   { label: 'Пауза', icon: 'pause', busy: 'Ставлю на паузу…', done: 'на паузе', verb: 'поставить на паузу', confirm: 'Да, на паузу' },
  stop:    { label: 'Остановить', icon: 'stop', busy: 'Останавливаю…', done: 'остановлен', verb: 'остановить', confirm: 'Да, остановить', danger: true },
  kill:    { label: 'Остановить принудительно', icon: 'lightning', busy: 'Останавливаю…', done: 'остановлен принудительно', verb: 'принудительно остановить', confirm: 'Да, остановить сразу', danger: true },
};

// main идут в первом ряду, more прячутся за кнопкой «Ещё» (редкие и опасные).
export function availableActions(c) {
  if (c.self) return { main: ['restart'], more: [] };
  switch (c.state) {
    case 'running': return { main: ['restart', 'stop'], more: ['pause', 'kill'] };
    case 'paused': return { main: ['unpause', 'stop'], more: ['kill'] };
    case 'restarting': return { main: ['stop'], more: ['kill'] };
    default: return { main: ['start'], more: [] };
  }
}

export function confirmText(c, act) {
  const a = about(c);
  const base = {
    restart: 'Контейнер остановится и сразу запустится снова. Обычно это занимает несколько секунд.',
    pause: 'Всё внутри замрёт, но останется в памяти. Кнопка «Продолжить» вернёт как было.',
    stop: 'Контейнер штатно завершит работу. Запустить его снова можно здесь же.',
    kill: 'Контейнер выключится мгновенно, без штатного завершения. Это нужно, только если обычная остановка не помогает: несохранённые данные могут потеряться.',
  }[act];
  const down = act !== 'restart';
  const impact = {
    vpn: down
      ? `Пока он не работает, VPN через ${a.title} не будет работать ни у кого. Если ваш компьютер сейчас подключён через этот VPN, связь может пропасть.`
      : `Все, кто подключён через ${a.title}, на несколько секунд потеряют связь.`,
    proxy: down ? 'Прокси для Telegram перестанет работать у всех, кто им пользуется.' : 'Прокси для Telegram пропадёт на несколько секунд.',
    app: down ? `Пока ${a.title} не работает, им нельзя пользоваться.` : `${a.title} пропадёт на несколько секунд.`,
    self: 'Панель пропадёт на пару секунд и вернётся сама.',
  }[a.kind] || '';
  const after = (act === 'stop' || act === 'kill') && c.restartPolicy === 'always'
    ? 'После перезагрузки сервера он запустится снова сам.' : '';
  // Остановили VPN или прокси, через которые сидите, и связь пропала: вернуть можно только по SSH.
  // Контейнер на паузе возвращается командой unpause, start для него не работает.
  const ssh = ['vpn', 'proxy'].includes(a.kind) && ['stop', 'kill', 'pause'].includes(act)
    ? `Если связь пропадёт, запустите его снова по SSH: «docker ${act === 'pause' ? 'unpause' : 'start'} ${c.name}».` : '';
  return [base, impact, after, ssh].filter(Boolean).join(' ');
}

export const isNet = c => ['vpn', 'proxy'].includes(about(c).kind);
