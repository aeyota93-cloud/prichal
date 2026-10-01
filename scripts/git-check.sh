# shellcheck shell=sh
# Prints KEY value lines for parseGitCheck (gitupdate.go). Argument: working
# dir. It runs unattended (opening the page, the weekly digest), so hooks are
# off too.
cd "$1" 2>/dev/null || { echo "SKIP"; exit 0; }
[ -e .git ] || { echo "SKIP"; exit 0; }
command -v git >/dev/null 2>&1 || { echo "ERR git не установлен на сервере"; exit 0; }
git_setup
out=$(g rev-parse --git-dir 2>&1) || {
  case $out in
  *"dubious ownership"*) echo "ERR папка принадлежит не root, и git ей не доверяет. Если доверяете, выполните на сервере: git config --global --add safe.directory $1" ;;
  *) echo "ERR git не открыл папку: $(echo "$out" | head -n 1)" ;;
  esac
  exit 0
}
g rev-parse --abbrev-ref '@{u}' >/dev/null 2>&1 || { echo "ERR у ветки нет upstream, нечего сравнивать"; exit 0; }
out=$(g -c core.hooksPath=/dev/null fetch --quiet 2>&1) || { echo "ERR не удалось связаться с git: $(echo "$out" | tail -n 1)"; exit 0; }
echo "BRANCH $(g rev-parse --abbrev-ref HEAD)"
echo "CURRENT $(g rev-parse --short HEAD)"
echo "LATEST $(g rev-parse --short '@{u}')"
echo "BEHIND $(g rev-list --count 'HEAD..@{u}')"
echo "AHEAD $(g rev-list --count '@{u}..HEAD')"
echo "DIRTY $(g status --porcelain --untracked-files=no | wc -l)"
g log --format='LOG %s' -n 8 'HEAD..@{u}'
