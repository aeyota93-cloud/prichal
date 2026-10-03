package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Who is connected to the Amnezia services. Every probe is a fixed read-only
// command run inside the container (the same way the Amnezia app itself looks
// at the server). Keys and proxy secrets are dropped here and never reach the
// browser. WireGuard and OpenVPN client addresses are dropped too; for telemt
// the device addresses (ips, recentIps) are sent on purpose, to the signed-in
// owner of the panel only.

const (
	connCacheFor   = 8 * time.Second
	wgOnlineWithin = 180 // seconds since the last WireGuard handshake
)

type probe struct {
	kind string // "wg", "openvpn", "telemt"
	cmd  []string
}

// Every Amnezia protocol lives in a container with a fixed name prefix; the
// probe scripts are scripts/wg.sh, openvpn.sh and telemt.sh.

// probeFor picks how to ask an Amnezia container about its clients.
func probeFor(name string) (probe, bool) {
	if !strings.HasPrefix(name, "amnezia-") {
		return probe{}, false
	}
	n := strings.TrimPrefix(name, "amnezia-")
	switch {
	case strings.Contains(n, "awg") || strings.Contains(n, "wireguard"):
		return probe{"wg", []string{"sh", "-c", wgScript}}, true
	case strings.Contains(n, "openvpn"):
		return probe{"openvpn", []string{"sh", "-c", openvpnScript}}, true
	case n == "telemt":
		return probe{"telemt", []string{"sh", "-c", telemtScript}}, true
	}
	return probe{}, false
}

// probeRank orders services on the page: WireGuard family, OpenVPN, proxy.
func probeRank(s ConnService) int {
	switch s.Kind {
	case "wg":
		if strings.Contains(s.Container, "awg") {
			return 0
		}
		return 1
	case "openvpn":
		return 2
	}
	return 3
}

type ConnClient struct {
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen,omitempty"` // unix seconds; 0 = never
	Since    int64  `json:"since,omitempty"`    // connected since (OpenVPN)
	Down     uint64 `json:"down"`               // bytes the client downloaded
	Up       uint64 `json:"up"`                 // bytes the client uploaded
	Total    uint64 `json:"total,omitempty"`    // telemt counts both directions together
	Devices  int    `json:"devices,omitempty"`  // telemt: unique IPs right now
	Conns    int    `json:"conns,omitempty"`    // telemt: open TCP connections
	Disabled bool   `json:"disabled,omitempty"`

	// telemt only: device addresses (the proxy knows nothing else about them)
	// and the owner's own label for the link, stored by the panel.
	IPs       []string `json:"ips,omitempty"`
	RecentIPs []string `json:"recentIps,omitempty"`
	Label     string   `json:"label,omitempty"`
}

type ConnService struct {
	Container string       `json:"container"`
	Kind      string       `json:"kind"`
	State     string       `json:"state"`
	Error     string       `json:"error,omitempty"`
	Clients   []ConnClient `json:"clients"`
}

type Connections struct {
	Services []ConnService `json:"services"`
	Updated  int64         `json:"updated"`
}

type ConnWatcher struct {
	docker *Docker
	labels *Labels
	mu     sync.Mutex
	last   time.Time
	snap   *Connections
}

// Invalidate drops the cached snapshot, e.g. after a label was changed.
func (w *ConnWatcher) Invalidate() {
	w.mu.Lock()
	w.last = time.Time{}
	w.mu.Unlock()
}

func (w *ConnWatcher) Get(ctx context.Context) (*Connections, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.snap != nil && time.Since(w.last) < connCacheFor {
		return w.snap, nil
	}
	list, err := w.docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	var out []ConnService
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range list {
		p, ok := probeFor(c.Name())
		if !ok {
			continue
		}
		wg.Add(1)
		go func(c ContainerSummary, p probe) {
			defer wg.Done()
			s := ConnService{Container: c.Name(), Kind: p.kind, State: c.State, Clients: []ConnClient{}}
			if c.State == "running" {
				ectx, cancel := context.WithTimeout(ctx, 8*time.Second)
				raw, err := w.docker.Exec(ectx, c.ID, p.cmd)
				cancel()
				if err == nil {
					s.Clients, err = parseProbe(p.kind, raw, time.Now().Unix())
				}
				for i := range s.Clients {
					s.Clients[i].Label = w.labels.Get(s.Container, s.Clients[i].Name)
				}
				if err != nil {
					s.Error = err.Error()
				}
			}
			mu.Lock()
			out = append(out, s)
			mu.Unlock()
		}(c, p)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := probeRank(out[i]), probeRank(out[j]); a != b {
			return a < b
		}
		return out[i].Container < out[j].Container
	})
	w.snap = &Connections{Services: out, Updated: time.Now().UnixMilli()}
	w.last = time.Now()
	return w.snap, nil
}

func parseProbe(kind string, raw []byte, now int64) ([]ConnClient, error) {
	switch kind {
	case "wg":
		return parseWG(raw, now)
	case "openvpn":
		return parseOpenVPN(raw)
	case "telemt":
		return parseTelemt(raw)
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

type amneziaClient struct {
	ClientID string `json:"clientId"`
	UserData struct {
		ClientName string `json:"clientName"`
	} `json:"userData"`
}

func clientNames(b []byte) (map[string]string, []string) {
	var table []amneziaClient
	_ = json.Unmarshal(bytes.TrimSpace(b), &table)
	names := map[string]string{}
	var order []string
	for _, c := range table {
		names[c.ClientID] = c.UserData.ClientName
		order = append(order, c.ClientID)
	}
	return names, order
}

func splitParts(raw []byte, n int) [][]byte {
	parts := bytes.SplitN(raw, []byte("@@@\n"), n)
	for len(parts) < n {
		parts = append(parts, nil)
	}
	return parts
}

func unnamed(id string) string {
	if len(id) > 6 {
		id = id[:6]
	}
	return "без имени (" + id + "…)"
}

// parseWG reads `wg show <if> latest-handshakes` / `transfer` (public keys
// only) and Amnezia's clientsTable for names.
func parseWG(raw []byte, now int64) ([]ConnClient, error) {
	parts := splitParts(raw, 3)
	byKey := map[string]*ConnClient{}
	var keys []string
	sc := bufio.NewScanner(bytes.NewReader(parts[0]))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			continue
		}
		ts, _ := strconv.ParseInt(f[1], 10, 64)
		c := &ConnClient{LastSeen: ts, Online: ts > 0 && now-ts <= wgOnlineWithin}
		byKey[f[0]] = c
		keys = append(keys, f[0])
	}
	if len(keys) == 0 && len(bytes.TrimSpace(parts[0])) > 0 {
		return nil, fmt.Errorf("не удалось прочитать список клиентов")
	}
	sc = bufio.NewScanner(bytes.NewReader(parts[1]))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 || byKey[f[0]] == nil {
			continue
		}
		rx, _ := strconv.ParseUint(f[1], 10, 64) // server received = client uploaded
		tx, _ := strconv.ParseUint(f[2], 10, 64)
		byKey[f[0]].Up, byKey[f[0]].Down = rx, tx
	}
	names, order := clientNames(parts[2])
	var out []ConnClient
	seen := map[string]bool{}
	add := func(k string) {
		c := byKey[k]
		if c == nil || seen[k] {
			return
		}
		seen[k] = true
		c.Name = names[k]
		if c.Name == "" {
			c.Name = unnamed(k)
		}
		out = append(out, *c)
	}
	for _, k := range order { // Amnezia's order: the order clients were added
		add(k)
	}
	for _, k := range keys {
		add(k)
	}
	return out, nil
}

// parseOpenVPN reads the status file (version 1 format). Real addresses are
// ignored on purpose.
func parseOpenVPN(raw []byte) ([]ConnClient, error) {
	parts := splitParts(raw, 2)
	type live struct {
		since    int64
		down, up uint64
	}
	online := map[string]live{}
	inList := false
	sc := bufio.NewScanner(bytes.NewReader(parts[0]))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "Common Name,"):
			inList = true
			continue
		case strings.HasPrefix(line, "ROUTING TABLE"):
			inList = false
		}
		if !inList {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) < 5 {
			continue
		}
		rx, _ := strconv.ParseUint(f[2], 10, 64)
		tx, _ := strconv.ParseUint(f[3], 10, 64)
		var since int64
		if t, err := time.Parse("2006-01-02 15:04:05", f[4]); err == nil {
			since = t.Unix() // the container runs in UTC
		}
		online[f[0]] = live{since, tx, rx}
	}
	names, order := clientNames(parts[1])
	var out []ConnClient
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
		c := ConnClient{Name: names[id]}
		if c.Name == "" {
			c.Name = unnamed(id)
		}
		if l, ok := online[id]; ok {
			c.Online, c.Since, c.Down, c.Up = true, l.since, l.down, l.up
		}
		out = append(out, c)
	}
	for id, l := range online {
		if !seen[id] {
			out = append(out, ConnClient{Name: unnamed(id), Online: true, Since: l.since, Down: l.down, Up: l.up})
		}
	}
	return out, nil
}

// parseTelemt reads telemt's /v1/stats/users. The struct deliberately has no
// field for the proxy links, which contain the secrets.
func parseTelemt(raw []byte) ([]ConnClient, error) {
	var resp struct {
		OK   bool `json:"ok"`
		Data []struct {
			Username    string   `json:"username"`
			Enabled     bool     `json:"enabled"`
			Connections int      `json:"current_connections"`
			ActiveIPs   int      `json:"active_unique_ips"`
			ActiveList  []string `json:"active_unique_ips_list"`
			RecentList  []string `json:"recent_unique_ips_list"`
			Octets      uint64   `json:"total_octets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || !resp.OK {
		return nil, fmt.Errorf("прокси не ответил на запрос статистики")
	}
	out := make([]ConnClient, 0, len(resp.Data))
	for _, u := range resp.Data {
		active := map[string]bool{}
		for _, ip := range u.ActiveList {
			active[ip] = true
		}
		var recent []string // "recently" means: seen lately but not on air now
		for _, ip := range u.RecentList {
			if !active[ip] {
				recent = append(recent, ip)
			}
		}
		out = append(out, ConnClient{
			Name: u.Username, Online: u.Connections > 0, Disabled: !u.Enabled,
			Devices: u.ActiveIPs, Conns: u.Connections, Total: u.Octets,
			IPs: u.ActiveList, RecentIPs: recent,
		})
	}
	return out, nil
}
