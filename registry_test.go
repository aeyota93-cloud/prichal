package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A fake registry with anonymous Bearer auth, like Docker Hub and GHCR.
func fakeRegistry(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.Write([]byte(`{"token":"t0k"}`))
		case r.Header.Get("Authorization") != "Bearer t0k":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake",scope="repository:app:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v2/team/app/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:new")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

func TestRegistryDigestWithToken(t *testing.T) {
	srv := fakeRegistry(t)
	defer srv.Close()
	g := newRegistry()
	g.http = srv.Client()
	host := strings.TrimPrefix(srv.URL, "https://")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := g.Digest(ctx, parseRef(host+"/team/app:latest"))
	if err != nil || d != "sha256:new" {
		t.Fatalf("got %q, %v", d, err)
	}
	// The token is cached and reused for the second call.
	if d, err := g.Digest(ctx, parseRef(host+"/team/app:latest")); err != nil || d != "sha256:new" {
		t.Fatalf("second call: %q, %v", d, err)
	}
	if _, err := g.Digest(ctx, parseRef(host+"/team/missing:latest")); !errors.Is(err, errNotInRegistry) {
		t.Errorf("missing image: want errNotInRegistry, got %v", err)
	}
}
