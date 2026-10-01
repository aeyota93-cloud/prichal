package main

import (
	"net/http"
	"net/http/httptest"
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

// A label is not only for proxy links: clients of AmneziaWG and OpenVPN get
// one too, by the client's name in Amnezia.
func TestLabelsForVPNClients(t *testing.T) {
	dir := t.TempDir()
	s := &server{conns: &ConnWatcher{labels: LoadLabels(dir)}}
	for _, c := range []struct{ container, name, label string }{
		{"amnezia-awg2", "Admin [iOS 27.2]", "Мой ноутбук"},
		{"amnezia-wireguard", "Phone", "Телефон мамы"},
		{"amnezia-openvpn", "Laptop", "Рабочий"},
		{"amnezia-telemt", "extra_3", "Мама"},
	} {
		body := `{"container":"` + c.container + `","name":"` + c.name + `","label":"` + c.label + `"}`
		rec := httptest.NewRecorder()
		s.setLabel(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.container, rec.Code, rec.Body)
		}
		if got := LoadLabels(dir).Get(c.container, c.name); got != c.label {
			t.Errorf("%s/%s after reload: got %q, want %q", c.container, c.name, got, c.label)
		}
	}
	// Still only for Amnezia services the panel knows how to ask.
	rec := httptest.NewRecorder()
	s.setLabel(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"container":"nginx","name":"x","label":"y"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown container: %d", rec.Code)
	}
}
