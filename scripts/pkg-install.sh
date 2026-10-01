# shellcheck shell=sh
# Task "install": upgrades the given packages. Arguments: package manager,
# then package names (checked by the panel against the list of upgrades).
pm=$1; shift
echo "== Устанавливаю: $*"
case $pm in
  apt) apt-get -y -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold \
         -o APT::Get::Always-Include-Phased-Updates=true install --only-upgrade "$@" ;;
  dnf|yum) $pm -y upgrade "$@" ;;
  apk) apk add --upgrade "$@" ;;
  *) echo "!! $pm не поддерживается"; exit 2 ;;
esac
echo "== Готово"
if [ -f /run/reboot-required ]; then echo "== Для части обновлений нужна перезагрузка сервера"; fi
