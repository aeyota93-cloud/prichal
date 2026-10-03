package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// A tiny client for the Docker Registry HTTP API v2 with anonymous token auth.
// It works with Docker Hub, GHCR, Quay, lscr.io and other public registries.
// HEAD requests are used to learn digests: Docker Hub does not count them
// against its pull limit.

type imageRef struct {
	Registry string // host to talk to, e.g. registry-1.docker.io
	Repo     string // e.g. library/nginx
	Tag      string
	Digest   string // set when the reference is pinned by digest
}

func parseRef(image string) imageRef {
	var r imageRef
	name := image
	if i := strings.Index(name, "@"); i >= 0 {
		r.Digest, name = name[i+1:], name[:i]
	}
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		r.Tag, name = name[i+1:], name[:i]
	}
	if r.Tag == "" {
		r.Tag = "latest"
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		r.Registry, r.Repo = parts[0], parts[1]
	} else {
		r.Registry, r.Repo = "docker.io", name
	}
	if r.Registry == "docker.io" || r.Registry == "index.docker.io" {
		r.Registry = "registry-1.docker.io"
		if !strings.Contains(r.Repo, "/") {
			r.Repo = "library/" + r.Repo
		}
	}
	return r
}

var versionTag = regexp.MustCompile(`^v?\d+(\.\d+)+([.-].*)?$`)

// pinned: the tag names a specific version, so a newer release will never
// arrive under it.
func (r imageRef) pinned() bool { return r.Digest != "" || versionTag.MatchString(r.Tag) }

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

type registry struct {
	http   *http.Client
	mu     sync.Mutex
	tokens map[string]string // "registry repo" -> bearer token
}

func (g *registry) getToken(key string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokens[key]
}

func (g *registry) setToken(key, t string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tokens[key] = t
}

func newRegistry() *registry {
	return &registry{http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: httpsOnly}, tokens: map[string]string{}}
}

// httpsOnly follows redirects like the default policy (up to 10), but never
// to a plain http address.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errors.New("реестр перенаправил на незащищённый адрес")
	}
	if len(via) >= 10 {
		return errors.New("слишком много перенаправлений")
	}
	return nil
}

var authParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// token performs the anonymous Bearer handshake described by WWW-Authenticate.
func (g *registry) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("реестр требует входа")
	}
	p := map[string]string{}
	for _, m := range authParam.FindAllStringSubmatch(challenge, -1) {
		p[strings.ToLower(m[1])] = m[2]
	}
	if p["realm"] == "" {
		return "", errors.New("реестр не сказал, где взять токен")
	}
	// The realm comes from the registry's answer: take it only as a plain
	// https address, so a broken or hostile reply cannot make the panel send
	// requests to http services of the server's own network.
	realm, err := url.Parse(p["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" {
		return "", errors.New("реестр указал неверный адрес для токена")
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	if p["scope"] != "" {
		q.Set("scope", p["scope"])
	}
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	if t.Token == "" {
		t.Token = t.AccessToken
	}
	if t.Token == "" {
		return "", errNotInRegistry
	}
	return t.Token, nil
}

func (g *registry) do(ctx context.Context, method string, r imageRef, path, accept string) (*http.Response, error) {
	u := "https://" + r.Registry + "/v2/" + r.Repo + path
	key := r.Registry + " " + r.Repo
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if t := g.getToken(key); t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		resp, err := g.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			t, err := g.token(ctx, resp.Header.Get("WWW-Authenticate"))
			if err != nil {
				return nil, err
			}
			g.setToken(key, t)
			continue
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
				return nil, errNotInRegistry
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				return nil, errors.New("реестр ограничил число запросов, попробуйте позже")
			}
			return nil, fmt.Errorf("реестр ответил %d", resp.StatusCode)
		}
		return resp, nil
	}
	return nil, errNotInRegistry
}

// errNotInRegistry: the registry does not know the image or wants a login.
// Usually the image was built on the server itself.
var errNotInRegistry = errors.New("образа нет в открытом реестре")

// Digest returns the digest the tag points to now.
func (g *registry) Digest(ctx context.Context, r imageRef) (string, error) {
	resp, err := g.do(ctx, http.MethodHead, r, "/manifests/"+r.Tag, manifestAccept)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if d := resp.Header.Get("Docker-Content-Digest"); d != "" {
		return d, nil
	}
	// Some registries omit the header on HEAD; hash the manifest instead.
	resp, err = g.do(ctx, http.MethodGet, r, "/manifests/"+r.Tag, manifestAccept)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(resp.Body, 4<<20)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Info reads the version label and build date of the image for this
// server's CPU architecture. Best effort: errors just leave fields empty.
func (g *registry) Info(ctx context.Context, r imageRef, ref string) (version, created string) {
	var m struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	get := func(path, accept string, v any) error {
		resp, err := g.do(ctx, http.MethodGet, r, path, accept)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
	}
	if get("/manifests/"+ref, manifestAccept, &m) != nil {
		return "", ""
	}
	if len(m.Manifests) > 0 {
		next := ""
		for _, mf := range m.Manifests {
			if mf.Platform.OS == "linux" && mf.Platform.Architecture == runtime.GOARCH {
				next = mf.Digest
				break
			}
		}
		if next == "" {
			return "", ""
		}
		m.Manifests = nil
		if get("/manifests/"+next, manifestAccept, &m) != nil {
			return "", ""
		}
	}
	if m.Config.Digest == "" {
		return "", ""
	}
	var cfg struct {
		Created string `json:"created"`
		Config  struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if get("/blobs/"+m.Config.Digest, "", &cfg) != nil {
		return "", ""
	}
	return cfg.Config.Labels["org.opencontainers.image.version"], cfg.Created
}
