package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Apps built on the server from a git checkout (Причал itself included) have
// no registry to compare with. For them the panel looks at the checkout: it
// fetches on the host and counts the commits the upstream branch is ahead.
// Updating is `git pull --ff-only`, a rebuild and a restart, with a rollback
// to the previous commit if the new build does not come up.

// GitInfo is what the panel knows about the checkout of a local app.
type GitInfo struct {
	Branch   string   `json:"branch"`
	Current  string   `json:"current"` // short hash of the checked-out commit
	Latest   string   `json:"latest"`  // short hash of the upstream branch
	Behind   int      `json:"behind"`
	Dirty    bool     `json:"dirty"`    // tracked files changed on the server
	Diverged bool     `json:"diverged"` // local commits not in upstream: no fast-forward
	Commits  []string `json:"commits"`  // subjects of the new commits, newest first
}

// gitEnv makes git fail instead of asking questions on a terminal nobody has.
// Git runs as root here, and a checkout's .git/config can name commands for
// git to run. So git's own ownership check stays on (a checkout that is not
// root's is refused unless root added it to safe.directory), and fsmonitor,
// a speed-up the panel does not need, is off.
const gitEnv = `export GIT_TERMINAL_PROMPT=0
git config core.sshCommand >/dev/null 2>&1 || export GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=15"
g() { git -c core.fsmonitor= "$@"; }`

// gitCheckScript prints KEY value lines. Argument: working dir.
// It runs unattended (opening the page, the weekly digest), so hooks are off too.
const gitCheckScript = `cd "$1" 2>/dev/null || { echo "SKIP"; exit 0; }
[ -e .git ] || { echo "SKIP"; exit 0; }
command -v git >/dev/null 2>&1 || { echo "ERR git не установлен на сервере"; exit 0; }
` + gitEnv + `
out=$(g rev-parse --git-dir 2>&1) || {
  case $out in
  *"dubious ownership"*) echo "ERR папка принадлежит не root, и git ей не доверяет. Если доверяете, выполните на сервере: git config --global --add safe.directory $1" ;;
  *) echo "ERR git не открыл папку: $(echo "$out" | head -n 1)" ;;
  esac
  exit 0
}
up=$(g rev-parse --abbrev-ref '@{u}' 2>/dev/null) || { echo "ERR у ветки нет upstream, нечего сравнивать"; exit 0; }
out=$(g -c core.hooksPath=/dev/null fetch --quiet 2>&1) || { echo "ERR не удалось связаться с git: $(echo "$out" | tail -n 1)"; exit 0; }
echo "BRANCH $(g rev-parse --abbrev-ref HEAD)"
echo "CURRENT $(g rev-parse --short HEAD)"
echo "LATEST $(g rev-parse --short '@{u}')"
echo "BEHIND $(g rev-list --count 'HEAD..@{u}')"
echo "AHEAD $(g rev-list --count '@{u}..HEAD')"
echo "DIRTY $(g status --porcelain --untracked-files=no | wc -l)"
g log --format='LOG %s' -n 8 'HEAD..@{u}'`

func parseGitCheck(out string) (*GitInfo, string) {
	g := &GitInfo{Commits: []string{}}
	ahead := 0
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "SKIP":
			return nil, ""
		case "ERR":
			return nil, val
		case "BRANCH":
			g.Branch = val
		case "CURRENT":
			g.Current = val
		case "LATEST":
			g.Latest = val
		case "BEHIND":
			g.Behind, _ = strconv.Atoi(val)
		case "AHEAD":
			ahead, _ = strconv.Atoi(val)
		case "DIRTY":
			n, _ := strconv.Atoi(val)
			g.Dirty = n > 0
		case "LOG":
			g.Commits = append(g.Commits, val)
		}
	}
	if g.Current == "" {
		return nil, ""
	}
	g.Diverged = ahead > 0 && g.Behind > 0
	return g, ""
}

// checkGit fills App.Git for the apps built on this server.
func (a *Apps) checkGit(ctx context.Context, v *AppsView) {
	var wg sync.WaitGroup
	for i := range v.Apps {
		app := &v.Apps[i]
		if !app.Local || app.Error != "" || app.workDir == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			out, _, err := a.docker.HostRun(rctx, "/bin/sh", "-c", gitCheckScript, "prichal", app.workDir)
			if err != nil {
				return // no git information, the card stays as it was
			}
			info, msg := parseGitCheck(out)
			app.Git = info
			if msg != "" {
				app.GitError = msg
			}
			if info != nil && info.Behind > 0 {
				app.Update = true
			}
		}()
	}
	wg.Wait()
}

// gitUpdateScript updates one compose service from its git checkout.
// Arguments: project, service, working dir, compose files (comma separated).
const gitUpdateScript = `project=$1; svc=$2; wd=$3; files=$4
cd "$wd"
` + gitEnv + `
fargs=""
old_ifs=$IFS; IFS=,
for f in $files; do fargs="$fargs -f $f"; done
IFS=$old_ifs
dc() { docker compose -p "$project" $fargs "$@"; }
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
sleep 10
cid=$(dc ps -q "$svc" | head -n 1)
[ -n "$cid" ] && [ "$(docker inspect -f '{{.State.Running}} {{.State.Restarting}}' "$cid")" = "true false" ] || { echo "!! $svc не поднялся"; exit 1; }
ok=1
docker image prune -f >/dev/null 2>&1 || true
docker builder prune -f >/dev/null 2>&1 || true
echo "== Готово"`

// UpdateGit updates one app from its git checkout.
func (u *Updates) UpdateGit(ctx context.Context, key string) (*Task, error) {
	app, err := u.apps.Find(ctx, key)
	if err != nil {
		return nil, err
	}
	switch {
	case app == nil:
		return nil, errors.New("приложение не найдено, обновите страницу")
	case app.Git == nil || app.Git.Behind == 0:
		return nil, errors.New("новой версии в git нет, нажмите «Проверить»")
	case app.Git.Diverged:
		return nil, errors.New("на сервере есть свои коммиты, обновление без слияния невозможно")
	}
	args := []string{app.Project, app.Service, app.workDir, app.files}
	return u.Start(ctx, "git", "обновление "+app.Title+" из git", args)
}
