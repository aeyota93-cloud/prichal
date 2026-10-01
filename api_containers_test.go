package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dockerWith serves the few Engine API calls the handlers make; the Docker
// client reaches it over its tcp:// mode.
func dockerWith(t *testing.T, h http.HandlerFunc) *Docker {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	d, err := NewDocker("tcp://" + srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const testContainerID = "0123456789abcdef0123456789abcdef"

func TestFailedActionGoesToJournal(t *testing.T) {
	d := dockerWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			w.Write([]byte(`[{"Id":"` + testContainerID + `","Names":["/amnezia-awg2"],"Image":"amnezia-awg2","State":"running"}]`))
		case strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"cannot stop container:\n  boom"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	s := &server{docker: d, col: NewCollector(d, "", "/", ""), journal: &Journal{}}

	do := func(act string) int {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.SetPathValue("id", testContainerID)
		req.SetPathValue("action", act)
		rec := httptest.NewRecorder()
		s.action(rec, req)
		return rec.Code
	}
	if code := do("stop"); code != http.StatusInternalServerError {
		t.Fatalf("stop: status %d", code)
	}
	list := s.journal.List()
	want := "Не получилось остановить AmneziaWG: cannot stop container: boom"
	if len(list) != 1 || list[0].Text != want || list[0].Tone != "bad" {
		t.Fatalf("want one red line %q, got %+v", want, list)
	}
	if code := do("start"); code != http.StatusOK {
		t.Fatalf("start: status %d", code)
	}
	if list = s.journal.List(); len(list) != 2 || list[0].Text != "AmneziaWG запущен из панели" || list[0].Tone != "" {
		t.Errorf("success keeps its plain line, got %+v", list)
	}
}

func TestFailedImageRemovalAndPruneGoToJournal(t *testing.T) {
	buildPruneFails := false
	d := dockerWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/images/json":
			w.Write([]byte(`[{"Id":"sha256:abcdef0123456789","RepoTags":["old/app:1"],"Size":100}]`))
		case r.URL.Path == "/containers/json":
			w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message":"image is referenced in multiple repositories"}`))
		case r.URL.Path == "/build/prune" && buildPruneFails:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"disk on fire"}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	s := &server{docker: d, journal: &Journal{}}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.SetPathValue("id", "sha256:abcdef0123456789")
	s.removeImage(httptest.NewRecorder(), req)
	if l := s.journal.List(); len(l) != 1 || l[0].Tone != "bad" ||
		l[0].Text != "Не получилось удалить образ old/app:1: image is referenced in multiple repositories" {
		t.Errorf("remove: %+v", l)
	}

	s.prune(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if l := s.journal.List(); len(l) != 2 || l[0].Tone != "bad" ||
		!strings.HasPrefix(l[0].Text, "Очистка удалила не всё: не получилось удалить 1 образ: image is referenced") {
		t.Errorf("prune with a stuck image: %+v", l)
	}

	buildPruneFails = true
	s.prune(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if l := s.journal.List(); len(l) != 3 || l[0].Text != "Очистка не удалась: disk on fire" {
		t.Errorf("prune failure: %+v", l)
	}
}

func TestShortErr(t *testing.T) {
	long := errors.New(strings.Repeat("я", 300))
	got := shortErr(long)
	if r := []rune(got); len(r) != 121 || !strings.HasSuffix(got, "…") {
		t.Errorf("want 120 runes and an ellipsis, got %d runes", len(r))
	}
	if got := shortErr(&APIError{Status: 409, Message: "no\nway"}); got != "no way" {
		t.Errorf("got %q", got)
	}
	if got := shortErr(errors.New("plain")); got != "plain" {
		t.Errorf("got %q", got)
	}
}
