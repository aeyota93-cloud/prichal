package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Telegram sends notifications to one person. The chat is bound the first
// time that person (the configured username) presses Start in the bot;
// everyone else is ignored. The token never appears in logs. The bot itself
// is started and replaced by TgHub; what must outlive it (bound chat, last
// seen kernel, last digest) lives in tgStore.
type Telegram struct {
	token    string
	username string // without @, lower case
	api      string // Bot API address; empty: the real one (tests point it elsewhere)
	store    *tgStore
	http     *http.Client

	queue chan string
}

const telegramAPI = "https://api.telegram.org"

func newTelegram(token, username, api string, store *tgStore) *Telegram {
	if api == "" {
		api = telegramAPI
	}
	return &Telegram{
		token:    token,
		username: strings.ToLower(strings.TrimPrefix(username, "@")),
		api:      api,
		store:    store,
		http:     &http.Client{Timeout: 70 * time.Second},
		queue:    make(chan string, 64),
	}
}

func (t *Telegram) chat() int64 { return t.store.chat() }

// redact keeps the bot token out of error messages (net/http puts the URL,
// token included, into its errors).
func (t *Telegram) redact(err error) string {
	if t.token == "" {
		return err.Error() // ReplaceAll with "" would insert text between every character
	}
	return strings.ReplaceAll(err.Error(), t.token, "<token>")
}

// tgError is an answer of the Bot API that says no. Status is Telegram's own
// error code (401 for a wrong token, 404 for a malformed one).
type tgError struct {
	Method string
	Status int
	Desc   string
}

func (e *tgError) Error() string {
	if e.Desc == "" {
		return fmt.Sprintf("telegram %s: %d", e.Method, e.Status)
	}
	return fmt.Sprintf("telegram %s: %s", e.Method, e.Desc)
}

func (t *Telegram) call(ctx context.Context, method string, params url.Values, out any) error {
	api := t.api
	if api == "" {
		api = telegramAPI
	}
	u := api + "/bot" + t.token + "/" + method
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
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return &tgError{Method: method, Status: resp.StatusCode}
	}
	if !r.OK {
		code := r.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &tgError{Method: method, Status: code, Desc: r.Description}
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

// sendNow delivers text to the bound chat right away, bypassing the queue.
func (t *Telegram) sendNow(ctx context.Context, text string) error {
	return t.call(ctx, "sendMessage", url.Values{
		"chat_id":                  {fmt.Sprint(t.chat())},
		"text":                     {text},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}, nil)
}

// getMe returns the bot's own username; it is also the cheapest way to learn
// whether a token works.
func (t *Telegram) getMe(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := t.call(ctx, "getMe", nil, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

func (t *Telegram) Run(ctx context.Context, onBound func()) {
	go t.bind(ctx, onBound)
	for {
		select {
		case <-ctx.Done():
			return
		case text := <-t.queue:
			for {
				if t.chat() != 0 {
					err := t.sendNow(ctx, text)
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
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			m := u.Message
			if m == nil || m.Chat.Type != "private" || strings.ToLower(m.From.Username) != t.username {
				continue
			}
			if !t.store.bind(ctx, m.Chat.ID) {
				return // this bot was replaced meanwhile
			}
			// Confirm the offset so Telegram forgets the handled updates.
			_ = t.call(ctx, "getUpdates", url.Values{"offset": {fmt.Sprint(offset)}, "timeout": {"0"}}, nil)
			log.Printf("telegram: bound to @%s", t.username)
			onBound()
			return
		}
	}
}
