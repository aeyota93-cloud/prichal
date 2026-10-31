package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Managing the links of a Telegram proxy (telemt) from the panel: a new link,
// show/copy a link, a new secret, off/on, delete and limits. Two proxies are
// known: Причал's own (compose service «proxy», container prichal-proxy), driven
// over a private network with a password, and the one the Amnezia app creates
// (amnezia-telemt), driven through `docker exec` (scripts/telemt-api.sh).
// Secrets reach the browser only when the owner asks for a link; the periodic
// connections probe never carries them.

const (
	telemtContainer   = "amnezia-telemt"
	ownProxyContainer = "prichal-proxy"
)

// ownProxyURL is telemt's API on the private compose network proxy-api. The
// alias exists only there: the container name also resolves on «default»,
// where the API (whitelisted to proxy-api) would drop the connection.
var ownProxyURL = "http://telemt-api:9091"

var proxyHTTP = &http.Client{Timeout: 15 * time.Second}

// proxyMu runs proxy changes one at a time: two «new link» at once would pick
// the same extra_N, two «new secret» would hand out a link that is already dead.
var proxyMu sync.Mutex

var (
	telemtAPIScript = script("telemt-api.sh")
	// telemt itself allows [A-Za-z0-9_.-]{1,64}; the panel names new links extra_N.
	proxyUserRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	extraRe     = regexp.MustCompile(`^extra_(\d+)$`)
)

// proxyDone and proxyVerb are for the timeline, like actionDone/actionVerb.
var proxyDone = map[string]string{
	"create": "создана", "rotate": "получила новый секрет", "enable": "включена",
	"disable": "отключена", "delete": "удалена", "limits": "получила новые лимиты",
}

var proxyVerb = map[string]string{
	"create": "создать ссылку прокси", "rotate": "сменить секрет у", "enable": "включить",
	"disable": "отключить", "delete": "удалить", "limits": "сменить лимиты у",
}

type proxyRequest struct {
	Container string `json:"container"` // amnezia-telemt or prichal-proxy; empty: the one that runs
	Action    string `json:"action"`
	Name      string `json:"name"`  // telemt user; empty for create
	Label     string `json:"label"` // create: the owner's label for the new link
	// limits: 0 removes the limit
	MaxDevices int `json:"maxDevices"`
	QuotaGB    int `json:"quotaGB"` // in total: telemt 3.5.x has no monthly reset
}

// telemtCall is one prepared API request: method, path and an optional body.
type telemtCall struct {
	method, path string
	body         map[string]any
}

// proxyCall turns a panel request into a telemt API request. Everything that
// reaches telemt is checked here.
func proxyCall(in proxyRequest, users []string) (telemtCall, string, error) {
	switch in.Action {
	case "create":
		name := nextExtra(users)
		return telemtCall{"POST", "/v1/users", map[string]any{"username": name}}, name, nil
	case "link", "rotate", "enable", "disable", "delete", "limits":
	default:
		return telemtCall{}, "", errors.New("неизвестное действие")
	}
	if !proxyUserRe.MatchString(in.Name) || !contains(users, in.Name) {
		return telemtCall{}, "", errors.New("нет такой ссылки")
	}
	if in.Action == "delete" && len(users) <= 1 {
		return telemtCall{}, "", errors.New("последнюю ссылку удалить нельзя: сначала создайте новую")
	}
	p := "/v1/users/" + url.PathEscape(in.Name)
	switch in.Action {
	case "link":
		return telemtCall{"GET", p, nil}, in.Name, nil
	case "rotate":
		return telemtCall{"POST", p + "/rotate-secret", nil}, in.Name, nil
	case "enable", "disable":
		return telemtCall{"POST", p + "/" + in.Action, nil}, in.Name, nil
	case "delete":
		if in.Name == "amnezia" && in.Container != ownProxyContainer {
			return telemtCall{}, "", errors.New("основную ссылку удалить нельзя: её выдаёт Amnezia")
		}
		return telemtCall{"DELETE", p, nil}, in.Name, nil
	}
	// limits
	if in.MaxDevices < 0 || in.MaxDevices > 1000 || in.QuotaGB < 0 || in.QuotaGB > 100000 {
		return telemtCall{}, "", errors.New("лимит вне разумных границ")
	}
	body := map[string]any{"max_unique_ips": nil, "data_quota_bytes": nil}
	if in.MaxDevices > 0 {
		body["max_unique_ips"] = in.MaxDevices
	}
	if in.QuotaGB > 0 {
		body["data_quota_bytes"] = uint64(in.QuotaGB) << 30
	}
	return telemtCall{"PATCH", p, body}, in.Name, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// nextExtra is the first free extra_N after the largest one in use.
func nextExtra(users []string) string {
	n := 0
	for _, u := range users {
		if m := extraRe.FindStringSubmatch(u); m != nil {
			if v, _ := strconv.Atoi(m[1]); v > n {
				n = v
			}
		}
	}
	return "extra_" + strconv.Itoa(n+1)
}

// telemtUser is the part of telemt's UserInfo the panel needs from a change.
type telemtUser struct {
	Username   string `json:"username"`
	MaxIPs     *int   `json:"max_unique_ips"`
	QuotaBytes *int64 `json:"data_quota_bytes"`
	Links      struct {
		TLS     []string `json:"tls"`
		Secure  []string `json:"secure"`
		Classic []string `json:"classic"`
	} `json:"links"`
}

func (u telemtUser) links() []string {
	out := append([]string{}, u.Links.TLS...)
	out = append(out, u.Links.Secure...)
	return append(out, u.Links.Classic...)
}

// parseTelemtReply reads telemt's envelope. data is either a UserInfo, a
// CreateUserResponse ({user, secret}) or a DeleteUserResponse.
func parseTelemtReply(raw []byte) (telemtUser, error) {
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return telemtUser{}, errors.New("прокси не ответил")
	}
	if !env.OK {
		msg := env.Error.Message
		if msg == "" {
			msg = env.Error.Code
		}
		return telemtUser{}, fmt.Errorf("прокси отказал: %s", msg)
	}
	var wrapped struct {
		User *telemtUser `json:"user"`
	}
	if json.Unmarshal(env.Data, &wrapped) == nil && wrapped.User != nil {
		return *wrapped.User, nil
	}
	var u telemtUser
	_ = json.Unmarshal(env.Data, &u)
	return u, nil
}

// telemtDo sends one API call to the proxy in container c.
func (s *server) telemtDo(ctx context.Context, c *ContainerSummary, call telemtCall) ([]byte, error) {
	if c.Name() == ownProxyContainer {
		return telemtHTTP(ctx, call)
	}
	return s.telemtExec(ctx, c.ID, call)
}

// telemtHTTP calls Причал's own proxy. The password comes from .env
// (PROXY_API_TOKEN, written by install.sh into both .env and the proxy config).
func telemtHTTP(ctx context.Context, c telemtCall) ([]byte, error) {
	var body io.Reader
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, c.method, ownProxyURL+c.path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("PROXY_API_TOKEN"))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := proxyHTTP.Do(req)
	if err != nil {
		return nil, errors.New("прокси не отвечает")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("прокси не принял пароль API: проверьте PROXY_API_TOKEN и перезапустите ./install.sh")
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (s *server) telemtExec(ctx context.Context, id string, c telemtCall) ([]byte, error) {
	cmd := []string{"sh", "-c", telemtAPIScript, "telemt-api", c.method, c.path}
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			return nil, err
		}
		cmd = append(cmd, string(b))
	}
	return s.docker.Exec(ctx, id, cmd)
}

// proxyUsers is the list of link names telemt has right now.
func (s *server) proxyUsers(ctx context.Context, c *ContainerSummary) ([]string, error) {
	raw, err := s.telemtDo(ctx, c, telemtCall{method: "GET", path: "/v1/users"})
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK   bool `json:"ok"`
		Data []struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &resp) != nil || !resp.OK {
		return nil, errors.New("прокси не ответил на запрос списка ссылок")
	}
	out := make([]string, 0, len(resp.Data))
	for _, u := range resp.Data {
		out = append(out, u.Username)
	}
	return out, nil
}

// pickProxy chooses the proxy container for a request: the named one, or the
// panel's own proxy before Amnezia's when none is named. Only running ones.
func pickProxy(list []ContainerSummary, want string) *ContainerSummary {
	if want != "" && want != ownProxyContainer && want != telemtContainer {
		return nil
	}
	var own, amnezia *ContainerSummary
	for i := range list {
		if list[i].State != "running" {
			continue
		}
		switch list[i].Name() {
		case ownProxyContainer:
			own = &list[i]
		case telemtContainer:
			amnezia = &list[i]
		}
	}
	switch {
	case want == ownProxyContainer:
		return own
	case want == telemtContainer:
		return amnezia
	case own != nil:
		return own
	}
	return amnezia
}

// proxyLinkName is how the timeline names a link (the same as the page does).
func proxyLinkName(name, label string) string {
	base := name
	if name == "amnezia" || name == "main" {
		base = "Основная ссылка"
	} else if m := extraRe.FindStringSubmatch(name); m != nil {
		base = "Дополнительная ссылка " + m[1]
	}
	if label != "" {
		return base + " («" + label + "»)"
	}
	return base
}

func (s *server) proxyUser(w http.ResponseWriter, r *http.Request) {
	var in proxyRequest
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	proxyMu.Lock()
	defer proxyMu.Unlock()
	ctx, cancel := ctxTimeout(r, 30*time.Second)
	defer cancel()
	list, err := s.docker.Containers(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	pc := pickProxy(list, in.Container)
	if pc == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "прокси Telegram не запущен"})
		return
	}
	container := pc.Name()
	users, err := s.proxyUsers(ctx, pc)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	call, name, err := proxyCall(in, users)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	label := s.conns.label(container, name)
	if in.Action == "create" {
		label = strings.Join(strings.Fields(in.Label), " ")
	}
	what := proxyLinkName(name, label)
	raw, err := s.telemtDo(ctx, pc, call)
	var u telemtUser
	if err == nil {
		u, err = parseTelemtReply(raw)
	}
	if err != nil {
		log.Printf("proxy %s %s: %v", in.Action, name, err)
		if in.Action != "link" {
			if in.Action == "create" {
				s.journal.Add("bad", "Не получилось "+proxyVerb[in.Action]+": "+shortErr(err))
			} else {
				s.journal.Add("bad", "Не получилось "+proxyVerb[in.Action]+" "+what+": "+shortErr(err))
			}
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	switch in.Action {
	case "create":
		if label != "" {
			if err := s.conns.labels.Set(container, name, label); err != nil {
				log.Printf("proxy label %s: %v", name, err)
			}
		}
	case "delete":
		_ = s.conns.labels.Set(container, name, "")
	}
	if in.Action != "link" {
		log.Printf("proxy %s %s", in.Action, name)
		s.journal.Add("", "Прокси Telegram: "+what+" "+proxyDone[in.Action]+" из панели")
		s.conns.Invalidate()
	}
	out := map[string]any{"name": name}
	if in.Action == "link" || in.Action == "create" || in.Action == "rotate" {
		out["links"] = u.links() // contains the secret: only on the owner's explicit request
	}
	if in.Action == "link" {
		maxIPs, quotaGB := 0, 0
		if u.MaxIPs != nil {
			maxIPs = *u.MaxIPs
		}
		if u.QuotaBytes != nil {
			quotaGB = int(*u.QuotaBytes >> 30)
		}
		out["maxDevices"], out["quotaGB"] = maxIPs, quotaGB
	}
	writeJSON(w, http.StatusOK, out)
}
