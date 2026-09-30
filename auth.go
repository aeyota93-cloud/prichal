package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The panel controls Docker and the host, so it is root-equivalent. By
// default it listens on 127.0.0.1 and is reached through an SSH tunnel; then
// a password is optional. If its port is published on another address and no
// PRICHAL_PASSWORD is set, it refuses to work at all.

const (
	sessionCookie = "prichal_session"
	sessionTTL    = 30 * 24 * time.Hour
	loginWindow   = 5 * time.Minute
	loginMaxFails = 10
)

type Auth struct {
	password string

	mu       sync.Mutex
	sessions map[string]time.Time // token -> expiry
	fails    []time.Time
}

func NewAuth(password string) *Auth {
	return &Auth{password: password, sessions: map[string]time.Time{}}
}

func (a *Auth) Enabled() bool { return a.password != "" }

func (a *Auth) Valid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	return true
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не понял запрос"})
		return
	}
	now := time.Now()
	a.mu.Lock()
	recent := a.fails[:0]
	for _, t := range a.fails {
		if now.Sub(t) < loginWindow {
			recent = append(recent, t)
		}
	}
	a.fails = recent
	if len(a.fails) >= loginMaxFails {
		a.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "слишком много неудачных попыток, подождите 5 минут"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(in.Password), []byte(a.password)) != 1 {
		a.fails = append(a.fails, now)
		a.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "неверный пароль"})
		return
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	a.sessions[token] = now.Add(sessionTTL)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func isLoopback(host string) bool {
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// exposure tells whether the panel is reachable from outside the server and
// on which address. Inside a container it reads its own port bindings;
// otherwise it looks at the listen address.
func exposure(ctx context.Context, d *Docker, selfID, listen string) (bool, string) {
	if selfID != "" {
		var c struct {
			HostConfig struct {
				NetworkMode  string `json:"NetworkMode"`
				PortBindings map[string][]struct {
					HostIP   string `json:"HostIp"`
					HostPort string `json:"HostPort"`
				} `json:"PortBindings"`
			} `json:"HostConfig"`
		}
		if err := d.getJSON(ctx, "/containers/"+selfID+"/json", nil, &c); err == nil {
			if c.HostConfig.NetworkMode == "host" {
				h, _, _ := net.SplitHostPort(listen)
				return !isLoopback(h), listen
			}
			for _, bs := range c.HostConfig.PortBindings {
				for _, b := range bs {
					if !isLoopback(b.HostIP) {
						ip := b.HostIP
						if ip == "" {
							ip = "0.0.0.0"
						}
						return true, net.JoinHostPort(ip, b.HostPort)
					}
				}
			}
			return false, ""
		}
	}
	h, _, _ := net.SplitHostPort(listen)
	return !isLoopback(h), listen
}
