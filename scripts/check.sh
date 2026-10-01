# shellcheck shell=sh
# Task "check": refreshes the package lists. Argument: package manager.
pm=$1
echo "== Обновляю списки пакетов"
case $pm in
  apt) apt-get -o DPkg::Lock::Timeout=600 update ;;
  dnf|yum) $pm -q makecache --refresh 2>/dev/null || $pm -q makecache ;;
  apk) apk update ;;
  *) echo "!! $pm не поддерживается"; exit 2 ;;
esac
echo "== Готово"
