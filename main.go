// Причал: a small Russian-language panel for a Linux server with Docker.
// Containers with their health and load, start/stop/kill/restart/pause, logs,
// images and cleanup, clients of Amnezia VPN, system and app updates,
// Telegram alerts. Meant to be reached through an SSH tunnel on 127.0.0.1.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

type server struct {
	docker *Docker
	col    *Collector
	conns  *ConnWatcher
	upd    *Updates
	auth   *Auth
	hosts  map[string]bool // extra hostnames allowed besides localhost
	locked string          // non-empty: reachable from outside without a password; why
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// hostLabel is the server name shown in the header: LABEL, or the host's own
// hostname.
func hostLabel(hostRoot string) string {
	if l := os.Getenv("LABEL"); l != "" {
		return l
	}
	if b, err := os.ReadFile(filepath.Join(hostRoot, "etc", "hostname")); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func main() {
	listen := env("LISTEN", ":9443")
	hostRoot := env("HOST_ROOT", "/")
	dataDir := env("DATA_DIR", "")
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
	s := &server{
		docker: d,
		col:    NewCollector(d, selfID, hostRoot, hostLabel(hostRoot)),
		conns:  &ConnWatcher{docker: d, labels: LoadLabels(dataDir)},
		upd:    NewUpdates(d, apps, hostRoot, dataDir),
		auth:   NewAuth(os.Getenv("PRICHAL_PASSWORD")),
		hosts:  map[string]bool{},
	}
	for _, h := range strings.Split(os.Getenv("ALLOWED_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			s.hosts[h] = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	exposed, addr := exposure(ctx, d, selfID, listen)
	cancel()
	s.locked = lockReason(exposed, addr, len(s.hosts) > 0, s.auth.Enabled())
	if exposed && s.locked == "" {
		log.Printf("панель доступна снаружи (%s), вход по паролю", addr)
	}
	if s.locked != "" {
		log.Printf("ВНИМАНИЕ: работа заблокирована: %s. Задайте PRICHAL_PASSWORD в .env", s.locked)
	}

	if tg := NewTelegram(os.Getenv("TG_TOKEN"), os.Getenv("TG_USERNAME"), dataDir); tg != nil {
		go NewNotifier(d, tg, hostRoot, s.upd).Run(context.Background())
	} else {
		log.Printf("Telegram не настроен (нет TG_TOKEN/TG_USERNAME), уведомления выключены")
	}

	static, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/session", s.session)
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
	mux.HandleFunc("POST /api/updates/reboot", s.updatesReboot)
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

// lockReason says why the panel must refuse to work, or "" if it may. It is
// root-equivalent, so without a password it is only allowed on 127.0.0.1.
// ALLOWED_HOSTS means a reverse proxy publishes it under a domain, where the
// 127.0.0.1 binding no longer protects it.
func lockReason(exposed bool, addr string, proxied, password bool) string {
	switch {
	case password:
		return ""
	case exposed:
		return "её порт открыт наружу (" + addr + "), а пароль не задан"
	case proxied:
		return "задан ALLOWED_HOSTS (доступ через обратный прокси), а пароль не задан"
	}
	return ""
}

func (s *server) hostAllowed(host string) bool {
	h := strings.ToLower(host)
	if i := strings.LastIndex(h, ":"); i > strings.LastIndex(h, "]") {
		h = h[:i] // drop the port
	}
	return isLoopback(h) || s.hosts[strings.Trim(h, "[]")]
}

// Without a session only the login page and its assets are served.
var publicPaths = map[string]bool{
	"/login.html": true, "/login.js": true, "/app.css": true, "/fonts.css": true, "/favicon.svg": true,
	"/api/health": true, "/api/session": true, "/api/login": true,
}

// guard blocks everything that does not come from the panel's own page:
// other sites in the same browser (CSRF), DNS-rebinding tricks, and, when a
// password is set, visitors without a session. POST additionally needs a
// custom header, which a foreign page cannot send without a CORS preflight
// that is never approved.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "forbidden host: add it to ALLOWED_HOSTS", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Prichal") != "1" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.locked != "" && r.URL.Path != "/api/health" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Причал заблокирован: " + s.locked + ".\n\n" +
				"Панель управляет Docker и сервером, без пароля это опасно. Сделайте одно из двух:\n" +
				"  1) задайте PRICHAL_PASSWORD в .env и выполните docker compose up -d;\n" +
				"  2) уберите ALLOWED_HOSTS, привяжите порт к 127.0.0.1 (\"127.0.0.1:9443:9443\")\n" +
				"     и заходите через SSH-туннель.\n"))
			return
		}
		if s.auth.Enabled() && !publicPaths[r.URL.Path] && !strings.HasPrefix(r.URL.Path, "/fonts/") && !s.auth.Valid(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "нужно войти"})
				return
			}
			http.Redirect(w, r, "/login.html", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"auth": s.auth.Enabled(), "valid": !s.auth.Enabled() || s.auth.Valid(r)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	msg := err.Error()
	var ae *APIError
	if errors.As(err, &ae) {
		status, msg = ae.Status, ae.Message
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func ctxTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 15*time.Second)
	defer cancel()
	ov, err := s.col.Overview(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

// findContainer accepts only IDs of containers that exist right now.
func (s *server) findContainer(ctx context.Context, id string) (*ContainerSummary, bool, error) {
	list, err := s.docker.Containers(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, c := range list {
		if c.ID == id {
			self := s.col.selfID != "" && strings.HasPrefix(c.ID, s.col.selfID)
			return &c, self, nil
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
	if self {
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не понял запрос"})
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

// ---------- Обновления ----------

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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не понял запрос"})
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не понял запрос"})
		return
	}
	ctx, cancel := ctxTimeout(r, 120*time.Second)
	defer cancel()
	t, err := s.upd.UpdateApp(ctx, in.Key, in.Backup)
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

type imageView struct {
	ID      string   `json:"id"`
	Tags    []string `json:"tags"`
	Size    int64    `json:"size"`
	Created int64    `json:"created"`
	UsedBy  []string `json:"usedBy"`
}

func (s *server) imageViews(ctx context.Context) ([]imageView, int64, error) {
	imgs, err := s.docker.Images(ctx)
	if err != nil {
		return nil, 0, err
	}
	conts, err := s.docker.Containers(ctx)
	if err != nil {
		return nil, 0, err
	}
	used := map[string][]string{}
	for _, c := range conts {
		used[c.ImageID] = append(used[c.ImageID], c.Name())
	}
	out := make([]imageView, 0, len(imgs))
	for _, im := range imgs {
		tags := []string{}
		for _, t := range im.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		u := used[im.ID]
		if u == nil {
			u = []string{}
		}
		for _, t := range tags {
			if t == helperImage && len(u) == 0 {
				u = []string{"prichal"} // runs host commands for the Updates tab
			}
		}
		out = append(out, imageView{ID: im.ID, Tags: tags, Size: im.Size, Created: im.Created, UsedBy: u})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Size > out[b].Size })

	var cache int64
	if du, err := s.docker.DiskUsage(ctx); err == nil {
		for _, b := range du.BuildCache {
			if !b.InUse {
				cache += b.Size
			}
		}
	}
	return out, cache, nil
}

func (s *server) images(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 20*time.Second)
	defer cancel()
	imgs, cache, err := s.imageViews(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": imgs, "buildCache": cache})
}

func (s *server) removeImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	imgs, _, err := s.imageViews(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, im := range imgs {
		if im.ID != id {
			continue
		}
		if len(im.UsedBy) > 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "образ используется контейнером " + strings.Join(im.UsedBy, ", ")})
			return
		}
		log.Printf("remove image %s %v", id, im.Tags)
		if err := s.docker.RemoveImage(ctx, id); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "образ не найден"})
}

func (s *server) prune(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 5*time.Minute)
	defer cancel()
	log.Printf("prune unused images and build cache")
	n, err := s.docker.Prune(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"reclaimed": n})
}
