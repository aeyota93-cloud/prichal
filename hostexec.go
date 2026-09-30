package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Host commands. The panel lives in a container, but apt, systemctl and
// docker compose must run on the server itself. For that it starts a short
// helper container (alpine) that enters the host's namespaces with nsenter,
// the same trick `docker run --pid=host --privileged` tools use. Only the
// fixed commands in updates.go go through here.

const (
	helperImage  = "alpine:latest"
	helperPrefix = "prichal-host-"
)

func isHelper(name string) bool { return strings.HasPrefix(name, helperPrefix) }

func (d *Docker) ensureImage(ctx context.Context, ref string) error {
	resp, err := d.do(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil)
	if err == nil {
		resp.Body.Close()
		return nil
	}
	repo, tag, _ := strings.Cut(ref, ":")
	resp, err = d.do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body) // the pull finishes when the stream ends
	return err
}

func helperBody(cmd []string) map[string]any {
	return map[string]any{
		"Image":  helperImage,
		"Cmd":    append([]string{"nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--"}, cmd...),
		"Labels": map[string]string{"prichal.helper": "1"},
		"HostConfig": map[string]any{
			"Privileged":  true,
			"PidMode":     "host",
			"NetworkMode": "none",
		},
	}
}

// HostStart starts cmd on the host in a helper container named name and
// returns at once; the container stays until RemoveContainer.
func (d *Docker) HostStart(ctx context.Context, name string, cmd ...string) error {
	if err := d.ensureImage(ctx, helperImage); err != nil {
		return fmt.Errorf("не удалось подготовить вспомогательный образ: %w", err)
	}
	d.RemoveContainer(ctx, name) // a leftover from an earlier run
	resp, err := d.doBody(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, helperBody(cmd))
	if err != nil {
		return err
	}
	resp.Body.Close()
	resp, err = d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (d *Docker) Exists(ctx context.Context, name string) bool {
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func (d *Docker) RemoveContainer(ctx context.Context, name string) {
	if r, err := d.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name), url.Values{"force": {"1"}}); err == nil {
		r.Body.Close()
	}
}

// HostRun runs cmd on the host and returns its combined output and exit code.
func (d *Docker) HostRun(ctx context.Context, cmd ...string) (string, int, error) {
	if err := d.ensureImage(ctx, helperImage); err != nil {
		return "", -1, fmt.Errorf("не удалось подготовить вспомогательный образ: %w", err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := helperPrefix + hex.EncodeToString(suffix)
	body := helperBody(cmd)
	resp, err := d.doBody(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, body)
	if err != nil {
		return "", -1, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if err != nil {
		return "", -1, err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if r, err := d.do(rctx, http.MethodDelete, "/containers/"+created.ID, url.Values{"force": {"1"}}); err == nil {
			r.Body.Close()
		}
	}()

	if r, err := d.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil); err != nil {
		return "", -1, err
	} else {
		r.Body.Close()
	}
	resp, err = d.do(ctx, http.MethodPost, "/containers/"+created.ID+"/wait", nil)
	if err != nil {
		return "", -1, err
	}
	var waited struct{ StatusCode int }
	err = json.NewDecoder(resp.Body).Decode(&waited)
	resp.Body.Close()
	if err != nil {
		return "", -1, err
	}
	resp, err = d.do(ctx, http.MethodGet, "/containers/"+created.ID+"/logs", url.Values{"stdout": {"1"}, "stderr": {"1"}})
	if err != nil {
		return "", waited.StatusCode, err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_ = demux(io.LimitReader(resp.Body, 8<<20), func(_ byte, chunk []byte) { out.Write(chunk) })
	return out.String(), waited.StatusCode, nil
}
