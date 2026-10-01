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
		return nil, errors.New("новой версии в git нет, нажмите «Проверить обновления»")
	case app.Git.Diverged:
		return nil, errors.New("на сервере есть свои коммиты, обновление без слияния невозможно")
	}
	args := []string{app.Project, app.Service, app.workDir, app.files}
	return u.Start(ctx, "git", "обновление "+app.Title+" из git", args)
}
