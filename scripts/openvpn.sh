# shellcheck shell=sh
# Runs inside the Amnezia OpenVPN container. The status file lives in
# openvpn's working directory.
cd "/proc/$(pgrep -x openvpn | head -n1)/cwd" 2>/dev/null && cat openvpn-status.log; echo @@@; cat /opt/amnezia/openvpn/clientsTable 2>/dev/null
