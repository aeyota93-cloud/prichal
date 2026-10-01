import { $, h, icon, toast, confirmBox, pillRow } from './dom.js';
import { fmtBytes, fmtAgo, plural } from './format.js';
import { api } from './api.js';
import { identities, about } from './model.js';

// ---------- Образы ----------

export let imgState = { mode: 'idle', data: null };

export async function loadImages() {
  try {
    const data = await api('/api/images');
    imgState.data = data;
    renderImages(data);
  } catch (e) {
    $('#im-sum').textContent = `Не удалось получить список: ${e.message}`;
  }
}

function imgName(im) {
  return im.tags[0] || 'без имени';
}

function renderPrune(unused, buildCache) {
  if (imgState.mode === 'busy') return;
  const free = unused.reduce((s, i) => s + i.size, 0) + buildCache;
  const what = [];
  if (unused.length) what.push(`${unused.length} ${plural(unused.length, 'неиспользуемый образ', 'неиспользуемых образа', 'неиспользуемых образов')}`);
  if (buildCache) what.push('кэш сборки');
  const end = free
    ? [h('span', { class: 'text', text: what.join(' и ') }), h('button', { class: 'pbtn white sm', onclick: () => askPrune(unused, buildCache) }, icon('broom'), `Очистить ${fmtBytes(free)}`)]
    : h('span', { class: 'state plain', text: 'чистить нечего' });
  $('#prune').replaceChildren(pillRow('Очистка', end));
}

export function renderImages(data) {
  const { images, buildCache } = data;
  const total = images.reduce((s, i) => s + i.size, 0);
  const unused = images.filter(i => !i.usedBy.length);
  imgState.titled = identities.size > 0; // иначе вместо названий будут имена в Docker
  $('#im-sum').textContent = `${images.length} ${plural(images.length, 'образ', 'образа', 'образов')} · ${fmtBytes(total)}`;
  $('#n-images').textContent = images.length || '';
  renderPrune(unused, buildCache);

  $('#im-list').replaceChildren(...images.map(im => {
    const used = im.usedBy.length
      ? `нужен: ${im.usedBy.map(n => about({ name: n }).title).join(', ')}`
      : 'не используется';
    const shortId = im.id.replace('sha256:', '').slice(0, 12);
    const li = h('li', { class: 'li', 'data-tone': im.usedBy.length ? 'ok' : 'idle' },
      h('span', { class: 'sdot' }),
      h('div', {}, h('p', { class: 'li-name', title: [...im.tags, shortId].join(', '), text: imgName(im) }),
        h('p', { class: 'li-sub im-id', text: shortId }),
        h('p', { class: 'li-sub im-used-m', text: used })), // на узком экране вместо ID
      h('p', { class: 'li-v num', text: fmtBytes(im.size) }),
      h('p', { class: 'li-sub', text: fmtAgo(Date.now() / 1000 - im.created) }),
      h('p', { class: 'im-used' + (im.usedBy.length ? '' : ' is-free'), text: used }),
      im.usedBy.length ? h('span') : h('button', { class: 'textbtn', onclick: () => askRemove(li, im) }, icon('trash'), 'Удалить'));
    return li;
  }));
}

function askPrune(unused, buildCache) {
  const list = unused.map(i => `${imgName(i)} (${fmtBytes(i.size)})`);
  if (buildCache) list.push(`кэш сборки (${fmtBytes(buildCache)})`);
  const back = () => renderImages(imgState.data);
  $('#prune').append(confirmBox({
    q: 'Очистить неиспользуемое?',
    text: `Будет удалено: ${list.join(', ')}. Образы, которые нужны контейнерам, останутся, даже если контейнер сейчас остановлен.`,
    yes: 'Да, очистить', danger: true, onYes: doPrune, onNo: back,
  }));
}

async function doPrune() {
  imgState.mode = 'busy';
  $('#prune').replaceChildren(pillRow('Очистка', h('button', { class: 'pbtn white sm is-busy', disabled: true }, icon('arrows-clockwise'), 'Очищаю…')));
  try {
    const res = await api('/api/prune', { method: 'POST' });
    toast(`Готово, освобождено ${fmtBytes(res.reclaimed)}`);
  } catch (e) {
    toast(`Очистка не удалась: ${e.message}`, true);
  }
  imgState.mode = 'idle';
  loadImages();
}

function askRemove(li, im) {
  li.querySelector('.confirm')?.remove();
  const box = confirmBox({
    q: `Удалить образ ${imgName(im)}?`,
    text: 'Он не нужен ни одному контейнеру. Если понадобится снова, Docker скачает его заново.',
    yes: 'Да, удалить', danger: true, onYes: () => removeImage(im), onNo: () => box.remove(),
  });
  li.append(box);
}

async function removeImage(im) {
  try {
    await api(`/api/images/${encodeURIComponent(im.id)}/remove`, { method: 'POST' });
    toast(`Образ ${imgName(im)} удалён`);
  } catch (e) {
    toast(`Не получилось удалить ${imgName(im)}: ${e.message}`, true);
  }
  loadImages();
}
