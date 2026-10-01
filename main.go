// Причал: a small Russian-language panel for a Linux server with Docker.
// An overview with a timeline of events, containers with their health and
// load, start/stop/kill/restart/pause, logs, images and cleanup, clients of
// Amnezia VPN, system and app updates, server reboot, Telegram alerts. Meant
// to be reached through an SSH tunnel on 127.0.0.1.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

type server struct {
	docker  *Docker
	col     *Collector
	conns   *ConnWatcher
	upd     *Updates
	auth    *Auth
	journal *Journal
	tg      *TgHub          // Telegram alerts, switched from the panel
	hosts   map[string]bool // extra hostnames allowed besides localhost
	locked  string          // non-empty: reachable from outside without a password; why
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// hostLabel is the server name shown on the Overview and in the page title:
// LABEL, or the host's own hostname.
func hostLabel(hostRoot string) string {
	if l := os.Getenv("LABEL"); l != "" {
		return l
	}
	if b, err := os.ReadFile(filepath.Join(hostRoot, "etc", "hostname")); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

// isSelf: container id is the panel's own container.
func isSelf(selfID, id string) bool { return selfID != "" && strings.HasPrefix(id, selfID) }

func main() {
	listen := env("LISTEN", ":9443")
	hostRoot := env("HOST_ROOT", "/")
	dataDir := env("DATA_DIR", "")

	// docker exec prichal /prichal reset-password: forget the password picked
	// on the first visit (see auth.go).
	if len(os.Args) > 1 {
		if os.Args[1] != "reset-password" {
			fmt.Fprintln(os.Stderr, "использование: prichal [reset-password]")
			os.Exit(2)
		}
		if err := resetPassword(dataDir); err != nil {
			fmt.Fprintln(os.Stderr, "не получилось:", err)
			os.Exit(1)
		}
		fmt.Println("Пароль сброшен. Перезапустите панель (docker restart prichal) и задайте новый на первой странице.")
		return
	}
	d, err := NewDocker(env("DOCKER_HOST", "unix:///var/run/docker.sock"))
	if err != nil {
		log.Fatal(err)
	}

	// Inside a container the hostname is the short container ID; used to stop
	// the panel from switching itself off.
	selfID := env("SELF_ID", "")
	if selfID == "" {
		if h, err := os.Hostname(); err == nil && len(h) == 12 {
			selfID = h
		}
	}

	apps := NewApps(d, selfID)
	journal := LoadJournal(dataDir)
	s := &server{
		docker:  d,
		col:     NewCollector(d, selfID, hostRoot, hostLabel(hostRoot)),
		conns:   &ConnWatcher{docker: d, labels: LoadLabels(dataDir)},
		upd:     NewUpdates(d, apps, hostRoot, dataDir),
		auth:    NewAuth(os.Getenv("PRICHAL_PASSWORD"), dataDir, journal),
		journal: journal,
		hosts:   map[string]bool{},
	}
	s.upd.journal = journal
	for _, h := range strings.Split(os.Getenv("ALLOWED_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			s.hosts[h] = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	exposed, addr := exposure(ctx, d, selfID, listen)
	cancel()
	s.locked = lockReason(exposed, addr, len(s.hosts) > 0, s.auth.HasPassword())
	switch {
	case s.locked != "":
		log.Printf("ВНИМАНИЕ: работа заблокирована: %s. Задайте PRICHAL_PASSWORD в .env", s.locked)
	case exposed:
		log.Printf("панель доступна снаружи (%s), вход по паролю", addr)
	case !s.auth.HasPassword():
		log.Printf("пароль ещё не задан: откройте панель через SSH-туннель и придумайте его")
	}

	s.tg = NewTgHub(dataDir, os.Getenv("TG_TOKEN"), os.Getenv("TG_USERNAME"))
	if s.tg.Status().State == "off" {
		log.Printf("Telegram не настроен, уведомления выключены (подключить: Обзор → Уведомления)")
	}
	go NewNotifier(d, s.tg, hostRoot, s.upd, s.journal).Run(context.Background())

	static, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/session", s.auth.session)
	mux.HandleFunc("POST /api/setup", s.auth.setup)
	mux.HandleFunc("POST /api/login", s.auth.login)
	mux.HandleFunc("POST /api/logout", s.auth.logout)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("POST /api/containers/{id}/{action}", s.action)
	mux.HandleFunc("GET /api/containers/{id}/logs", s.logs)
	mux.HandleFunc("GET /api/connections", s.connections)
	mux.HandleFunc("POST /api/labels", s.setLabel)
	mux.HandleFunc("GET /api/updates", s.updates)
	mux.HandleFunc("GET /api/updates/task", s.updatesTask)
	mux.HandleFunc("POST /api/updates/check", s.updatesCheck)
	mux.HandleFunc("POST /api/updates/install", s.updatesInstall)
	mux.HandleFunc("POST /api/updates/app", s.updatesApp)
	mux.HandleFunc("POST /api/updates/git", s.updatesGit)
	mux.HandleFunc("POST /api/updates/reboot", s.updatesReboot)
	mux.HandleFunc("GET /api/telegram", s.telegramStatus)
	mux.HandleFunc("POST /api/telegram", s.telegramSave)
	mux.HandleFunc("POST /api/telegram/test", s.telegramTest)
	mux.HandleFunc("POST /api/telegram/off", s.telegramOff)
	mux.HandleFunc("GET /api/images", s.images)
	mux.HandleFunc("POST /api/images/{id}/remove", s.removeImage)
	mux.HandleFunc("POST /api/prune", s.prune)

	srv := &http.Server{
		Addr:              listen,
		Handler:           s.guard(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("Причал слушает %s", listen)
	log.Fatal(srv.ListenAndServe())
}
