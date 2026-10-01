package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Telegram sends notifications to one person. The chat is bound the first
// time that person (TG_USERNAME) presses Start in the bot; everyone else is
// ignored. The token never appears in logs.
type Telegram struct {
	token    string
	username string // without @, lower case
	path     string // state file (bound chat, last seen kernel)
	http     *http.Client

	mu    sync.Mutex
	state tgState
	queue chan string
}

type tgState struct {
	ChatID     int64  `json:"chatId"`
	Kernel     string `json:"kernel,omitempty"`
	LastDigest int64  `json:"lastDigest,omitempty"`
}

// DigestDue reports whether the weekly digest for the period starting at
// `since` has not been sent yet, and marks it as sent.
func (t *Telegram) DigestDue(since time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.LastDigest >= since.Unix() {
		return false
	}
	t.state.LastDigest = time.Now().Unix()
	t.saveLocked()
	return true
}

func NewTelegram(token, username, dataDir string) *Telegram {
	if token == "" || username == "" {
		return nil
	}
	t := &Telegram{
		token:    token,
		username: strings.ToLower(strings.TrimPrefix(username, "@")),
		http:     &http.Client{Timeout: 70 * time.Second},
		queue:    make(chan string, 64),
	}
	if dataDir != "" {
		t.path = filepath.Join(dataDir, "telegram.json")
		if b, err := os.ReadFile(t.path); err == nil {
			_ = json.Unmarshal(b, &t.state)
		}
	}
	return t
}

func (t *Telegram) saveLocked() {
	if t.path == "" {
		return
	}
	_ = writeJSONAtomic(t.path, t.state)
}

func (t *Telegram) chat() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state.ChatID
}

// SwapKernel stores the running kernel version and returns the previous one.
func (t *Telegram) SwapKernel(k string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.state.Kernel
	if prev != k {
		t.state.Kernel = k
		t.saveLocked()
	}
	return prev
}

// redact keeps the bot token out of error messages (net/http puts the URL,
// token included, into its errors).
func (t *Telegram) redact(err error) string {
	return strings.ReplaceAll(err.Error(), t.token, "<token>")
}

func (t *Telegram) call(ctx context.Context, method string, params url.Values, out any) error {
	u := "https://api.telegram.org/bot" + t.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(params.Encode()))
	if err != nil {
		return errors.New(t.redact(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.http.Do(req)
	if err != nil {
		return errors.New(t.redact(err))
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: %d", method, resp.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Send queues a message (HTML). Messages wait until the chat is bound and are
// retried while the network is down.
func (t *Telegram) Send(text string) {
	if t == nil {
		return
	}
	select {
	case t.queue <- text:
	default:
		log.Printf("telegram: queue full, dropped a message")
	}
}

func (t *Telegram) Run(ctx context.Context, onBound func()) {
	go t.bind(ctx, onBound)
	for {
		select {
		case <-ctx.Done():
			return
		case text := <-t.queue:
			for {
				id := t.chat()
				if id != 0 {
					err := t.call(ctx, "sendMessage", url.Values{
						"chat_id":                  {fmt.Sprint(id)},
						"text":                     {text},
						"parse_mode":               {"HTML"},
						"disable_web_page_preview": {"true"},
					}, nil)
					if err == nil {
						break
					}
					log.Printf("telegram: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(30 * time.Second):
				}
			}
		}
	}
}

// bind waits for the owner to press Start, then remembers the chat.
func (t *Telegram) bind(ctx context.Context, onBound func()) {
	if t.chat() != 0 {
		return
	}
	log.Printf("telegram: waiting for @%s to press Start in the bot", t.username)
	var offset int64
	for ctx.Err() == nil {
		var updates []struct {
			UpdateID int64 `json:"update_id"`
			Message  *struct {
				Chat struct {
					ID   int64  `json:"id"`
					Type string `json:"type"`
				} `json:"chat"`
				From struct {
					Username string `json:"username"`
				} `json:"from"`
			} `json:"message"`
		}
		err := t.call(ctx, "getUpdates", url.Values{
			"offset":          {fmt.Sprint(offset)},
			"timeout":         {"50"},
			"allowed_updates": {`["message"]`},
		}, &updates)
		if err != nil {
			log.Printf("telegram: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			m := u.Message
			if m == nil || m.Chat.Type != "private" || strings.ToLower(m.From.Username) != t.username {
				continue
			}
			t.mu.Lock()
			t.state.ChatID = m.Chat.ID
			t.saveLocked()
			t.mu.Unlock()
			// Confirm the offset so Telegram forgets the handled updates.
			_ = t.call(ctx, "getUpdates", url.Values{"offset": {fmt.Sprint(offset)}, "timeout": {"0"}}, nil)
			log.Printf("telegram: bound to @%s", t.username)
			onBound()
			return
		}
	}
}
