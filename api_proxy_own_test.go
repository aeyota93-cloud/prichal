package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPickProxy(t *testing.T) {
	own := ContainerSummary{ID: "own", Names: []string{"/prichal-proxy"}, State: "running"}
	amn := ContainerSummary{ID: "amn", Names: []string{"/amnezia-telemt"}, State: "running"}
	ownStopped := ContainerSummary{ID: "own", Names: []string{"/prichal-proxy"}, State: "exited"}
	other := ContainerSummary{ID: "x", Names: []string{"/n8n"}, State: "running"}
	cases := []struct {
		list []ContainerSummary
		want string
		id   string
	}{
		{[]ContainerSummary{amn, own}, "", "own"}, // own proxy first
		{[]ContainerSummary{amn, ownStopped}, "", "amn"},
		{[]ContainerSummary{amn, own}, "amnezia-telemt", "amn"},
		{[]ContainerSummary{amn}, "prichal-proxy", ""},
		{[]ContainerSummary{other, own}, "n8n", ""}, // never any other container
	}
	for _, c := range cases {
		got := pickProxy(c.list, c.want)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != c.id {
			t.Errorf("pickProxy(want %q) = %q, want %q", c.want, id, c.id)
		}
	}
}

func TestProxyDeleteRules(t *testing.T) {
	if _, _, err := proxyCall(proxyRequest{Action: "delete", Name: "main"}, []string{"main"}); err == nil || !strings.Contains(err.Error(), "последнюю") {
		t.Errorf("last link must stay: %v", err)
	}
	if _, _, err := proxyCall(proxyRequest{Container: ownProxyContainer, Action: "delete", Name: "amnezia"}, []string{"amnezia", "extra_1"}); err != nil {
		t.Errorf("moved «amnezia» link may be deleted from the own proxy: %v", err)
	}
	if _, _, err := proxyCall(proxyRequest{Container: telemtContainer, Action: "delete", Name: "amnezia"}, []string{"amnezia", "extra_1"}); err == nil {
		t.Error("Amnezia's main link must stay in Amnezia's proxy")
	}
}

func TestOwnProxyLabelFallsBackToAmnezia(t *testing.T) {
	w := &ConnWatcher{labels: &Labels{m: map[string]string{}}}
	_ = w.labels.Set(telemtContainer, "extra_1", "Мама")
	if got := w.label(ownProxyContainer, "extra_1"); got != "Мама" {
		t.Errorf("moved link label = %q", got)
	}
	_ = w.labels.Set(ownProxyContainer, "extra_1", "Папа")
	if got := w.label(ownProxyContainer, "extra_1"); got != "Папа" {
		t.Errorf("own label must win: %q", got)
	}
	if got := w.label(telemtContainer, "extra_2"); got != "" {
		t.Errorf("no fallback the other way: %q", got)
	}
}

func TestTelemtHTTP(t *testing.T) {
	t.Setenv("PROXY_API_TOKEN", "s3cret")
	var gotAuth, gotBody, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if gotAuth != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"username":"extra_1"}}`))
	}))
	defer srv.Close()
	old := ownProxyURL
	ownProxyURL = srv.URL
	defer func() { ownProxyURL = old }()

	raw, err := telemtHTTP(context.Background(), telemtCall{"PATCH", "/v1/users/extra_1", map[string]any{"max_unique_ips": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PATCH" || gotPath != "/v1/users/extra_1" || gotBody != `{"max_unique_ips":2}` {
		t.Errorf("request: %s %s %s", gotMethod, gotPath, gotBody)
	}
	if u, err := parseTelemtReply(raw); err != nil || u.Username != "extra_1" {
		t.Errorf("reply: %+v %v", u, err)
	}

	t.Setenv("PROXY_API_TOKEN", "wrong")
	if _, err := telemtHTTP(context.Background(), telemtCall{method: "GET", path: "/v1/users"}); err == nil || !strings.Contains(err.Error(), "пароль") {
		t.Errorf("wrong password must be explained: %v", err)
	}
}
