package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// lockReason says why the panel must refuse to work, or "" if it may. It is
// root-equivalent, and without a password the first visitor would pick one,
// so that is only allowed on 127.0.0.1. ALLOWED_HOSTS means a reverse proxy
// publishes it under a domain, where the 127.0.0.1 binding no longer
// protects it.
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
	"/api/health": true, "/api/session": true, "/api/login": true, "/api/setup": true,
}

// guard blocks everything that does not come from the panel's own page:
// other sites in the same browser (CSRF), DNS-rebinding tricks, and visitors
// without a session. POST additionally needs a custom header, which a
// foreign page cannot send without a CORS preflight that is never approved.
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
				"  2) уберите ALLOWED_HOSTS, привяжите порт к 127.0.0.1 (\"127.0.0.1:9443:9443\"),\n" +
				"     зайдите через SSH-туннель и придумайте пароль на первой странице.\n"))
			return
		}
		if !publicPaths[r.URL.Path] && !strings.HasPrefix(r.URL.Path, "/fonts/") && !s.auth.Valid(r) {
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

// readJSON decodes a request body of at most limit bytes into v. On failure
// it answers 400 itself and returns false.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не понял запрос"})
		return false
	}
	return true
}

func ctxTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
