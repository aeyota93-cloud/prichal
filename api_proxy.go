package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Managing the links of the Amnezia Telegram proxy (telemt) from the panel:
// a new link, show/copy a link, a new secret, off/on, delete and limits.
// Every call is one request to telemt's own control API inside its container
// (scripts/telemt-api.sh). Secrets reach the browser only when the owner asks
// for a link; the periodic connections probe never carries them.

const telemtContainer = "amnezia-telemt"

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
	Action string `json:"action"`
	Name   string `json:"name"`  // telemt user; empty for create
	Label  string `json:"label"` // create: the owner's label for the new link
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
	p := "/v1/users/" + url.PathEscape(in.Name)
	switch in.Action {
	case "link":
		return telemtCall{"GET", p, nil}, in.Name, nil
	case "rotate":
		return telemtCall{"POST", p + "/rotate-secret", nil}, in.Name, nil
	case "enable", "disable":
		return telemtCall{"POST", p + "/" + in.Action, nil}, in.Name, nil
	case "delete":
		if in.Name == "amnezia" {
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
func (s *server) proxyUsers(ctx context.Context, id string) ([]string, error) {
	raw, err := s.telemtExec(ctx, id, telemtCall{method: "GET", path: "/v1/users"})
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

// proxyLinkName is how the timeline names a link (the same as the page does).
func proxyLinkName(name, label string) string {
	base := name
	if name == "amnezia" {
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
	id := ""
	for _, c := range list {
		if c.Name() == telemtContainer && c.State == "running" {
			id = c.ID
		}
	}
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "прокси Telegram не запущен"})
		return
	}
	users, err := s.proxyUsers(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	call, name, err := proxyCall(in, users)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	label := s.conns.labels.Get(telemtContainer, name)
	if in.Action == "create" {
		label = strings.Join(strings.Fields(in.Label), " ")
	}
	what := proxyLinkName(name, label)
	raw, err := s.telemtExec(ctx, id, call)
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
			if err := s.conns.labels.Set(telemtContainer, name, label); err != nil {
				log.Printf("proxy label %s: %v", name, err)
			}
		}
	case "delete":
		_ = s.conns.labels.Set(telemtContainer, name, "")
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
