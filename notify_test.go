package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testNotifier() (*Notifier, *Telegram) {
	tg := &Telegram{token: "123:SECRET", queue: make(chan string, 16)}
	hub := &TgHub{store: &tgStore{}, bot: tg}
	return NewNotifier(nil, hub, "/", nil, &Journal{}), tg
}

func ev(action, id, name string, attrs ...string) dockerEvent {
	e := dockerEvent{Action: action}
	e.Actor.ID = id
	e.Actor.Attributes = map[string]string{"name": name}
	for i := 0; i+1 < len(attrs); i += 2 {
		e.Actor.Attributes[attrs[i]] = attrs[i+1]
	}
	return e
}

func drain(tg *Telegram) []string {
	var out []string
	for {
		select {
		case m := <-tg.queue:
			out = append(out, m)
		default:
			return out
		}
	}
}

func TestManualStopIsQuiet(t *testing.T) {
	n, tg := testNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // follow-up checks return immediately
	n.handle(ctx, ev("kill", "c1", "n8n-n8n-1", "signal", "15"))
	n.handle(ctx, ev("die", "c1", "n8n-n8n-1", "exitCode", "0"))
	if msgs := drain(tg); len(msgs) != 0 {
		t.Errorf("manual stop must not notify, got %q", msgs)
	}
	if n.down["c1"] {
		t.Error("manual stop must not be remembered as a failure")
	}
}

func TestRestartLoop(t *testing.T) {
	n, tg := testNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 3; i++ {
		n.handle(ctx, ev("die", "c2", "amnezia-awg2", "exitCode", "1"))
	}
	msgs := drain(tg)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "снова и снова") || !strings.Contains(msgs[0], "AmneziaWG") {
		t.Errorf("want one restart-loop message, got %q", msgs)
	}
	if !n.down["c2"] {
		t.Error("crash must be remembered for the recovery message")
	}
}

func TestOOMAndUnhealthy(t *testing.T) {
	n, tg := testNotifier()
	ctx := context.Background()
	n.handle(ctx, ev("oom", "c3", "n8n-n8n-1"))
	n.handle(ctx, ev("health_status: unhealthy", "c3", "n8n-n8n-1"))
	n.handle(ctx, ev("health_status: unhealthy", "c3", "n8n-n8n-1")) // deduped
	msgs := drain(tg)
	if len(msgs) != 2 || !strings.Contains(msgs[0], "памяти") || !strings.Contains(msgs[1], "нездоров") {
		t.Errorf("got %q", msgs)
	}
}

func TestTokenRedacted(t *testing.T) {
	tg := &Telegram{token: "123:SECRET"}
	got := tg.redact(errors.New(`Post "https://api.telegram.org/bot123:SECRET/sendMessage": timeout`))
	if strings.Contains(got, "SECRET") {
		t.Errorf("token leaked: %s", got)
	}
}

func TestNotifierForgets(t *testing.T) {
	n, _ := testNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.handle(ctx, ev("kill", "gone", "x"))
	n.handle(ctx, ev("die", "gone2", "y", "exitCode", "1"))
	n.handle(ctx, ev("oom", "gone3", "z"))
	n.forget(time.Now())
	if len(n.lastKill) != 1 || len(n.crashes) != 1 || len(n.sent) != 1 {
		t.Fatalf("fresh entries must stay: %d %d %d", len(n.lastKill), len(n.crashes), len(n.sent))
	}
	n.forget(time.Now().Add(time.Hour))
	if len(n.lastKill)+len(n.crashes)+len(n.sent) != 0 {
		t.Errorf("old entries must go: %v %v %v", n.lastKill, n.crashes, n.sent)
	}
}
