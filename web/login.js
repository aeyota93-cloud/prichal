const form = document.getElementById('form');
const input = document.getElementById('password');
const input2 = document.getElementById('password2');
const error = document.getElementById('error');

// Пароля ещё нет: та же форма, но придумать новый (два поля).
let setup = false;

function setupMode() {
  setup = true;
  document.title = 'Причал · пароль';
  document.getElementById('title').textContent = 'Придумайте пароль';
  document.getElementById('lead').hidden = false;
  document.getElementById('pw-label').textContent = 'Новый пароль';
  document.getElementById('pw2-label').hidden = false;
  input2.hidden = false;
  input.autocomplete = 'new-password';
  document.getElementById('submit').textContent = 'Сохранить и войти';
  document.getElementById('hint').hidden = true;
}

fetch('/api/session').then(r => r.json()).then(s => {
  if (s.valid) location.href = '/';
  else if (s.setup) setupMode();
}).catch(() => {});

form.addEventListener('submit', async e => {
  e.preventDefault();
  error.textContent = '';
  if (setup) {
    if ([...input.value].length < 8) { error.textContent = 'Нужно не меньше 8 символов'; input.focus(); return; }
    if (input.value !== input2.value) { error.textContent = 'Пароли не совпадают'; input2.select(); return; }
  }
  const btn = form.querySelector('button');
  btn.disabled = true;
  try {
    const res = await fetch(setup ? '/api/setup' : '/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Prichal': '1' },
      body: JSON.stringify({ password: input.value }),
    });
    if (res.ok) {
      location.href = '/';
      return;
    }
    const data = await res.json().catch(() => ({}));
    error.textContent = data.error || `Ошибка ${res.status}`;
    if (res.status === 409) setTimeout(() => location.reload(), 1500); // пароль уже задан или ещё не задан
    input.select();
  } catch {
    error.textContent = 'Нет связи с сервером';
  } finally {
    btn.disabled = false;
  }
});
