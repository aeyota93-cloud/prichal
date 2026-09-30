package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Labels are the owner's own names for clients, e.g. "Мама" for proxy link
// extra_3. Kept in a small JSON file on a volume; without DATA_DIR they live
// only in memory (development).
type Labels struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

const maxLabel = 40

func LoadLabels(dir string) *Labels {
	l := &Labels{m: map[string]string{}}
	if dir == "" {
		return l
	}
	l.path = filepath.Join(dir, "labels.json")
	b, err := os.ReadFile(l.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("labels: %v", err)
		}
		return l
	}
	if err := json.Unmarshal(b, &l.m); err != nil {
		log.Printf("labels: %v", err)
		l.m = map[string]string{}
	}
	return l
}

func labelKey(container, name string) string { return container + "/" + name }

func (l *Labels) Get(container, name string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m[labelKey(container, name)]
}

// Set stores a label; an empty label removes it.
func (l *Labels) Set(container, name, label string) error {
	label = strings.Join(strings.Fields(label), " ")
	if utf8.RuneCountInString(label) > maxLabel {
		return errors.New("подпись длиннее 40 символов")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if label == "" {
		delete(l.m, labelKey(container, name))
	} else {
		l.m[labelKey(container, name)] = label
	}
	if l.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(l.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
