package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	historyLen   = 40 // points kept per container (~2 min at 3 s)
	minInterval  = 2500 * time.Millisecond
	historyReset = 30 * time.Second // a gap this long means nobody watched; start over
)

type Point struct {
	T   int64   `json:"t"`
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
}

type ContainerView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	Identity
	Project       string   `json:"project,omitempty"` // docker compose project
	State         string   `json:"state"`
	Health        string   `json:"health"`
	HealthOutput  string   `json:"healthOutput,omitempty"`
	StartedAt     string   `json:"startedAt"`
	FinishedAt    string   `json:"finishedAt"`
	ExitCode      int      `json:"exitCode"`
	OOMKilled     bool     `json:"oomKilled"`
	RestartCount  int      `json:"restartCount"`
	RestartPolicy string   `json:"restartPolicy"`
	Ports         []string `json:"ports"`
	CPU           *float64 `json:"cpu"`
	Mem           uint64   `json:"mem"`
	MemLimit      uint64   `json:"memLimit"`
	History       []Point  `json:"history"`
	Self          bool     `json:"self"`
	ManualStop    bool     `json:"manualStop"`
	tty           bool
}

type HostView struct {
	CPU       *float64 `json:"cpu"`
	MemTotal  uint64   `json:"memTotal"`
	MemUsed   uint64   `json:"memUsed"`
	SwapTotal uint64   `json:"swapTotal"`
	SwapUsed  uint64   `json:"swapUsed"`
	DiskTotal uint64   `json:"diskTotal"`
	DiskUsed  uint64   `json:"diskUsed"`
	Uptime    int64    `json:"uptime"`
	Load      string   `json:"load"`
	CPUs      int      `json:"cpus"`
}

type Overview struct {
	Label      string          `json:"label"`
	Host       *HostView       `json:"host"`
	Containers []ContainerView `json:"containers"`
	Updated    int64           `json:"updated"`
}

type cpuPrev struct{ total, system uint64 }

// Collector samples Docker only while someone is looking at the panel:
// every request refreshes the data at most once per minInterval.
type Collector struct {
	docker   *Docker
	selfID   string
	hostRoot string
	label    string

	mu       sync.Mutex
	last     time.Time
	snap     *Overview
	prevMu   sync.Mutex
	prev     map[string]cpuPrev
	history  map[string][]Point
	hostPrev [2]uint64 // busy, total jiffies
	tty      map[string]bool
	manual   map[string]bool // stopped through this panel, so "exited" is not a crash
}

func NewCollector(d *Docker, selfID, hostRoot, label string) *Collector {
	return &Collector{
		docker: d, selfID: selfID, hostRoot: hostRoot, label: label,
		prev: map[string]cpuPrev{}, history: map[string][]Point{}, tty: map[string]bool{},
		manual: map[string]bool{},
	}
}

// Remember reports a finished action so a manual stop is not shown as a crash.
func (c *Collector) Remember(id, action string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case "stop", "kill":
		c.manual[id] = true
	case "start", "restart":
		delete(c.manual, id)
	}
	c.last = time.Time{}
}

func (c *Collector) IsTTY(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tty[id]
}

func (c *Collector) Overview(ctx context.Context) (*Overview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && time.Since(c.last) < minInterval {
		return c.snap, nil
	}
	snap, err := c.collect(ctx)
	if err != nil {
		return nil, err
	}
	c.snap, c.last = snap, time.Now()
	return snap, nil
}

func (c *Collector) collect(ctx context.Context) (*Overview, error) {
	all, err := c.docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	list := all[:0:0]
	for _, s := range all {
		if !isHelper(s.Name()) { // short-lived helpers of the Updates tab
			list = append(list, s)
		}
	}
	now := time.Now()
	if c.snap != nil && now.Sub(time.UnixMilli(c.snap.Updated)) > historyReset {
		c.history = map[string][]Point{}
		c.prev = map[string]cpuPrev{}
	}

	views := make([]ContainerView, len(list))
	var wg sync.WaitGroup
	for i, s := range list {
		wg.Add(1)
		go func(i int, s ContainerSummary) {
			defer wg.Done()
			v := ContainerView{ID: s.ID, Name: s.Name(), Image: s.Image, State: s.State, Ports: []string{}}
			v.Self = isSelf(c.selfID, s.ID)
			v.Identity = identify(v.Name, v.Image, s.Labels["com.docker.compose.service"], v.Self)
			v.Project = s.Labels["com.docker.compose.project"]
			seen := map[string]bool{}
			for _, p := range s.Ports {
				if p.PublicPort == 0 {
					continue
				}
				ip := p.IP
				if ip == "0.0.0.0" || ip == "::" || ip == "" {
					ip = "все адреса"
				}
				str := fmt.Sprintf("%d/%s · %s", p.PublicPort, p.Type, ip)
				if !seen[str] {
					seen[str] = true
					v.Ports = append(v.Ports, str)
				}
			}
			if in, err := c.docker.Inspect(ctx, s.ID); err == nil {
				v.StartedAt, v.FinishedAt = in.State.StartedAt, in.State.FinishedAt
				v.ExitCode, v.OOMKilled = in.State.ExitCode, in.State.OOMKilled
				v.RestartCount, v.RestartPolicy = in.RestartCount, in.HostConfig.RestartPolicy.Name
				v.tty = in.Config.Tty
				if h := in.State.Health; h != nil {
					v.Health = h.Status
					if n := len(h.Log); n > 0 && h.Status != "healthy" {
						v.HealthOutput = strings.TrimSpace(h.Log[n-1].Output)
					}
				}
			}
			if s.State == "running" || s.State == "paused" {
				if st, err := c.docker.Stats(ctx, s.ID); err == nil {
					v.Mem, v.MemLimit = st.MemUsed(), st.MemoryStats.Limit
					v.CPU = c.cpuPercent(s.ID, st)
				}
			}
			views[i] = v
		}(i, s)
	}
	wg.Wait()

	// History is updated after the parallel part so the maps stay single-writer.
	alive := map[string]bool{}
	for i := range views {
		v := &views[i]
		alive[v.ID] = true
		c.tty[v.ID] = v.tty
		v.ManualStop = c.manual[v.ID]
		if v.CPU != nil {
			h := append(c.history[v.ID], Point{T: now.UnixMilli(), CPU: *v.CPU, Mem: v.Mem})
			if len(h) > historyLen {
				h = h[len(h)-historyLen:]
			}
			c.history[v.ID] = h
		} else if v.State != "running" {
			delete(c.history, v.ID)
		}
		v.History = c.history[v.ID]
		if v.History == nil {
			v.History = []Point{}
		}
	}
	for id := range c.history {
		if !alive[id] {
			delete(c.history, id)
			delete(c.prev, id)
			delete(c.tty, id)
		}
	}
	for id := range c.manual {
		if !alive[id] {
			delete(c.manual, id)
		}
	}

	sort.SliceStable(views, func(a, b int) bool { return views[a].Name < views[b].Name })

	return &Overview{Label: c.label, Host: c.host(), Containers: views, Updated: now.UnixMilli()}, nil
}

// cpuPercent computes usage between this sample and the previous one, the same
// way `docker stats` does: share of total host CPU time, scaled by CPU count.
// Called from the per-container goroutines, hence its own lock.
func (c *Collector) cpuPercent(id string, s *StatsSample) *float64 {
	c.prevMu.Lock()
	defer c.prevMu.Unlock()
	cur := cpuPrev{s.CPUStats.CPUUsage.TotalUsage, s.CPUStats.SystemUsage}
	p, ok := c.prev[id]
	c.prev[id] = cur
	if !ok || cur.system <= p.system || cur.total < p.total {
		return nil
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = 1
	}
	v := float64(cur.total-p.total) / float64(cur.system-p.system) * cpus * 100
	return &v
}

// ---------- Host metrics (read from /proc, which is not namespaced) ----------

func (c *Collector) host() *HostView {
	mem, err := readKV("/proc/meminfo")
	if err != nil {
		return nil // not on Linux (development on Windows)
	}
	h := &HostView{
		MemTotal:  mem["MemTotal"] * 1024,
		MemUsed:   (mem["MemTotal"] - mem["MemAvailable"]) * 1024,
		SwapTotal: mem["SwapTotal"] * 1024,
		SwapUsed:  (mem["SwapTotal"] - mem["SwapFree"]) * 1024,
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			h.Uptime = int64(up)
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			h.Load = strings.Join(f[:3], " ")
		}
	}
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if f[0] == "cpu" && len(f) >= 8 {
				var total, idle uint64
				for i, s := range f[1:] {
					n, _ := strconv.ParseUint(s, 10, 64)
					if i < 8 { // user..steal; guest is already counted in user
						total += n
					}
					if i == 3 || i == 4 { // idle + iowait
						idle += n
					}
				}
				busy := total - idle
				if p := c.hostPrev; p[1] > 0 && total > p[1] {
					v := float64(busy-p[0]) / float64(total-p[1]) * 100
					h.CPU = &v
				}
				c.hostPrev = [2]uint64{busy, total}
			} else if strings.HasPrefix(f[0], "cpu") {
				h.CPUs++
			}
		}
	}
	h.DiskTotal, h.DiskUsed = diskUsage(c.hostRoot)
	return h
}

func readKV(path string) (map[string]uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if f := strings.Fields(v); len(f) > 0 {
			m[k], _ = strconv.ParseUint(f[0], 10, 64)
		}
	}
	return m, nil
}
