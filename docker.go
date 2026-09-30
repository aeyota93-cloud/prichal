package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Docker is a minimal client for the Docker Engine API over a unix socket
// (or a TCP address, which is handy for development through an SSH forward).
type Docker struct {
	http *http.Client
	base string
}

func NewDocker(host string) (*Docker, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	var dial func(ctx context.Context, _, _ string) (net.Conn, error)
	switch u.Scheme {
	case "unix":
		path := u.Path
		dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
	case "tcp":
		addr := u.Host
		dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}
	default:
		return nil, fmt.Errorf("unsupported DOCKER_HOST %q", host)
	}
	return &Docker{
		http: &http.Client{Transport: &http.Transport{DialContext: dial, MaxIdleConns: 8}},
		base: "http://docker",
	}, nil
}

// APIError carries Docker's own message and HTTP status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker %d: %s", e.Status, e.Message) }

func (d *Docker) do(ctx context.Context, method, path string, q url.Values) (*http.Response, error) {
	return d.doBody(ctx, method, path, q, nil)
}

// doBody is do with an optional JSON request body.
func (d *Docker) doBody(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	u := d.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var m struct{ Message string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m)
		return nil, &APIError{Status: resp.StatusCode, Message: m.Message}
	}
	return resp, nil
}

func (d *Docker) getJSON(ctx context.Context, path string, q url.Values, v any) error {
	resp, err := d.do(ctx, http.MethodGet, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// ---------- Containers ----------

type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

func (c ContainerSummary) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func (d *Docker) Containers(ctx context.Context) ([]ContainerSummary, error) {
	var out []ContainerSummary
	err := d.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &out)
	return out, err
}

type ContainerInspect struct {
	ID           string `json:"Id"`
	Image        string `json:"Image"` // image ID the container runs
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				End      string `json:"End"`
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Tty bool `json:"Tty"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		Memory int64 `json:"Memory"`
	} `json:"HostConfig"`
}

func (d *Docker) Inspect(ctx context.Context, id string) (*ContainerInspect, error) {
	var out ContainerInspect
	if err := d.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type StatsSample struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// MemUsed matches what `docker stats` shows: usage minus reclaimable page cache.
func (s *StatsSample) MemUsed() uint64 {
	cache := s.MemoryStats.Stats["inactive_file"]
	if cache == 0 {
		cache = s.MemoryStats.Stats["total_inactive_file"]
	}
	if cache > s.MemoryStats.Usage {
		return s.MemoryStats.Usage
	}
	return s.MemoryStats.Usage - cache
}

func (d *Docker) Stats(ctx context.Context, id string) (*StatsSample, error) {
	var out StatsSample
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	if err := d.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/stats", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Action runs start/stop/restart/kill/pause/unpause. "Already in that state"
// (304) counts as success.
func (d *Docker) Action(ctx context.Context, id, action string) error {
	q := url.Values{}
	switch action {
	case "stop", "restart":
		q.Set("t", "10")
	case "kill":
		q.Set("signal", "SIGKILL")
	case "start", "pause", "unpause":
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	resp, err := d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/"+action, q)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------- Logs ----------

type LogLine struct {
	Time   string `json:"time"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (d *Docker) Logs(ctx context.Context, id string, tail int, tty bool) ([]LogLine, error) {
	q := url.Values{
		"stdout":     {"1"},
		"stderr":     {"1"},
		"timestamps": {"1"},
		"tail":       {fmt.Sprint(tail)},
	}
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, 8<<20)

	var lines []LogLine
	add := func(stream, chunk string) {
		for _, raw := range strings.Split(strings.TrimRight(chunk, "\n"), "\n") {
			raw = strings.TrimRight(raw, "\r")
			ts, text, ok := strings.Cut(raw, " ")
			if !ok {
				ts, text = "", raw
			}
			lines = append(lines, LogLine{Time: ts, Stream: stream, Text: ansiRe.ReplaceAllString(text, "")})
		}
	}

	if tty {
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			add("stdout", sc.Text())
		}
		return lines, sc.Err()
	}

	err = demux(body, func(stream byte, chunk []byte) {
		name := "stdout"
		if stream == 2 {
			name = "stderr"
		}
		add(name, string(chunk))
	})
	return lines, err
}

// demux reads Docker's multiplexed stream: frames with an 8-byte header
// [stream, 0, 0, 0, size (4 bytes, big-endian)] followed by the payload.
func demux(r io.Reader, fn func(stream byte, chunk []byte)) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		buf := make([]byte, binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil
		}
		fn(hdr[0], buf)
	}
}

// Exec runs a command inside a running container and returns its stdout.
// Used only with fixed read-only commands (see connections.go).
func (d *Docker) Exec(ctx context.Context, id string, cmd []string) ([]byte, error) {
	var created struct {
		ID string `json:"Id"`
	}
	resp, err := d.doBody(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/exec", nil,
		map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": cmd})
	if err != nil {
		return nil, err
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp, err = d.doBody(ctx, http.MethodPost, "/exec/"+url.PathEscape(created.ID)+"/start", nil,
		map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	err = demux(io.LimitReader(resp.Body, 4<<20), func(stream byte, chunk []byte) {
		if stream == 1 {
			out.Write(chunk)
		}
	})
	return out.Bytes(), err
}

// ---------- Images ----------

type ImageSummary struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     int64    `json:"Created"`
	Size        int64    `json:"Size"`
}

func (d *Docker) Images(ctx context.Context) ([]ImageSummary, error) {
	var out []ImageSummary
	err := d.getJSON(ctx, "/images/json", nil, &out)
	return out, err
}

type DiskUsage struct {
	BuildCache []struct {
		Size  int64 `json:"Size"`
		InUse bool  `json:"InUse"`
	} `json:"BuildCache"`
}

func (d *Docker) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	var out DiskUsage
	err := d.getJSON(ctx, "/system/df", url.Values{"type": {"build-cache"}}, &out)
	return &out, err
}

func (d *Docker) RemoveImage(ctx context.Context, id string) error {
	resp, err := d.do(ctx, http.MethodDelete, "/images/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Prune removes every image not used by any container (running or stopped)
// plus the unused build cache. Returns bytes reclaimed.
func (d *Docker) Prune(ctx context.Context) (int64, error) {
	var total int64

	var img struct{ SpaceReclaimed int64 }
	resp, err := d.do(ctx, http.MethodPost, "/images/prune", url.Values{"filters": {`{"dangling":["false"]}`}})
	if err != nil {
		return 0, err
	}
	_ = json.NewDecoder(resp.Body).Decode(&img)
	resp.Body.Close()
	total += img.SpaceReclaimed

	var bc struct{ SpaceReclaimed int64 }
	resp, err = d.do(ctx, http.MethodPost, "/build/prune", nil)
	if err != nil {
		return total, err
	}
	_ = json.NewDecoder(resp.Body).Decode(&bc)
	resp.Body.Close()
	total += bc.SpaceReclaimed

	return total, nil
}
