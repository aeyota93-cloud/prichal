const form = document.getElementById('form');
const input = document.getElementById('password');
const error = document.getElementById('error');

form.addEventListener('submit', async e => {
  e.preventDefault();
  error.textContent = '';
  const btn = form.querySelector('button');
  btn.disabled = true;
  try {
    const res = await fetch('/api/login', {
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
    input.select();
  } catch {
    error.textContent = 'Нет связи с сервером';
  } finally {
    btn.disabled = false;
  }
});
