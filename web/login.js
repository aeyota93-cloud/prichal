const form = document.getElementById('form');
const input = document.getElementById('password');
const input2 = document.getElementById('password2');
const error = document.getElementById('error');

// Куда идти после входа: раздел из ?next=, но только из белого списка (как VIEWS в views.js).
// Любое другое значение (адрес, //, javascript:) игнорируется.
const NEXT_OK = ['#containers', '#connections', '#updates', '#images'];
// Если сервер сам перекинул на вход с адреса вида /#updates, раздел остался в #хэше.
const next = [new URLSearchParams(location.search).get('next'), location.hash].find(v => NEXT_OK.includes(v));
const home = next ? '/' + next : '/';

// Ошибка у поля: текст, красная рамка и связь для чтения с экрана.
function fail(msg, ...fields) {
  error.textContent = msg;
  for (const f of fields) {
    f.setAttribute('aria-invalid', 'true');
    f.setAttribute('aria-describedby', 'error');
  }
}

function clearFail() {
  error.textContent = '';
  for (const f of [input, input2]) {
    f.removeAttribute('aria-invalid');
    f.removeAttribute('aria-describedby');
  }
}

// Глазик: показать или скрыть пароль в своём поле.
document.querySelectorAll('.pw-eye').forEach(btn => btn.addEventListener('click', () => {
  const field = document.getElementById(btn.dataset.for);
  const show = field.type === 'password';
  field.type = show ? 'text' : 'password';
  btn.setAttribute('aria-pressed', String(show));
  const label = show ? 'Скрыть пароль' : 'Показать пароль';
  btn.setAttribute('aria-label', label);
  btn.title = label;
  btn.querySelector('.eye-on').hidden = show;
  btn.querySelector('.eye-off').hidden = !show;
}));

// Пароля ещё нет: та же форма, но придумать новый (два поля).
let setup = false;

function setupMode() {
  setup = true;
  document.title = 'Причал · пароль';
  document.getElementById('title').textContent = 'Придумайте пароль';
  document.getElementById('lead').hidden = false;
  document.getElementById('pw-label').textContent = 'Новый пароль';
  document.getElementById('pw2-label').hidden = false;
  document.getElementById('pw2-box').hidden = false;
  input.autocomplete = 'new-password';
  document.getElementById('submit').textContent = 'Сохранить и войти';
  document.getElementById('hint').hidden = true;
}

fetch('/api/session').then(r => r.json()).then(s => {
  if (s.valid) location.href = home;
  else if (s.setup) setupMode();
}).catch(() => {});

form.addEventListener('submit', async e => {
  e.preventDefault();
  clearFail();
  if (setup) {
    if ([...input.value].length < 8) { fail('Нужно не меньше 8 символов', input); input.focus(); return; }
    if (input.value !== input2.value) { fail('Пароли не совпадают', input2); input2.select(); return; }
  }
  const btn = document.getElementById('submit');
  btn.disabled = true;
  try {
    const res = await fetch(setup ? '/api/setup' : '/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Prichal': '1' },
      body: JSON.stringify({ password: input.value }),
    });
    if (res.ok) {
      location.href = home;
      return;
    }
    const data = await res.json().catch(() => ({}));
    fail(data.error || `Ошибка ${res.status}`, input);
    if (res.status === 409) setTimeout(() => location.reload(), 1500); // пароль уже задан или ещё не задан
    input.select();
  } catch {
    fail('Нет связи с сервером');
  } finally {
    btn.disabled = false;
  }
});

// Начали печатать заново: красная рамка больше не нужна.
for (const f of [input, input2]) f.addEventListener('input', () => { f.removeAttribute('aria-invalid'); f.removeAttribute('aria-describedby'); });
