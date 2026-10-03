package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() { pbkdf2Iter = 1000 } // keep the tests fast

// testPanel is a guarded mux with the auth routes, like main() builds it.
func testPanel(a *Auth) http.Handler {
	s := &server{hosts: map[string]bool{}, auth: a}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("data")) })
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	return s.guard(mux)
}

type req struct {
	method, path, body string
	hdr                map[string]string
	cookie             *http.Cookie
	http10             bool
}

func (q req) do(h http.Handler) *httptest.ResponseRecorder {
	r := httptest.NewRequest(q.method, "http://localhost:9443"+q.path, strings.NewReader(q.body))
	if q.method == http.MethodPost {
		r.Header.Set("X-Prichal", "1")
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range q.hdr {
		r.Header.Set(k, v)
	}
	if q.cookie != nil {
		r.AddCookie(q.cookie)
	}
	if q.http10 {
		r.Proto, r.ProtoMajor, r.ProtoMinor = "HTTP/1.0", 1, 0
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func sessionOf(t *testing.T, h http.Handler, c *http.Cookie) map[string]bool {
	t.Helper()
	var s map[string]bool
	if err := json.Unmarshal(req{method: "GET", path: "/api/session", cookie: c}.do(h).Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSetupOnFirstVisit(t *testing.T) {
	dir := tempDir(t)
	j := &Journal{}
	h := testPanel(NewAuth("", dir, j))

	if s := sessionOf(t, h, nil); !s["setup"] || s["valid"] {
		t.Fatalf("fresh panel must ask for a password: %v", s)
	}
	// Without a password nothing but the setup page works.
	if w := (req{method: "GET", path: "/api/overview"}).do(h); w.Code != http.StatusUnauthorized {
		t.Fatalf("no password yet, overview: %d", w.Code)
	}
	if w := (req{method: "GET", path: "/"}).do(h); w.Code != http.StatusSeeOther {
		t.Fatalf("no password yet, index: %d", w.Code)
	}
	if w := (req{method: "POST", path: "/api/login", body: `{"password":""}`}).do(h); w.Code != http.StatusConflict {
		t.Fatalf("login before setup: %d", w.Code)
	}
	if w := (req{method: "POST", path: "/api/setup", body: `{"password":"short"}`}).do(h); w.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d", w.Code)
	}
	w := req{method: "POST", path: "/api/setup", body: `{"password":"длинный пароль"}`}.do(h)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	c := w.Result().Cookies()[0]
	if w := (req{method: "GET", path: "/api/overview", cookie: c}).do(h); w.Code != http.StatusOK {
		t.Fatalf("session after setup: %d", w.Code)
	}
	if w := (req{method: "POST", path: "/api/setup", body: `{"password":"другой пароль"}`}).do(h); w.Code != http.StatusConflict {
		t.Fatalf("second setup must be refused: %d", w.Code)
	}
	if l := j.List(); len(l) != 1 || !strings.Contains(l[0].Text, "пароль") {
		t.Errorf("timeline: %+v", l)
	}

	// Only a hash is stored, and the session survives a panel restart.
	b, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if strings.Contains(string(b), "длинный") || strings.Contains(string(b), c.Value) {
		t.Fatalf("auth.json holds a secret in clear: %s", b)
	}
	h2 := testPanel(NewAuth("", dir, nil))
	if s := sessionOf(t, h2, c); s["setup"] || !s["valid"] {
		t.Fatalf("after restart: %v", s)
	}
	if w := (req{method: "POST", path: "/api/login", body: `{"password":"неверный"}`}).do(h2); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if w := (req{method: "POST", path: "/api/login", body: `{"password":"длинный пароль"}`}).do(h2); w.Code != http.StatusOK {
		t.Fatalf("right password: %d", w.Code)
	}

	// Logout ends the session for good.
	(req{method: "POST", path: "/api/logout", cookie: c}).do(h2)
	if s := sessionOf(t, testPanel(NewAuth("", dir, nil)), c); s["valid"] {
		t.Fatal("session survived logout")
	}

	// A password in .env wins and logs out the sessions of the old one.
	w = req{method: "POST", path: "/api/login", body: `{"password":"длинный пароль"}`}.do(h2)
	c = w.Result().Cookies()[0]
	h3 := testPanel(NewAuth("из env", dir, nil))
	if s := sessionOf(t, h3, c); s["valid"] {
		t.Fatal("sessions of the old password must end")
	}
	if w := (req{method: "POST", path: "/api/login", body: `{"password":"длинный пароль"}`}).do(h3); w.Code != http.StatusUnauthorized {
		t.Fatalf("stored password while .env has one: %d", w.Code)
	}

	// reset-password forgets the stored one.
	if err := resetPassword(dir); err != nil {
		t.Fatal(err)
	}
	if s := sessionOf(t, testPanel(NewAuth("", dir, nil)), nil); !s["setup"] {
		t.Fatal("after reset the panel must ask for a new password")
	}
}

// The first visitor picks the password, so setup is refused when the request
// looks like it came through a proxy: nginx by default turns Host into
// 127.0.0.1 and talks HTTP/1.0 to the backend.
func TestSetupRefusedThroughProxy(t *testing.T) {
	for name, q := range map[string]req{
		"nginx default":   {http10: true},
		"x-forwarded-for": {hdr: map[string]string{"X-Forwarded-For": "1.2.3.4"}},
		"forwarded":       {hdr: map[string]string{"Forwarded": "for=1.2.3.4"}},
		"via":             {hdr: map[string]string{"Via": "1.1 proxy"}},
		"cloudflare":      {hdr: map[string]string{"Cf-Connecting-Ip": "1.2.3.4"}},
	} {
		a := NewAuth("", "", nil)
		q.method, q.path, q.body = "POST", "/api/setup", `{"password":"длинный пароль"}`
		if w := q.do(testPanel(a)); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
		if a.HasPassword() {
			t.Errorf("%s: password was set", name)
		}
	}
}

func TestPublicPathsWithoutSession(t *testing.T) {
	h := testPanel(NewAuth("secret", "", nil))
	for _, p := range []string{"/api/session", "/api/health"} {
		if w := (req{method: "GET", path: p}).do(h); w.Code == http.StatusUnauthorized || w.Code == http.StatusSeeOther {
			t.Errorf("%s must be public: %d", p, w.Code)
		}
	}
}

// fakeDocker answers /containers/abc123/json with the given HostConfig.
func fakeDocker(t *testing.T, hostConfig string) *Docker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/abc123/json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"HostConfig":` + hostConfig + `}`))
	}))
	t.Cleanup(srv.Close)
	d, err := NewDocker("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExposure(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		hostConfig string
		listen     string
		exposed    bool
	}{
		{`{"PortBindings":{"9443/tcp":[{"HostIp":"127.0.0.1","HostPort":"9443"}]}}`, ":9443", false},
		{`{"PortBindings":{"9443/tcp":[{"HostIp":"::1","HostPort":"9443"}]}}`, ":9443", false},
		{`{"PortBindings":{"9443/tcp":[{"HostIp":"","HostPort":"9443"}]}}`, ":9443", true},
		{`{"PortBindings":{"9443/tcp":[{"HostIp":"0.0.0.0","HostPort":"9443"}]}}`, ":9443", true},
		{`{"NetworkMode":"host"}`, ":9443", true},
		{`{"NetworkMode":"host"}`, "127.0.0.1:9443", false},
	}
	for _, c := range cases {
		got, _ := exposure(ctx, fakeDocker(t, c.hostConfig), "abc123", c.listen)
		if got != c.exposed {
			t.Errorf("%s listen=%s: exposed=%v", c.hostConfig, c.listen, got)
		}
	}
	// Unknown container (not in Docker, or a wrong ID guess): judge by the
	// listen address, where ":9443" means every address.
	if got, _ := exposure(ctx, fakeDocker(t, `{}`), "other", ":9443"); !got {
		t.Error("unknown container with :9443 must count as exposed")
	}
	if got, _ := exposure(ctx, fakeDocker(t, `{}`), "", "127.0.0.1:9443"); got {
		t.Error("127.0.0.1 outside Docker is not exposed")
	}
}
