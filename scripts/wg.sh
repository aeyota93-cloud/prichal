# shellcheck shell=sh
# Runs inside an Amnezia WireGuard container. Finds its own config, so
# different Amnezia versions (awg, awg2, plain WireGuard) work without knowing
# interface names in advance. Prints public keys only.
f=""
for c in /opt/amnezia/*/*.conf; do grep -qs '^\[Interface\]' "$c" && { f=$c; break; }; done
[ -n "$f" ] || { echo "в контейнере нет настроек WireGuard" >&2; exit 3; }
d=${f%/*}; i=${f##*/}; i=${i%.conf}
t=wg; command -v awg >/dev/null 2>&1 && t=awg
$t show "$i" latest-handshakes; echo @@@; $t show "$i" transfer; echo @@@; cat "$d/clientsTable" 2>/dev/null
