# shellcheck shell=sh
# Task "app": updates one docker compose service from its registry.
# Arguments: project, service, working dir, compose files (comma separated),
# backup (1/0), helper image, then the volumes to back up.
project=$1; svc=$2; wd=$3; files=$4; backup=$5; helper=$6; shift 6
backups=${PRICHAL_BACKUPS:-/var/backups/prichal}
cd "$wd" || exit 1
echo "== Скачиваю новую версию ($svc)"
# The image the container runs now: kept until the update is confirmed.
old_img=$(docker inspect -f '{{.Image}}' "$(dc ps -q "$svc" | head -n 1)" 2>/dev/null) || old_img=
dc pull "$svc"
ok=0; upped=0
# Before "up" the old container starts again as it was: "up" would recreate it
# from the image just pulled, i.e. update without the backup. After "up" the
# new container stays for its log, and the old image ID is shown.
trap '[ "$ok" = 1 ] || { if [ "$upped" = 1 ]; then echo "!! Новая версия не заработала. Контейнер оставлен, чтобы посмотреть журнал."; [ -z "$old_img" ] || echo "   Прежняя версия: образ $old_img (данные могли измениться, сверьтесь с README)"; else echo "!! Ошибка. Запускаю $svc обратно"; dc start "$svc" || dc up -d "$svc"; fi; }' EXIT
if [ "$backup" = 1 ] && [ $# -gt 0 ]; then
  echo "== Останавливаю $svc, чтобы сделать копию данных"
  dc stop "$svc"
  # Copies hold the apps' data, secrets included: only root may read them.
  mkdir -p "$backups"
  chmod 700 "$backups"
  dir=$backups/$project/$(date +%Y%m%d-%H%M%S)
  mkdir -p "$dir"
  for v in "$@"; do
    echo "   копирую том $v"
    docker run --rm --name "prichal-host-backup-$$" -v "$v":/data:ro -v "$dir":/backup "$helper" tar czf "/backup/$v.tar.gz" -C /data .
  done
  old_ifs=$IFS; IFS=,; set -f
  for f in $files; do cp "$f" "$dir"/ 2>/dev/null || true; done
  set +f; IFS=$old_ifs
  echo "== Копия данных: $dir"
fi
echo "== Запускаю новую версию"
dc up -d "$svc"
upped=1
echo "== Проверяю, что запустилась"
wait_up "$svc" || { echo "!! $svc не поднялся"; exit 1; }
ok=1
docker image prune -f >/dev/null 2>&1 || true
ls -1dt "$backups/$project"/*/ 2>/dev/null | tail -n +6 | xargs -r rm -rf
echo "== Готово. Хранятся 5 последних копий в $backups/$project"
