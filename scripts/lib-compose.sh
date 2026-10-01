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
