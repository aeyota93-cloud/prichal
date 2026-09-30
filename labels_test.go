package main

import (
	"strings"
	"testing"
)

func TestLabelsPersist(t *testing.T) {
	dir := t.TempDir()
	l := LoadLabels(dir)
	if err := l.Set("amnezia-telemt", "extra_3", "  Мама   телефон "); err != nil {
		t.Fatal(err)
	}
	if got := LoadLabels(dir).Get("amnezia-telemt", "extra_3"); got != "Мама телефон" {
		t.Errorf("after reload got %q", got)
	}
	if err := l.Set("amnezia-telemt", "extra_3", ""); err != nil {
		t.Fatal(err)
	}
	if got := LoadLabels(dir).Get("amnezia-telemt", "extra_3"); got != "" {
		t.Errorf("empty label must remove, got %q", got)
	}
	if err := l.Set("amnezia-telemt", "extra_1", strings.Repeat("я", 41)); err == nil {
		t.Error("want error for a label over 40 characters")
	}
}
