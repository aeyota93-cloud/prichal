# shellcheck shell=sh
# Task "git": updates one compose service from its git checkout: pull, build,
# restart, and back to the previous commit if the new version does not come
# up. Arguments: project, service, working dir, compose files (comma
# separated).
# The way back is reset --keep: files edited on the server are never thrown
# away (--hard would delete them together with the failed update).
# project and files are read by dc() from lib-compose.sh.
# shellcheck disable=SC2034
project=$1; svc=$2; wd=$3; files=$4
cd "$wd" || exit 1
git_setup
old=$(g rev-parse HEAD)
pulled=0; ok=0
trap '[ "$ok" = 1 ] || { echo "!! Ошибка."; if [ "$pulled" = 1 ]; then echo "== Возвращаю прежнюю версию"; if g reset --keep "$old"; then dc up -d --build "$svc"; else echo "!! Не вернуть: правки на сервере мешают. Файлы не тронуты"; fi; fi; }' EXIT
echo "== Забираю новую версию из git"
g pull --ff-only
pulled=1
echo "== Версия: $(g log -1 --format='%h %s')"
echo "== Собираю (первый раз может занять пару минут)"
dc build "$svc"
echo "== Запускаю новую версию"
dc up -d "$svc"
echo "== Проверяю, что запустилась"
wait_up "$svc" || { echo "!! $svc не поднялся"; exit 1; }
ok=1
docker image prune -f >/dev/null 2>&1 || true
echo "== Готово"
