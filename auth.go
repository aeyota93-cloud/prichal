package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The panel controls Docker and the host, so it is root-equivalent and always
// asks for a password. The owner picks it on the first visit (through the SSH
// tunnel) and the panel keeps only its hash in DATA_DIR/auth.json, or it is
// given as PRICHAL_PASSWORD in .env, which then wins. Until a password
// exists the panel serves nothing but the page that sets it.
//
// If its port is published on another address, or ALLOWED_HOSTS is set, and
// there is no password yet, it refuses to work at all: the first visitor
// from the internet must not be the one who picks the password.

const (
	sessionCookie = "prichal_session"
	sessionTTL    = 30 * 24 * time.Hour
	loginWindow   = 5 * time.Minute
	loginMaxFails = 10
	minPassword   = 8
	maxPassword   = 256
)

// pbkdf2Iter is the PBKDF2-SHA256 work factor (OWASP 2023). Tests lower it.
var pbkdf2Iter = 600_000

type Auth struct {
	env     string   // PRICHAL_PASSWORD; wins over the stored one
	path    string   // DATA_DIR/auth.json; "" keeps everything in memory
	journal *Journal // "password set" goes to the timeline

	mu    sync.Mutex
	st    authState
	fails []time.Time
}

type authState struct {
	Hash string `json:"hash,omitempty"` // hex PBKDF2-SHA256 of the password
	Salt string `json:"salt,omitempty"`
	Iter int    `json:"iter,omitempty"`
	// Sessions survive panel restarts (updates rebuild it). Only hashes of
	// the tokens are stored, and they belong to one password: For tells
	// which, so a new password logs everybody out.
	For      string           `json:"for,omitempty"`
	Sessions map[string]int64 `json:"sessions,omitempty"` // sha256(token) -> expiry, unix
}

func NewAuth(envPassword, dataDir string, journal *Journal) *Auth {
	a := &Auth{env: envPassword, journal: journal}
	if dataDir != "" {
		a.path = filepath.Join(dataDir, "auth.json")
		if b, err := os.ReadFile(a.path); err == nil {
			if err := json.Unmarshal(b, &a.st); err != nil {
				log.Printf("auth: %v", err)
			}
		}
	}
	if a.st.For != a.passwordID() {
		a.st.Sessions = nil
	}
	return a
}

// HasPassword: a password is set, in .env or on the first visit.
func (a *Auth) HasPassword() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hasPasswordLocked()
}

func (a *Auth) hasPasswordLocked() bool { return a.env != "" || a.st.Hash != "" }

// passwordID names the current password without revealing it.
func (a *Auth) passwordID() string {
	src := a.st.Hash
	if a.env != "" {
		src = "env:" + a.env
	}
	if src == "" {
		return ""
	}
	h := sha256.Sum256([]byte(src))
	return hex.EncodeToString(h[:8])
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (a *Auth) saveLocked() {
	if a.path == "" {
		return
	}
	if err := writeJSONAtomic(a.path, a.st); err != nil {
		log.Printf("auth: %v", err)
	}
}

func (a *Auth) Valid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.hasPasswordLocked() {
		return false
	}
	exp, ok := a.st.Sessions[hashToken(c.Value)]
	return ok && time.Now().Unix() < exp
}

// checkLocked compares a password with the current one.
func (a *Auth) checkLocked(pw string) bool {
	if a.env != "" {
		return subtle.ConstantTimeCompare([]byte(pw), []byte(a.env)) == 1
	}
	if a.st.Hash == "" {
		return false
	}
	salt, err1 := hex.DecodeString(a.st.Salt)
	want, err2 := hex.DecodeString(a.st.Hash)
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, a.st.Iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// newSessionLocked creates a session and returns its token.
func (a *Auth) newSessionLocked(now time.Time) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	if a.st.Sessions == nil {
		a.st.Sessions = map[string]int64{}
	}
	for k, exp := range a.st.Sessions {
		if exp <= now.Unix() {
			delete(a.st.Sessions, k)
		}
	}
	a.st.Sessions[hashToken(token)] = now.Add(sessionTTL).Unix()
	a.st.For = a.passwordID()
	a.saveLocked()
	return token
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSession(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r),
	})
}

// tooManyFailsLocked drops old failures and says whether logins are paused.
func (a *Auth) tooManyFailsLocked(now time.Time) bool {
	recent := a.fails[:0]
	for _, t := range a.fails {
		if now.Sub(t) < loginWindow {
			recent = append(recent, t)
		}
	}
	a.fails = recent
	return len(a.fails) >= loginMaxFails
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	now := time.Now()
	a.mu.Lock()
	if !a.hasPasswordLocked() {
		a.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "пароль ещё не задан, обновите страницу"})
		return
	}
	if a.tooManyFailsLocked(now) {
		a.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "слишком много неудачных попыток, подождите 5 минут"})
		return
	}
	if !a.checkLocked(in.Password) {
		a.fails = append(a.fails, now)
		a.mu.Unlock()
		time.Sleep(500 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "неверный пароль"})
		return
	}
	token := a.newSessionLocked(now)
	a.mu.Unlock()
	setSession(w, r, token)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// viaProxy reports signs that a request came through a reverse proxy or a
// tunnel service rather than straight through the SSH tunnel. Nginx, for
// one, talks HTTP/1.0 to the backend and replaces Host with the backend's
// address unless told otherwise.
func viaProxy(r *http.Request) bool {
	if r.ProtoMajor == 1 && r.ProtoMinor == 0 {
		return true
	}
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Via", "Cf-Connecting-Ip"} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

var errPasswordSet = errors.New("пароль уже задан, войдите с ним")

// setup sets the password on the first visit.
func (a *Auth) setup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	h := strings.ToLower(r.Host)
	if i := strings.LastIndex(h, ":"); i > strings.LastIndex(h, "]") {
		h = h[:i]
	}
	if !isLoopback(h) || viaProxy(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "пароль можно задать только через SSH-туннель (http://localhost). Или впишите PRICHAL_PASSWORD в .env на сервере"})
		return
	}
	if n := utf8.RuneCountInString(in.Password); n < minPassword || len(in.Password) > maxPassword {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пароль должен быть не короче 8 символов"})
		return
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, in.Password, salt, pbkdf2Iter, 32)
	if err != nil {
		writeErr(w, err)
		return
	}
	now := time.Now()
	a.mu.Lock()
	if a.hasPasswordLocked() {
		a.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": errPasswordSet.Error()})
		return
	}
	a.st = authState{Hash: hex.EncodeToString(key), Salt: hex.EncodeToString(salt), Iter: pbkdf2Iter}
	token := a.newSessionLocked(now)
	a.mu.Unlock()
	log.Printf("auth: password set")
	a.journal.Add("ok", "Задан пароль для входа в панель")
	setSession(w, r, token)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		if _, ok := a.st.Sessions[hashToken(c.Value)]; ok {
			delete(a.st.Sessions, hashToken(c.Value))
			a.saveLocked()
		}
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// session tells the login page what to show.
func (a *Auth) session(w http.ResponseWriter, r *http.Request) {
	has := a.HasPassword()
	writeJSON(w, http.StatusOK, map[string]bool{"auth": true, "setup": !has, "valid": has && a.Valid(r)})
}

// resetPassword is `prichal reset-password`: forgets the stored password and
// all sessions, so the next visit sets a new one. A PRICHAL_PASSWORD in .env
// is not touched.
func resetPassword(dataDir string) error {
	if dataDir == "" {
		return errors.New("DATA_DIR не задан")
	}
	err := os.Remove(filepath.Join(dataDir, "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
