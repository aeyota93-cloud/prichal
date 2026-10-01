package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// tgState is the content of telegram.json.
type tgState struct {
	ChatID     int64  `json:"chatId"`
	Kernel     string `json:"kernel,omitempty"`
	LastDigest int64  `json:"lastDigest,omitempty"`
}

// tgStore is what must survive a bot being switched off or replaced: the bound
// chat, the last seen kernel and the last digest. It is the old
// telegram.json, same file and same fields, so a server configured through
// .env keeps its bound chat after the update.
type tgStore struct {
	mu    sync.Mutex
	path  string
	state tgState
}

func loadTgStore(dataDir string) *tgStore {
	st := &tgStore{}
	if dataDir != "" {
		st.path = filepath.Join(dataDir, "telegram.json")
		if b, err := os.ReadFile(st.path); err == nil {
			_ = json.Unmarshal(b, &st.state)
		}
	}
	return st
}

func (st *tgStore) saveLocked() {
	if st.path == "" {
		return
	}
	_ = writeJSONAtomic(st.path, st.state)
}

func (st *tgStore) chat() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.state.ChatID
}

func (st *tgStore) setChat(id int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.state.ChatID != id {
		st.state.ChatID = id
		st.saveLocked()
	}
}

// bind remembers the chat unless the bot that found it was stopped already.
// The check is under the lock: TgHub cancels the old bot first and resets the
// chat after, so a stale bot cannot bind the chat of its replacement.
func (st *tgStore) bind(ctx context.Context, id int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	st.state.ChatID = id
	st.saveLocked()
	return true
}

// swapKernel stores the running kernel version and returns the previous one.
func (st *tgStore) swapKernel(k string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	prev := st.state.Kernel
	if prev != k {
		st.state.Kernel = k
		st.saveLocked()
	}
	return prev
}

// digestDue reports whether the weekly digest for the period starting at
// `since` has not been sent yet, and marks it as sent.
func (st *tgStore) digestDue(since time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.state.LastDigest >= since.Unix() {
		return false
	}
	st.state.LastDigest = time.Now().Unix()
	st.saveLocked()
	return true
}

// TgConfig is DATA_DIR/telegram-config.json, written from the panel.
type TgConfig struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"` // without @
	Bot      string `json:"bot,omitempty"`      // the bot's own username, from getMe
	Off      bool   `json:"off,omitempty"`
}

func (c TgConfig) active() bool { return !c.Off && c.Token != "" && c.Username != "" }

// cleanUsername drops spaces and a leading @.
func cleanUsername(s string) string {
	return strings.TrimPrefix(strings.Join(strings.Fields(s), ""), "@")
}

// TgStatus is what the panel may know about the bot. The token is never in it.
type TgStatus struct {
	State  string `json:"state"` // "off", "waiting" (no Start yet) or "on"
	User   string `json:"user"`
	Bot    string `json:"bot"`
	Source string `json:"source,omitempty"` // "panel" or "env"
}

// TgHub owns the bot and can switch it on, off or to another token without
// restarting the panel. It is never nil: with the bot off, Send does nothing
// and DigestDue says no, while the kernel is still tracked.
type TgHub struct {
	dir   string // DATA_DIR; empty: settings live in memory only (development)
	api   string // Bot API address, empty: the real one (tests set it)
	store *tgStore
	env   TgConfig // TG_TOKEN and TG_USERNAME

	mu      sync.Mutex
	cfg     TgConfig // the effective settings
	source  string   // where cfg came from: "panel" or "env"
	ctx     context.Context
	onBound func()
	bot     *Telegram
	runCtx  context.Context // context of the running bot
	cancel  context.CancelFunc
}

// NewTgHub reads the settings. Precedence:
//   - no telegram-config.json: TG_TOKEN and TG_USERNAME from .env, as before;
//   - the file exists: the file wins and .env is ignored. "off": true switches
//     the bot off even if .env still has a token.
//
// So a bot set up through .env keeps working after an update, and after the
// first save from the panel everything is managed there.
func NewTgHub(dataDir, envToken, envUser string) *TgHub {
	h := &TgHub{
		dir:   dataDir,
		store: loadTgStore(dataDir),
		env:   TgConfig{Token: strings.TrimSpace(envToken), Username: cleanUsername(envUser)},
	}
	h.cfg, h.source = h.env, "env"
	if h.dir != "" {
		b, err := os.ReadFile(h.configPath())
		switch {
		case err == nil:
			var c TgConfig
			if json.Unmarshal(b, &c) == nil {
				c.Username = cleanUsername(c.Username)
				h.cfg, h.source = c, "panel"
			} else {
				log.Printf("telegram: telegram-config.json не читается, беру TG_TOKEN/TG_USERNAME из .env")
			}
		case !errors.Is(err, os.ErrNotExist):
			log.Printf("telegram: %v", err)
		}
	}
	return h
}

func (h *TgHub) configPath() string { return filepath.Join(h.dir, "telegram-config.json") }

func (h *TgHub) saveConfig(c TgConfig) error {
	if h.dir == "" {
		return nil
	}
	return writeJSONAtomic(h.configPath(), c)
}

// Start runs the bot, if one is configured, until ctx ends.
func (h *TgHub) Start(ctx context.Context, onBound func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctx, h.onBound = ctx, onBound
	h.startLocked()
}

func (h *TgHub) startLocked() {
	if h.ctx == nil || !h.cfg.active() {
		return
	}
	h.bot = newTelegram(h.cfg.Token, h.cfg.Username, h.api, h.store)
	h.runCtx, h.cancel = context.WithCancel(h.ctx)
	go h.bot.Run(h.runCtx, h.onBound)
	if h.cfg.Bot == "" {
		go h.lookupBot(h.runCtx, h.cfg.Token) // settings from .env: no bot name yet
	}
}

// stopLocked cancels the running bot; its goroutines end on their own.
func (h *TgHub) stopLocked() {
	if h.cancel != nil {
		h.cancel()
	}
	h.bot, h.runCtx, h.cancel = nil, nil, nil
}

func (h *TgHub) lookupBot(ctx context.Context, token string) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	name, err := h.probe(token).getMe(cctx)
	if err != nil {
		log.Printf("telegram: %v", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg.Token == token && h.cfg.Bot == "" {
		h.cfg.Bot = name
	}
}

// probe is a throwaway client for one request with a candidate token.
func (h *TgHub) probe(token string) *Telegram {
	t := newTelegram(token, "", h.api, nil)
	t.http.Timeout = 10 * time.Second
	return t
}

// Check asks Telegram who the token belongs to and returns the bot's own
// username. A *tgError means Telegram answered; anything else is the network.
func (h *TgHub) Check(ctx context.Context, token string) (string, error) {
	return h.probe(token).getMe(ctx)
}

// Apply saves new settings and switches the bot to them. A new token or a new
// username needs a new binding, so the chat is forgotten.
func (h *TgHub) Apply(cfg TgConfig) error {
	cfg.Token, cfg.Username, cfg.Off = strings.TrimSpace(cfg.Token), cleanUsername(cfg.Username), false
	h.mu.Lock()
	defer h.mu.Unlock()
	same := h.cfg.active() && h.cfg.Token == cfg.Token && strings.EqualFold(h.cfg.Username, cfg.Username)
	if err := h.saveConfig(cfg); err != nil {
		return err
	}
	h.cfg, h.source = cfg, "panel"
	if same && (h.bot != nil || h.ctx == nil) {
		return nil // nothing to switch, the bot keeps its queue and chat
	}
	h.stopLocked()
	h.store.setChat(0)
	h.startLocked()
	return nil
}

// Disable switches the bot off and forgets the token, the username and the chat.
func (h *TgHub) Disable() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.saveConfig(TgConfig{Off: true}); err != nil {
		return err
	}
	h.stopLocked()
	h.store.setChat(0)
	h.cfg, h.source = TgConfig{Off: true}, "panel"
	return nil
}

// Status never contains the token.
func (h *TgHub) Status() TgStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := TgStatus{State: "off", Source: h.source}
	if h.cfg.active() {
		st.State, st.User, st.Bot = "waiting", h.cfg.Username, h.cfg.Bot
		if h.store.chat() != 0 {
			st.State = "on"
		}
	}
	return st
}

// token is the current token, for "change only the username".
func (h *TgHub) token() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg.active() {
		return h.cfg.Token
	}
	return ""
}

func (h *TgHub) current() *Telegram {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bot
}

// Send queues a message; with the bot off it does nothing.
func (h *TgHub) Send(text string) { h.current().Send(text) }

// SendNow sends at once, not through the queue. It fails if no chat is bound.
func (h *TgHub) SendNow(ctx context.Context, text string) error {
	bot := h.current()
	if bot == nil || bot.chat() == 0 {
		return errors.New("бот не привязан")
	}
	return bot.sendNow(ctx, text)
}

// SwapKernel works with the bot off too.
func (h *TgHub) SwapKernel(k string) string { return h.store.swapKernel(k) }

// DigestDue is always false while the bot is off.
func (h *TgHub) DigestDue(since time.Time) bool {
	if h.current() == nil {
		return false
	}
	return h.store.digestDue(since)
}
