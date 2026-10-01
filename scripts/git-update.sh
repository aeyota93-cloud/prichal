# shellcheck shell=sh
# Task "git": updates one compose service from its git checkout: pull, build,
# restart, and back to the previous commit if the new version does not come
# up. Arguments: project, service, working dir, compose files (comma
# separated).
project=$1; svc=$2; wd=$3; files=$4
cd "$wd" || exit 1
git_setup
old=$(g rev-parse HEAD)
pulled=0; ok=0
trap '[ "$ok" = 1 ] || { echo "!! Ошибка."; if [ "$pulled" = 1 ]; then echo "== Возвращаю прежнюю версию"; g reset --hard "$old"; dc up -d --build "$svc"; fi; }' EXIT
echo "== Забираю новую версию из git"
g pull --ff-only
pulled=1
echo "== Версия: $(g log -1 --format='%h %s')"
echo "== Собираю (первый раз может занять пару минут)"
dc build "$svc"
echo "== Запускаю новую версию"
dc up -d "$svc"
echo "== Проверяю, что запустилась"
sleep "${PRICHAL_SETTLE:-10}"
cid=$(dc ps -q "$svc" | head -n 1)
[ -n "$cid" ] && [ "$(docker inspect -f '{{.State.Running}} {{.State.Restarting}}' "$cid")" = "true false" ] || { echo "!! $svc не поднялся"; exit 1; }
ok=1
docker image prune -f >/dev/null 2>&1 || true
docker builder prune -f >/dev/null 2>&1 || true
echo "== Готово"
