# shellcheck shell=sh
# Task "app": updates one docker compose service from its registry.
# Arguments: project, service, working dir, compose files (comma separated),
# backup (1/0), helper image, then the volumes to back up.
project=$1; svc=$2; wd=$3; files=$4; backup=$5; helper=$6; shift 6
backups=${PRICHAL_BACKUPS:-/var/backups/prichal}
cd "$wd" || exit 1
echo "== Скачиваю новую версию ($svc)"
dc pull "$svc"
ok=0
# On failure the old container starts again as it was: "up" would recreate it
# from the image just pulled, i.e. update without the backup.
trap '[ "$ok" = 1 ] || { echo "!! Ошибка. Запускаю $svc обратно"; dc start "$svc" || dc up -d "$svc"; }' EXIT
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
ok=1
docker image prune -f >/dev/null 2>&1 || true
ls -1dt "$backups/$project"/*/ 2>/dev/null | tail -n +6 | xargs -r rm -rf
echo "== Готово. Хранятся 5 последних копий в $backups/$project"
