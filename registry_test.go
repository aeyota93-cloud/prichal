package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// A malformed or plain-http realm must come back as an error. Before, the
// request was built without checking, Do(nil) panicked in a background
// goroutine and took the whole panel down; an http realm let the registry
// make the panel call services of its own network.
func TestRegistryBadRealm(t *testing.T) {
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"token":"t0k"}`))
	}))
	defer internal.Close()

	for _, realm := range []string{"https://bad%zz/token", internal.URL + "/token", "/token", "ftp://x/token", "https:///token"} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		g := newRegistry()
		g.http = srv.Client()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := g.Digest(ctx, parseRef(strings.TrimPrefix(srv.URL, "https://")+"/team/app:latest"))
		cancel()
		srv.Close()
		if err == nil || errors.Is(err, errNotInRegistry) {
			t.Errorf("realm %q: want a clear error, got %v", realm, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the panel called the internal http service %d times", n)
	}
}

// A redirect to plain http is refused, https ones are followed.
func TestRegistryRedirectPolicy(t *testing.T) {
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer internal.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			return
		}
		to := internal.URL
		if r.URL.Path == "/secure" {
			to = "/ok"
		}
		http.Redirect(w, r, to, http.StatusFound)
	}))
	defer srv.Close()
	c := srv.Client()
	c.CheckRedirect = httpsOnly
	if resp, err := c.Get(srv.URL + "/secure"); err != nil {
		t.Errorf("https redirect refused: %v", err)
	} else {
		resp.Body.Close()
	}
	if resp, err := c.Get(srv.URL + "/plain"); err == nil {
		resp.Body.Close()
		t.Error("redirect to http was followed")
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the http service was called %d times", n)
	}
}
