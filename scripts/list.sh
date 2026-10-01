# shellcheck shell=sh
# Runs on the host and prints @@-sections that parseListing (updates.go)
# understands. It never touches the network: lists are only as fresh as the
# last "check".
pm=none
for p in apt-get dnf yum apk pacman zypper; do
  if command -v $p >/dev/null 2>&1; then pm=$p; break; fi
done
[ "$pm" = apt-get ] && pm=apt
echo "@@PM $pm"
if [ -d /run/systemd/system ]; then echo "@@INIT systemd"; else echo "@@INIT other"; fi
. /etc/os-release 2>/dev/null; echo "@@OS ${PRETTY_NAME:-Linux}"
echo "@@KERNEL $(uname -r)"
case $pm in
apt)
  echo "@@STAMP $(stat -c %Y /var/lib/apt/periodic/update-success-stamp 2>/dev/null || stat -c %Y /var/lib/apt/lists 2>/dev/null || echo 0)"
  if [ -f /run/reboot-required ]; then echo "@@REBOOT"; cat /run/reboot-required.pkgs 2>/dev/null; fi
  echo "@@UPGRADABLE"; apt list --upgradable 2>/dev/null
  echo "@@SUMMARY"; dpkg-query -W -f='${Package}\t${binary:Summary}\n'
  ;;
dnf|yum)
  echo "@@STAMP $(stat -c %Y /var/cache/dnf/last_makecache 2>/dev/null || stat -c %Y /var/cache/$pm 2>/dev/null || echo 0)"
  $pm needs-restarting -r >/dev/null 2>&1; rc=$?
  if [ $rc -eq 1 ]; then echo "@@REBOOT"
  elif [ $rc -ne 0 ]; then
    latest=$(rpm -q --last kernel-core kernel 2>/dev/null | grep -v 'not installed' | head -n1 | awk '{print $1}' | sed 's/^kernel-core-//; s/^kernel-//')
    [ -n "$latest" ] && [ "$latest" != "$(uname -r)" ] && echo "@@REBOOT"
  fi
  echo "@@UPGRADABLE"; $pm -q -C check-update 2>/dev/null
  echo "@@SECURITY"; $pm -q -C updateinfo list --security --available 2>/dev/null
  echo "@@INSTALLED"; rpm -qa --qf '%{NAME}.%{ARCH}\t%{VERSION}-%{RELEASE}\t%{SUMMARY}\n'
  ;;
apk)
  idx=$(ls -t /var/cache/apk/APKINDEX.* /lib/apk/db/installed 2>/dev/null | head -n1)
  echo "@@STAMP $(stat -c %Y "$idx" 2>/dev/null || echo 0)"
  echo "@@UPGRADABLE"; apk version -l '<' 2>/dev/null
  ;;
esac
