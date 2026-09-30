#!/bin/sh
# Причал: SSH-туннель к панели (macOS и Linux).
#   ./prichal-tunnel.sh root@1.2.3.4            панель на порту 9443
#   ./prichal-tunnel.sh root@1.2.3.4 9443 5678  плюс другие порты сервера
set -eu
[ $# -ge 1 ] || { echo "Использование: $0 пользователь@сервер [порт] [другие порты...]"; exit 1; }
server=$1
port=${2:-9443}
[ $# -ge 2 ] && shift 2 || shift 1

fwd="-L $port:127.0.0.1:$port"
for p in "$@"; do fwd="$fwd -L $p:127.0.0.1:$p"; done

open_url() {
  if command -v open >/dev/null 2>&1; then open "$1"
  elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$1" >/dev/null 2>&1
  else echo "Откройте в браузере: $1"; fi
}

url="http://localhost:$port"
# Open the browser once the tunnel answers.
(
  i=0
  while [ $i -lt 300 ]; do
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null || nc -z 127.0.0.1 "$port" 2>/dev/null; then
      open_url "$url"
      exit 0
    fi
    i=$((i + 1))
    sleep 1
  done
) &

echo "Подключаюсь к $server. Пока работаете, не закрывайте это окно (Ctrl+C, чтобы выйти)."
# shellcheck disable=SC2086
exec ssh -N $fwd -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 "$server"
