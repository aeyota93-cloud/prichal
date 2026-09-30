package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentify(t *testing.T) {
	cases := []struct {
		name, image, service string
		self                 bool
		title, kind          string
	}{
		{"amnezia-awg2", "amnezia-awg2", "", false, "AmneziaWG", "vpn"},
		{"amnezia-telemt", "amnezia-telemt", "", false, "Прокси Telegram", "proxy"},
		{"amnezia-newproto", "x", "", false, "newproto", "vpn"},
		{"n8n-n8n-1", "n8nio/n8n:latest", "n8n", false, "n8n", "app"},
		{"db", "docker.io/library/postgres:16", "db", false, "PostgreSQL", "app"},
		{"wg", "ghcr.io/wg-easy/wg-easy:15", "wg", false, "wg-easy", "vpn"},
		{"web-1", "registry.example.com:5000/team/site:2", "web", false, "web", "other"},
		{"lonely", "busybox", "", false, "lonely", "other"},
		{"prichal", "prichal:local", "prichal", true, "Причал", "self"},
	}
	for _, c := range cases {
		id := identify(c.name, c.image, c.service, c.self)
		if id.Title != c.title || id.Kind != c.kind {
			t.Errorf("%s (%s): got %+v", c.name, c.image, id)
		}
	}
}

func TestParseRef(t *testing.T) {
	cases := map[string]imageRef{
		"nginx":                           {"registry-1.docker.io", "library/nginx", "latest", ""},
		"n8nio/n8n:latest":                {"registry-1.docker.io", "n8nio/n8n", "latest", ""},
		"ghcr.io/wg-easy/wg-easy:15":      {"ghcr.io", "wg-easy/wg-easy", "15", ""},
		"localhost:5000/app:dev":          {"localhost:5000", "app", "dev", ""},
		"lscr.io/linuxserver/wireguard":   {"lscr.io", "linuxserver/wireguard", "latest", ""},
		"redis@sha256:abc":                {"registry-1.docker.io", "library/redis", "latest", "sha256:abc"},
		"docker.io/library/postgres:16.4": {"registry-1.docker.io", "library/postgres", "16.4", ""},
	}
	for in, want := range cases {
		if got := parseRef(in); got != want {
			t.Errorf("%s: got %+v, want %+v", in, got, want)
		}
	}
	if !parseRef("postgres:16.4").pinned() || !parseRef("app:v2.1.0-alpine").pinned() || parseRef("n8nio/n8n:latest").pinned() {
		t.Error("pinned detection is wrong")
	}
}

func TestHostAllowed(t *testing.T) {
	s := &server{hosts: map[string]bool{"panel.example.com": true}}
	for h, want := range map[string]bool{
		"localhost:9443": true, "127.0.0.1:19443": true, "[::1]:9443": true, "localhost": true,
		"panel.example.com": true, "PANEL.example.com:443": true,
		"evil.example:9443": false, "127.0.0.1.evil.example": false,
	} {
		if got := s.hostAllowed(h); got != want {
			t.Errorf("%s: %v, want %v", h, got, want)
		}
	}
}

func TestGuardAndLogin(t *testing.T) {
	s := &server{hosts: map[string]bool{}, auth: NewAuth("secret")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("data")) })
	mux.HandleFunc("POST /api/login", s.auth.login)
	h := s.guard(mux)

	do := func(method, path, body string, hdr map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:9443"+path, strings.NewReader(body))
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/api/overview", "", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", w.Code)
	}
	post := map[string]string{"X-Prichal": "1", "Content-Type": "application/json"}
	if w := do("POST", "/api/login", `{"password":"nope"}`, post, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if w := do("POST", "/api/login", `{"password":"secret"}`, map[string]string{"Content-Type": "application/json"}, nil); w.Code != http.StatusForbidden {
		t.Fatalf("login without X-Prichal must be refused: %d", w.Code)
	}
	w := do("POST", "/api/login", `{"password":"secret"}`, post, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie flags: %+v", c)
	}
	if w := do("GET", "/api/overview", "", nil, c); w.Code != http.StatusOK {
		t.Errorf("with a session: %d", w.Code)
	}

	s.locked = "0.0.0.0:9443"
	if w := do("GET", "/api/overview", "", nil, c); w.Code != http.StatusServiceUnavailable {
		t.Errorf("locked panel must refuse: %d", w.Code)
	}
}
