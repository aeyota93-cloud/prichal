package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Overview, containers (actions and logs) and Amnezia connections.

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 15*time.Second)
	defer cancel()
	ov, err := s.col.Overview(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*Overview
		Events   []Event `json:"events"`
		Telegram string  `json:"telegram"` // who gets the alerts; empty: alerts are off
		Zone     string  `json:"zone"`     // time zone of the weekly digest
	}{ov, s.journal.List(), s.tgUser, zoneName})
}

// actionDone is how the timeline says what the panel did to a container.
var actionDone = map[string]string{
	"start": "запущен", "stop": "остановлен", "restart": "перезапущен",
	"kill": "остановлен принудительно", "pause": "поставлен на паузу", "unpause": "снят с паузы",
}

// findContainer accepts only IDs of containers that exist right now.
func (s *server) findContainer(ctx context.Context, id string) (*ContainerSummary, bool, error) {
	list, err := s.docker.Containers(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, c := range list {
		if c.ID == id {
			return &c, isSelf(s.col.selfID, c.ID), nil
		}
	}
	return nil, false, nil
}

var actions = map[string]bool{"start": true, "stop": true, "restart": true, "kill": true, "pause": true, "unpause": true}

func (s *server) action(w http.ResponseWriter, r *http.Request) {
	id, act := r.PathValue("id"), r.PathValue("action")
	if !actions[act] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неизвестное действие"})
		return
	}
	ctx, cancel := ctxTimeout(r, 40*time.Second)
	defer cancel()
	c, self, err := s.findContainer(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "контейнер не найден"})
		return
	}
	if self && act != "restart" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "панель не может выключить сама себя"})
		return
	}
	log.Printf("%s: %s", c.Name(), act)
	title := identify(c.Name(), c.Image, c.Labels["com.docker.compose.service"], self).Title
	if self {
		s.journal.Add("", title+" "+actionDone[act]+" из панели")
		// Answer first: the restart will cut this very connection.
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := s.docker.Action(context.Background(), id, act); err != nil {
				log.Printf("self restart failed: %v", err)
			}
		}()
		return
	}
	if err := s.docker.Action(ctx, id, act); err != nil {
		log.Printf("%s: %s failed: %v", c.Name(), act, err)
		writeErr(w, err)
		return
	}
	s.col.Remember(id, act)
	s.journal.Add("", title+" "+actionDone[act]+" из панели")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (s *server) logs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 2000 {
		tail = 300
	}
	ctx, cancel := ctxTimeout(r, 20*time.Second)
	defer cancel()
	c, _, err := s.findContainer(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "контейнер не найден"})
		return
	}
	tty := s.col.IsTTY(id)
	lines, err := s.docker.Logs(ctx, id, tail, tty)
	if err != nil {
		writeErr(w, err)
		return
	}
	if lines == nil {
		lines = []LogLine{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *server) connections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 20*time.Second)
	defer cancel()
	c, err := s.conns.Get(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) setLabel(w http.ResponseWriter, r *http.Request) {
	var in struct{ Container, Name, Label string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	if _, ok := probeFor(in.Container); !ok || in.Name == "" || len(in.Name) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нет такого клиента"})
		return
	}
	if err := s.conns.labels.Set(in.Container, in.Name, in.Label); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("label %s/%s = %q", in.Container, in.Name, in.Label)
	s.conns.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
