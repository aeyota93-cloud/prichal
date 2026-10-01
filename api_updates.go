package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// The Updates tab: system packages, compose apps, git checkouts, reboot.

func (s *server) updates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.upd.View(ctx, r.URL.Query().Get("fresh") == "1"))
}

func (s *server) updatesTask(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 30*time.Second)
	defer cancel()
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	t := s.upd.Task(ctx)
	text, next := s.upd.TaskLog(ctx, offset)
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "log": text, "offset": next})
}

func (s *server) taskReply(w http.ResponseWriter, t *Task, err error) {
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errBusy) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (s *server) updatesCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	t, err := s.upd.Check(ctx)
	s.taskReply(w, t, err)
}

func (s *server) updatesInstall(w http.ResponseWriter, r *http.Request) {
	var in struct{ Packages []string }
	if !readJSON(w, r, 64<<10, &in) {
		return
	}
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	t, err := s.upd.Install(ctx, in.Packages)
	s.taskReply(w, t, err)
}

func (s *server) updatesApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key    string
		Backup bool
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	ctx, cancel := ctxTimeout(r, 120*time.Second)
	defer cancel()
	t, err := s.upd.UpdateApp(ctx, in.Key, in.Backup)
	s.taskReply(w, t, err)
}

func (s *server) updatesGit(w http.ResponseWriter, r *http.Request) {
	var in struct{ Key string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	ctx, cancel := ctxTimeout(r, 120*time.Second)
	defer cancel()
	t, err := s.upd.UpdateGit(ctx, in.Key)
	s.taskReply(w, t, err)
}

func (s *server) updatesReboot(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	if err := s.upd.Reboot(ctx); err != nil {
		s.taskReply(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
