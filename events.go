package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Journal keeps the last things that happened on the server (crashes,
// reboots, actions from the panel, finished updates) for the timeline on the
// Overview page. It survives panel restarts when DATA_DIR is set.
type Journal struct {
	mu   sync.Mutex
	path string
	list []Event // oldest first
}

type Event struct {
	T    int64  `json:"t"`
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"` // "ok", "warn", "bad" or empty
}

const journalLen = 30

func LoadJournal(dataDir string) *Journal {
	j := &Journal{}
	if dataDir != "" {
		j.path = filepath.Join(dataDir, "events.json")
		if b, err := os.ReadFile(j.path); err == nil {
			_ = json.Unmarshal(b, &j.list)
		}
	}
	return j
}

// Add records an event: a short plain sentence, already capitalised.
func (j *Journal) Add(tone, text string) {
	if j == nil || text == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.list = append(j.list, Event{T: time.Now().Unix(), Text: text, Tone: tone})
	if len(j.list) > journalLen {
		j.list = j.list[len(j.list)-journalLen:]
	}
	if j.path == "" {
		return
	}
	_ = writeJSONAtomic(j.path, j.list)
}

// List returns the events newest first.
func (j *Journal) List() []Event {
	out := []Event{}
	if j == nil {
		return out
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.list) - 1; i >= 0; i-- {
		out = append(out, j.list[i])
	}
	return out
}

func upperFirst(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}
