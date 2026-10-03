# shellcheck shell=sh
# dc runs docker compose for the app being updated. Needs $project and $files
# (the compose files, comma separated; paths may contain spaces).
# shellcheck disable=SC2154
dc() {
  _dc_n=$#
  _dc_ifs=$IFS
  IFS=,
  set -f
  for _dc_f in $files; do set -- "$@" -f "$_dc_f"; done
  set +f
  IFS=$_dc_ifs
  # Move the caller's arguments after the -f options.
  while [ "$_dc_n" -gt 0 ]; do set -- "$@" "$1"; shift; _dc_n=$((_dc_n - 1)); done
  docker compose -p "$project" "$@"
}

# wait_up checks that every container of service $1 came up. It gives them
# PRICHAL_SETTLE seconds (10) to crash, then waits up to PRICHAL_WAIT seconds
# (120) for those with a healthcheck to turn healthy. Fails at once for a
# container that is not running, restarting or unhealthy.
wait_up() {
  sleep "${PRICHAL_SETTLE:-10}"
  _w_end=$(( $(date +%s) + ${PRICHAL_WAIT:-120} ))
  while :; do
    _w_ids=$(dc ps -q "$1")
    [ -n "$_w_ids" ] || return 1
    _w_pending=0
    for _w_id in $_w_ids; do
      _w_s=$(docker inspect -f '{{.State.Running}} {{.State.Restarting}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$_w_id" 2>/dev/null) || return 1
      case $_w_s in
        "true false none" | "true false healthy") ;;
        "true false starting") _w_pending=1 ;;
        *) return 1 ;;
      esac
    done
    [ "$_w_pending" = 0 ] && return 0
    [ "$(date +%s)" -lt "$_w_end" ] || return 1
    sleep 2
  done
}
