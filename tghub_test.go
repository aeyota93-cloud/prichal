package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// None of these tests may reach the real api.telegram.org: every hub that
// starts a bot gets its api pointed at fakeTelegram first.

const (
	goodToken  = "111111111:AAGoodTokenGoodTokenGoodTokenGoodToken"
	otherToken = "222222222:AAOtherTokenOtherTokenOtherTokenOther"
	badToken   = "333333333:AABadTokenBadTokenBadTokenBadTokenBad"
)

type fakeTG struct {
	*httptest.Server
	mu   sync.Mutex
	sent []string
}

func (f *fakeTG) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// fakeTelegram answers getMe for goodToken and otherToken, 401 for the rest,
// holds getUpdates open like a long poll and records sendMessage texts.
func fakeTelegram(t *testing.T) *fakeTG {
	f := &fakeTG{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		token, method := parts[0], parts[1]
		r.ParseForm() // reading the body lets the server notice a client that went away
		if token != goodToken && token != otherToken {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		switch method {
		case "getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"prichal_test_bot"}}`))
		case "getUpdates":
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
			w.Write([]byte(`{"ok":true,"result":[]}`))
		case "sendMessage":
			f.mu.Lock()
			f.sent = append(f.sent, r.Form.Get("text"))
			f.mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func newHub(t *testing.T, dir, envToken, envUser string, f *fakeTG) *TgHub {
	h := NewTgHub(dir, envToken, envUser)
	if f != nil {
		h.api = f.URL
	}
	return h
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTgPrecedence(t *testing.T) {
	t.Run("nothing set", func(t *testing.T) {
		st := NewTgHub(tempDir(t), "", "").Status()
		if st.State != "off" || st.Source != "env" {
			t.Errorf("got %+v", st)
		}
	})
	t.Run("env only", func(t *testing.T) {
		st := NewTgHub(tempDir(t), goodToken, "@Alice_01 ").Status()
		if st.State != "waiting" || st.Source != "env" || st.User != "Alice_01" {
			t.Errorf("got %+v", st)
		}
	})
	t.Run("env with a chat bound before the update", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "telegram.json"), `{"chatId": 42, "kernel": "6.8.0"}`)
		h := NewTgHub(dir, goodToken, "alice01")
		if st := h.Status(); st.State != "on" || st.Source != "env" {
			t.Errorf("got %+v", st)
		}
		if got := h.SwapKernel("6.8.1"); got != "6.8.0" {
			t.Errorf("kernel not kept, previous %q", got)
		}
	})
	t.Run("file beats env", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "telegram-config.json"), `{"token":"`+otherToken+`","username":"bob_the_user","bot":"b_bot"}`)
		st := NewTgHub(dir, goodToken, "alice01").Status()
		if st.State != "waiting" || st.Source != "panel" || st.User != "bob_the_user" || st.Bot != "b_bot" {
			t.Errorf("got %+v", st)
		}
	})
	t.Run("off beats env", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "telegram-config.json"), `{"off":true}`)
		h := NewTgHub(dir, goodToken, "alice01")
		if st := h.Status(); st.State != "off" || st.Source != "panel" || st.User != "" {
			t.Errorf("got %+v", st)
		}
		h.Start(context.Background(), func() {}) // must not start a bot (no api set: a real call would fail the test run)
		if h.current() != nil {
			t.Error("bot started although the file says off")
		}
	})
	t.Run("broken file falls back to env", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "telegram-config.json"), `{not json`)
		if st := NewTgHub(dir, goodToken, "alice01").Status(); st.Source != "env" || st.State != "waiting" {
			t.Errorf("got %+v", st)
		}
	})
}

func TestTgStatusHasNoToken(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "telegram-config.json"), `{"token":"`+otherToken+`","username":"bob_the_user","bot":"b_bot"}`)
	for _, h := range []*TgHub{
		NewTgHub(dir, goodToken, "alice01"),
		NewTgHub(tempDir(t), goodToken, "alice01"),
	} {
		b, _ := json.Marshal(h.Status())
		for _, tok := range []string{goodToken, otherToken, strings.Split(goodToken, ":")[1], strings.Split(otherToken, ":")[1]} {
			if strings.Contains(string(b), tok) {
				t.Errorf("token in status: %s", b)
			}
		}
	}
	// The overview shows no source.
	st := NewTgHub(tempDir(t), goodToken, "alice01").Status()
	st.Source = ""
	if b, _ := json.Marshal(st); strings.Contains(string(b), "source") {
		t.Errorf("source in overview: %s", b)
	}
}

func TestTgChangeResetsChat(t *testing.T) {
	f := fakeTelegram(t)
	dir := tempDir(t)
	h := newHub(t, dir, "", "", f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Start(ctx, func() {})
	apply := func(token, user string) {
		t.Helper()
		if err := h.Apply(TgConfig{Token: token, Username: user, Bot: "prichal_test_bot"}); err != nil {
			t.Fatal(err)
		}
	}
	bound := func() { h.store.setChat(42) }

	apply(goodToken, "alice01")
	bound()
	if st := h.Status(); st.State != "on" {
		t.Fatalf("got %+v", st)
	}
	apply(goodToken, "@ALICE01") // the same person, nothing changes
	if st := h.Status(); st.State != "on" {
		t.Errorf("same nick must keep the chat, got %+v", st)
	}
	apply(goodToken, "bob_the_user")
	if st := h.Status(); st.State != "waiting" || st.User != "bob_the_user" {
		t.Errorf("new nick must drop the chat, got %+v", st)
	}
	bound()
	apply(otherToken, "bob_the_user")
	if st := h.Status(); st.State != "waiting" {
		t.Errorf("new token must drop the chat, got %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "telegram.json")); !strings.Contains(string(b), `"chatId": 0`) {
		t.Errorf("telegram.json not reset: %s", b)
	}
}

func TestTgApplyStopsOldBot(t *testing.T) {
	f := fakeTelegram(t)
	h := newHub(t, tempDir(t), "", "", f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Start(ctx, func() {})
	if h.current() != nil {
		t.Fatal("no bot expected before the first Apply")
	}
	runCtx := func() context.Context {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.runCtx
	}
	if err := h.Apply(TgConfig{Token: goodToken, Username: "alice01"}); err != nil {
		t.Fatal(err)
	}
	first := runCtx()
	if first == nil || first.Err() != nil {
		t.Fatal("first bot is not running")
	}
	if err := h.Apply(TgConfig{Token: otherToken, Username: "alice01"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old bot was not stopped")
	}
	second := runCtx()
	if second == nil || second.Err() != nil {
		t.Fatal("new bot is not running")
	}
	if err := h.Disable(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Disable did not stop the bot")
	}
	h.Send("nobody reads this") // off: must not panic
	if h.DigestDue(time.Now().Add(-time.Hour)) {
		t.Error("digest must not be due while the bot is off")
	}
}

func TestTgOffKeepsKernelTracking(t *testing.T) {
	dir := tempDir(t)
	h := NewTgHub(dir, "", "")
	if prev := h.SwapKernel("6.8.0"); prev != "" {
		t.Errorf("got %q", prev)
	}
	if prev := NewTgHub(dir, "", "").SwapKernel("6.8.1"); prev != "6.8.0" {
		t.Errorf("kernel not kept across restarts while off, got %q", prev)
	}
}

func TestTgEnvBotNameLooked(t *testing.T) {
	f := fakeTelegram(t)
	h := newHub(t, tempDir(t), goodToken, "alice01", f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Start(ctx, func() {})
	deadline := time.Now().Add(2 * time.Second)
	for h.Status().Bot == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := h.Status(); st.Bot != "prichal_test_bot" || st.Source != "env" {
		t.Errorf("got %+v", st)
	}
}

func TestRedactEmptyToken(t *testing.T) {
	tg := &Telegram{}
	if got := tg.redact(os.ErrNotExist); got != os.ErrNotExist.Error() {
		t.Errorf("empty token must leave the text alone, got %q", got)
	}
}

// ---------- HTTP ----------

func tgServer(t *testing.T, dir string, f *fakeTG) (*server, context.CancelFunc) {
	h := newHub(t, dir, "", "", f)
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx, func() {})
	return &server{tg: h, journal: &Journal{}}, cancel
}

func post(h http.HandlerFunc, body string) (int, string) {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))
	return rec.Code, rec.Body.String()
}

func TestTelegramSave(t *testing.T) {
	f := fakeTelegram(t)
	dir := tempDir(t)
	s, stop := tgServer(t, dir, f)
	defer stop()
	var all strings.Builder // every answer, to look for the token

	check := func(name, body string, wantCode int, wantIn string) string {
		t.Helper()
		code, out := post(s.telegramSave, body)
		all.WriteString(out)
		if code != wantCode || !strings.Contains(out, wantIn) {
			t.Errorf("%s: want %d with %q, got %d %s", name, wantCode, wantIn, code, out)
		}
		return out
	}
	check("bad nick", `{"token":"`+goodToken+`","username":"ab"}`, 400, "Ник в Telegram")
	check("no token", `{"username":"alice01"}`, 400, "Вставьте токен бота")
	check("not a token", `{"token":"hello","username":"alice01"}`, 400, "Это не похоже на токен")
	check("telegram says no", `{"token":"`+badToken+`","username":"alice01"}`, 400, "Telegram не принял токен")
	if _, err := os.Stat(filepath.Join(dir, "telegram-config.json")); err == nil {
		t.Error("a rejected token must not be saved")
	}

	out := check("good", `{"token":" `+goodToken+` \n`+`","username":"@Alice01"}`, 200, `"state":"waiting"`)
	for _, want := range []string{`"user":"Alice01"`, `"bot":"prichal_test_bot"`, `"source":"panel"`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %s in %s", want, out)
		}
	}
	path := filepath.Join(dir, "telegram-config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved TgConfig
	if json.Unmarshal(b, &saved) != nil || saved.Token != goodToken || saved.Username != "Alice01" || saved.Bot != "prichal_test_bot" {
		t.Errorf("config file: %s", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("config file mode %v, want 0600", fi.Mode().Perm())
		}
	}

	// Change only the nick: empty token keeps the old one.
	s.tg.store.setChat(42)
	out = check("only nick", `{"token":"","username":"bob_the_user"}`, 200, `"user":"bob_the_user"`)
	if !strings.Contains(out, `"state":"waiting"`) {
		t.Errorf("new nick needs a new Start: %s", out)
	}
	if s.tg.token() != goodToken {
		t.Error("the old token was lost")
	}

	// Test message: refused until the chat is bound, then sent at once.
	code, out := post(s.telegramTest, "")
	all.WriteString(out)
	if code != 409 || !strings.Contains(out, "нажмите Start") {
		t.Errorf("test while waiting: %d %s", code, out)
	}
	s.tg.store.setChat(42)
	if code, out = post(s.telegramTest, ""); code != 200 {
		t.Errorf("test: %d %s", code, out)
	}
	if m := f.messages(); len(m) != 1 || !strings.Contains(m[0], "Причал на месте") {
		t.Errorf("sent: %q", m)
	}

	// Off.
	code, out = post(s.telegramOff, "")
	all.WriteString(out)
	if code != 200 || !strings.Contains(out, `"state":"off"`) {
		t.Errorf("off: %d %s", code, out)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "token") || !strings.Contains(string(b), `"off": true`) {
		t.Errorf("off must wipe the token: %s", b)
	}

	// GET answers the same way.
	rec := httptest.NewRecorder()
	s.telegramStatus(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	all.WriteString(rec.Body.String())

	var events strings.Builder
	for _, e := range s.journal.List() {
		events.WriteString(e.Text + "\n")
	}
	for _, want := range []string{"подключены: @Alice01", "изменены: @bob_the_user", "отключены"} {
		if !strings.Contains(events.String(), want) {
			t.Errorf("timeline lacks %q: %s", want, events.String())
		}
	}
	leak := all.String() + events.String()
	for _, tok := range []string{goodToken, strings.Split(goodToken, ":")[1], strings.Split(badToken, ":")[1]} {
		if strings.Contains(leak, tok) {
			t.Errorf("token leaked into answers or the timeline: %s", leak)
		}
	}
}

func TestTelegramSaveNetworkError(t *testing.T) {
	f := fakeTelegram(t)
	f.Close() // refused connections put the URL, token included, into the error
	s, stop := tgServer(t, tempDir(t), nil)
	defer stop()
	s.tg.api = f.URL
	code, out := post(s.telegramSave, `{"token":"`+goodToken+`","username":"alice01"}`)
	if code != 502 || !strings.Contains(out, "Не удалось связаться с Telegram") {
		t.Errorf("got %d %s", code, out)
	}
	if strings.Contains(out, strings.Split(goodToken, ":")[1]) {
		t.Errorf("token leaked: %s", out)
	}
}
