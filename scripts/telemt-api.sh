# shellcheck shell=sh
# Runs inside the Amnezia telemt container: one call to telemt's control API.
# $1 method, $2 path (/v1/...), $3 optional JSON body. Arguments come from the
# panel as separate exec arguments, never through a shell string. telemt
# answers only on 127.0.0.1 there; the port comes from its own config.
p=$(sed -n '/^\[server\.api\]/,/^\[\[/p' /data/config.toml 2>/dev/null | sed -n 's/^listen *= *"[^"]*:\([0-9][0-9]*\)".*/\1/p' | head -n1)
url="http://127.0.0.1:${p:-9091}$2"
if [ -n "${3:-}" ]; then
	curl -s -m 8 -X "$1" -H 'Content-Type: application/json' --data "$3" "$url"
else
	curl -s -m 8 -X "$1" "$url"
fi
