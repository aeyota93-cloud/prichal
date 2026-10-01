// Числа, размеры, время и слова.
const nf1 = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 });
export const nf0 = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 0 });

export function fmtBytes(b) {
  if (!b) return '0 Б';
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (b >= 1024 && i < units.length - 1) { b /= 1024; i++; }
  return `${(b < 10 && i > 0 ? nf1 : nf0).format(b)} ${units[i]}`;
}

export function fmtPct(p) {
  if (p == null) return '';
  return `${(p < 10 ? nf1 : nf0).format(p)} %`;
}

export function fmtDur(sec) {
  sec = Math.max(0, Math.floor(sec));
  if (sec < 60) return `${sec} с`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} мин`;
  const hrs = Math.floor(m / 60);
  if (hrs < 10) return m % 60 ? `${hrs} ч ${m % 60} мин` : `${hrs} ч`;
  if (hrs < 24) return `${hrs} ч`;
  const d = Math.floor(hrs / 24);
  if (d < 3 && hrs % 24) return `${d} дн ${hrs % 24} ч`;
  return `${d} дн`;
}

export function fmtAgo(sec) {
  if (sec < 60) return 'только что';
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} мин назад`;
  const hrs = Math.floor(m / 60);
  if (hrs < 24) return `${hrs} ч назад`;
  const d = Math.floor(hrs / 24);
  if (d < 14) return `${d} дн назад`;
  if (d < 60) return `${Math.floor(d / 7)} нед назад`;
  if (d < 730) return `${Math.floor(d / 30)} мес назад`;
  return `${Math.floor(d / 365)} г назад`;
}

export const dateFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
const hmFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
export const timeFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
export const fullFmt = new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'medium' });

// «09:56», «вчера, 23:10» или «28 сент., 12:00».
export function fmtWhen(ms) {
  const d = new Date(ms), now = new Date();
  const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(now) - day(d)) / 86400000);
  if (diff === 0) return hmFmt.format(d);
  if (diff === 1) return `вчера, ${hmFmt.format(d)}`;
  return dateFmt.format(d);
}

export function parseTime(s) {
  if (!s || s.startsWith('0001-')) return null;
  const t = Date.parse(s);
  return Number.isNaN(t) ? null : t;
}

export function plural(n, one, few, many) {
  const a = n % 10, b = n % 100;
  if (a === 1 && b !== 11) return one;
  if (a >= 2 && a <= 4 && (b < 12 || b > 14)) return few;
  return many;
}

export const cap = s => s ? s[0].toUpperCase() + s.slice(1) : s;

// «5:29.8.1-1~ubuntu.24.04~noble» -> «29.8.1-1»: эпоха и хвост дистрибутива
// только мешают читать. Полная версия остаётся в подсказке.
export const shortVer = v => v.replace(/^\d+:/, '').replace(/[~+].*$/, '');
