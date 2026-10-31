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

if [ ! -f .env ]; then
  cp .env.example .env
  chmod 600 .env
  say "Создан файл настроек .env."
fi
chmod 600 .env

# Go needs roughly 500 MB of memory to build the panel.
avail=$(awk '/^MemAvailable:/ {m=$2} /^SwapFree:/ {s=$2} END {print int((m+s)/1024)}' /proc/meminfo 2>/dev/null || echo 0)
if [ "${avail:-0}" -gt 0 ] && [ "$avail" -lt 600 ]; then
  say "Внимание: свободно всего $avail МБ памяти вместе с подкачкой, сборка может не пройти."
  say "Если она оборвётся, добавьте подкачку (swap) на 1-2 ГБ и запустите ./install.sh снова."
fi

# ---- Прокси Telegram (по желанию, PROXY=1 в .env) ----
envget() { sed -n "s/^$1=//p" .env | tail -n1; }
envset() {
  if grep -q "^$1=" .env; then
    sed -i "s|^$1=.*|$1=$2|" .env
  else
    printf '%s=%s\n' "$1" "$2" >>.env
  fi
}

# proxy_config prints proxy-data/config.toml. Links and secrets live in the
# [access*] sections: taken from PROXY_IMPORT (telemt keeps them at the end of
# its file) or a new link «main». Everything else comes from PROXY_* in .env.
proxy_config() {
  token=$(envget PROXY_API_TOKEN)
  subnet=$(envget PROXY_API_SUBNET)
  pport=$(envget PROXY_PUBLIC_PORT)
  [ -n "$pport" ] || pport=$(envget PROXY_PORT)
  phost=$(envget PROXY_PUBLIC_HOST)
  domain=$(envget PROXY_TLS_DOMAIN)
  import=$(envget PROXY_IMPORT)
  access=
  if [ -n "$import" ]; then
    old=$(docker exec "$import" cat /data/config.toml) || fail "Не удалось прочитать настройки прокси $import."
    [ -n "$domain" ] || domain=$(printf '%s\n' "$old" | sed -n 's/^tls_domain *= *"\([^"]*\)".*/\1/p' | head -n1)
    access=$(printf '%s\n' "$old" | sed -n '/^\[access/,$p')
    printf '%s\n' "$access" | grep -q '^\[access\.users\]' || fail "В настройках $import нет ссылок ([access.users])."
  fi
  [ -n "$domain" ] || fail "Задайте PROXY_TLS_DOMAIN в .env: сайт-маскировка, например, ya.ru."
  [ -n "$access" ] || access=$(printf '[access.users]\nmain = "%s"' "$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')")
  hostline=
  [ -z "$phost" ] || hostline="public_host = \"$phost\""
  cat <<EOF
### Причал: прокси Telegram (telemt). Ссылки и секреты меняются из панели (API);
### остальное — PROXY_* в .env. Файл создаёт ./install.sh один раз и потом не трогает.
[general]
use_middle_proxy = true
log_level = "normal"
data_path = "/data"

[general.modes]
classic = false
secure = false
tls = true

[general.links]
show = "*"
public_port = ${pport:-443}
$hostline

[server]
port = 8443

[server.api]
enabled = true
listen = "0.0.0.0:9091"
whitelist = ["${subnet:-172.30.207.0/28}"]
auth_header = "Bearer $token"

[[server.listeners]]
ip = "0.0.0.0"

[censorship]
tls_domain = "$domain"
mask = true
tls_emulation = false
tls_front_dir = "/data/tlsfront"

$access
EOF
}

if [ "$(envget PROXY)" = 1 ]; then
  say "== Прокси Telegram"
  [ -n "$(envget PROXY_API_TOKEN)" ] || envset PROXY_API_TOKEN "$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
  [ -n "$(envget PROXY_API_SUBNET)" ] || envset PROXY_API_SUBNET 172.30.207.0/28
  # Compose reads COMPOSE_PROFILES from .env: every later `docker compose up`,
  # the panel's own updates included, keeps the proxy.
  envset COMPOSE_PROFILES proxy
  mkdir -p proxy-data
  if [ ! -f proxy-data/config.toml ]; then
    proxy_config >proxy-data/config.toml.new
    mv proxy-data/config.toml.new proxy-data/config.toml
    say "Создан proxy-data/config.toml."
  fi
  chown -R 65532:65532 proxy-data
  chmod 700 proxy-data
  chmod 600 proxy-data/config.toml
fi

say "== Собираю и запускаю Причал (первый раз это займёт пару минут)"
docker compose up -d --build
# Keep the disk tidy. Only dangling images: the build cache of the whole
# daemon may belong to other projects (the Images tab can clear it).
docker image prune -f >/dev/null 2>&1 || true

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
say "Уведомления в Telegram подключаются в самой панели: Обзор → Уведомления → Подключить."
say "Готовые ярлыки для Windows, macOS и Linux лежат в папке clients/."
