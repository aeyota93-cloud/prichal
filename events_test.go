package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestJournalOrderLimitAndPersistence(t *testing.T) {
	dir := tempDir(t)
	j := LoadJournal("")
	for i := 0; i < journalLen+5; i++ {
		j.Add("", "событие")
	}
	// One write is enough to check persistence (and Windows antivirus does
	// not get to hold dozens of temp files during cleanup).
	j.path = filepath.Join(dir, "events.json")
	j.Add("bad", "n8n упал")
	list := j.List()
	if len(list) != journalLen {
		t.Fatalf("want %d events, got %d", journalLen, len(list))
	}
	if list[0].Text != "n8n упал" || list[0].Tone != "bad" {
		t.Errorf("newest first, text as given, got %+v", list[0])
	}
	if again := LoadJournal(dir).List(); len(again) != journalLen || again[0].Text != "n8n упал" {
		t.Errorf("journal not restored from disk: %d events, first %+v", len(again), again)
	}
}

func TestNilJournalIsSafe(t *testing.T) {
	var j *Journal
	j.Add("", "ничего")
	if l := j.List(); len(l) != 0 {
		t.Errorf("got %v", l)
	}
}

func TestNotifierFillsJournal(t *testing.T) {
	n, _ := testNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 3; i++ {
		n.handle(ctx, ev("die", "c2", "amnezia-awg2", "exitCode", "1"))
	}
	n.handle(ctx, ev("kill", "c1", "n8n-n8n-1", "signal", "15"))
	n.handle(ctx, ev("die", "c1", "n8n-n8n-1", "exitCode", "0"))
	list := n.journal.List()
	if len(list) != 1 || list[0].Text != "AmneziaWG падает снова и снова" || list[0].Tone != "bad" {
		t.Errorf("want only the restart loop in the timeline, got %+v", list)
	}
}

func TestNotifierWithoutTelegram(t *testing.T) {
	n := NewNotifier(nil, NewTgHub("", "", ""), "/", nil, &Journal{})
	n.handle(context.Background(), ev("oom", "c3", "n8n-n8n-1", "image", "n8nio/n8n"))
	if l := n.journal.List(); len(l) != 1 || l[0].Text != "n8n: не хватило памяти" {
		t.Errorf("got %+v", l)
	}
}

func TestUpperFirst(t *testing.T) {
	if got := upperFirst("обновление Причал из git"); got != "Обновление Причал из git" {
		t.Errorf("got %q", got)
	}
	if got := upperFirst(""); got != "" {
		t.Errorf("got %q", got)
	}
}
