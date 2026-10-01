#!/bin/sh
# Установка и обновление Причала. Запускать от root в папке репозитория:
#   ./install.sh
# Повторный запуск после `git pull` обновляет панель, настройки в .env сохраняются.
set -eu
cd "$(dirname "$0")"

say() { printf '%s\n' "$*"; }
fail() { say "!! $*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || fail "Запустите от root: sudo ./install.sh"
command -v docker >/dev/null 2>&1 || fail "Нужен Docker: https://docs.docker.com/engine/install/"
docker compose version >/dev/null 2>&1 || fail "Нужен Docker Compose v2 (плагин docker compose)."
docker info >/dev/null 2>&1 || fail "Docker не отвечает. Он запущен?"

# set_env KEY VALUE: записать значение в .env, ничего не экранируя руками.
set_env() {
  tmp=$(mktemp)
  awk -v k="$1" -v v="$2" 'BEGIN { done = 0 }
    $0 ~ "^" k "=" { print k "=" v; done = 1; next } { print }
    END { if (!done) print k "=" v }' .env > "$tmp"
  cat "$tmp" > .env
  rm -f "$tmp"
}

if [ ! -f .env ]; then
  cp .env.example .env
  chmod 600 .env
  say "Создан файл настроек .env."
  if [ -t 0 ]; then
    printf 'Токен Telegram-бота для уведомлений (Enter, чтобы пропустить): '
    read -r tok || tok=""
    if [ -n "$tok" ]; then
      printf 'Ваш @username в Telegram: '
      read -r user || user=""
      set_env TG_TOKEN "$tok"
      set_env TG_USERNAME "${user#@}"
      say "Откройте своего бота в Telegram и нажмите Start: он начнёт писать вам."
    fi
  fi
fi
chmod 600 .env

# Go needs roughly 500 MB of memory to build the panel.
avail=$(awk '/^MemAvailable:/ {m=$2} /^SwapFree:/ {s=$2} END {print int((m+s)/1024)}' /proc/meminfo 2>/dev/null || echo 0)
if [ "${avail:-0}" -gt 0 ] && [ "$avail" -lt 600 ]; then
  say "Внимание: свободно всего $avail МБ памяти вместе с подкачкой, сборка может не пройти."
  say "Если она оборвётся, добавьте подкачку (swap) на 1-2 ГБ и запустите ./install.sh снова."
fi

say "== Собираю и запускаю Причал (первый раз это займёт пару минут)"
docker compose up -d --build
# Keep the disk tidy: the Go toolchain is only needed while building.
docker image prune -f >/dev/null 2>&1 || true
docker builder prune -f >/dev/null 2>&1 || true

port=$(sed -n 's/^PRICHAL_PORT=\([0-9][0-9]*\).*/\1/p' .env | head -n1)
port=${port:-9443}
ok=0
i=0
while [ $i -lt 30 ]; do
  if wget -qO- "http://127.0.0.1:$port/api/health" 2>/dev/null | grep -q ok || curl -fsS "http://127.0.0.1:$port/api/health" 2>/dev/null | grep -q ok; then
    ok=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ "$ok" = 1 ] || { docker logs --tail 20 prichal; fail "Причал не ответил. Журнал выше."; }

addr=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "$addr" ] || addr=$(ip route get 1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')
say ""
say "=== Причал работает на 127.0.0.1:$port ==="
say "Откройте на своём компьютере SSH-туннель:"
say "  ssh -N -L $port:127.0.0.1:$port root@${addr:-АДРЕС-СЕРВЕРА}"
say "и зайдите в браузере на http://localhost:$port"
if ! grep -q '^PRICHAL_PASSWORD=..*' .env; then
  say "При первом входе панель попросит придумать пароль."
fi
say "Готовые ярлыки для Windows, macOS и Linux лежат в папке clients/."
