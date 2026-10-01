# shellcheck shell=sh
# Runs inside the Amnezia telemt container. telemt answers only on 127.0.0.1
# there; the port comes from its own config.
p=$(sed -n '/^\[server\.api\]/,/^\[\[/p' /data/config.toml 2>/dev/null | sed -n 's/^listen *= *"[^"]*:\([0-9][0-9]*\)".*/\1/p' | head -n1)
curl -s -m 3 "http://127.0.0.1:${p:-9091}/v1/stats/users"
