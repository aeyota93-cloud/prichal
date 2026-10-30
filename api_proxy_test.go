package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNextExtra(t *testing.T) {
	cases := []struct {
		users []string
		want  string
	}{
		{nil, "extra_1"},
		{[]string{"amnezia"}, "extra_1"},
		{[]string{"amnezia", "extra_1", "extra_5", "extra_2"}, "extra_6"},
		{[]string{"extra_x", "friend"}, "extra_1"},
	}
	for _, c := range cases {
		if got := nextExtra(c.users); got != c.want {
			t.Errorf("nextExtra(%v) = %q, want %q", c.users, got, c.want)
		}
	}
}

func TestProxyCall(t *testing.T) {
	users := []string{"amnezia", "extra_1", "extra_3"}
	cases := []struct {
		in       proxyRequest
		method   string
		path     string
		name     string
		bodyJSON string
		err      string
	}{
		{in: proxyRequest{Action: "create", Name: "ignored"}, method: "POST", path: "/v1/users", name: "extra_4", bodyJSON: `{"username":"extra_4"}`},
		{in: proxyRequest{Action: "link", Name: "extra_1"}, method: "GET", path: "/v1/users/extra_1", name: "extra_1"},
		{in: proxyRequest{Action: "rotate", Name: "amnezia"}, method: "POST", path: "/v1/users/amnezia/rotate-secret", name: "amnezia"},
		{in: proxyRequest{Action: "disable", Name: "extra_3"}, method: "POST", path: "/v1/users/extra_3/disable", name: "extra_3"},
		{in: proxyRequest{Action: "enable", Name: "extra_3"}, method: "POST", path: "/v1/users/extra_3/enable", name: "extra_3"},
		{in: proxyRequest{Action: "delete", Name: "extra_3"}, method: "DELETE", path: "/v1/users/extra_3", name: "extra_3"},
		{in: proxyRequest{Action: "limits", Name: "extra_1", MaxDevices: 3, QuotaGB: 50}, method: "PATCH", path: "/v1/users/extra_1", name: "extra_1",
			bodyJSON: `{"data_quota_bytes":53687091200,"max_unique_ips":3}`},
		{in: proxyRequest{Action: "limits", Name: "extra_1"}, method: "PATCH", path: "/v1/users/extra_1", name: "extra_1",
			bodyJSON: `{"data_quota_bytes":null,"max_unique_ips":null}`},
		{in: proxyRequest{Action: "delete", Name: "amnezia"}, err: "основную ссылку"},
		{in: proxyRequest{Action: "rotate", Name: "nobody"}, err: "нет такой ссылки"},
		{in: proxyRequest{Action: "rotate", Name: "../config"}, err: "нет такой ссылки"},
		{in: proxyRequest{Action: "rotate", Name: "extra_1/../x"}, err: "нет такой ссылки"},
		{in: proxyRequest{Action: "limits", Name: "extra_1", MaxDevices: -1}, err: "границ"},
		{in: proxyRequest{Action: "reload", Name: "extra_1"}, err: "неизвестное действие"},
	}
	for _, c := range cases {
		call, name, err := proxyCall(c.in, users)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%+v: err = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%+v: unexpected error %v", c.in, err)
			continue
		}
		if call.method != c.method || call.path != c.path || name != c.name {
			t.Errorf("%+v: got %s %s (%s), want %s %s (%s)", c.in, call.method, call.path, name, c.method, c.path, c.name)
		}
		if c.bodyJSON == "" {
			if call.body != nil {
				t.Errorf("%+v: unexpected body %v", c.in, call.body)
			}
			continue
		}
		b, _ := json.Marshal(call.body)
		if string(b) != c.bodyJSON {
			t.Errorf("%+v: body %s, want %s", c.in, b, c.bodyJSON)
		}
	}
}

func TestParseTelemtReply(t *testing.T) {
	created := `{"ok":true,"data":{"user":{"username":"extra_4","max_unique_ips":2,"links":{"tls":["tg://proxy?a"],"secure":[],"classic":["tg://proxy?c"]}},"secret":"00112233445566778899aabbccddeeff"}}`
	u, err := parseTelemtReply([]byte(created))
	if err != nil || u.Username != "extra_4" || *u.MaxIPs != 2 {
		t.Fatalf("created: %+v, %v", u, err)
	}
	if got := strings.Join(u.links(), " "); got != "tg://proxy?a tg://proxy?c" {
		t.Errorf("links = %q", got)
	}
	plain := `{"ok":true,"data":{"username":"extra_1","links":{"tls":["tg://proxy?x"]}}}`
	if u, err := parseTelemtReply([]byte(plain)); err != nil || u.Username != "extra_1" || len(u.links()) != 1 {
		t.Errorf("plain: %+v, %v", u, err)
	}
	bad := `{"ok":false,"error":{"code":"not_found","message":"user not found"},"request_id":3}`
	if _, err := parseTelemtReply([]byte(bad)); err == nil || !strings.Contains(err.Error(), "user not found") {
		t.Errorf("bad: %v", err)
	}
	if _, err := parseTelemtReply([]byte("curl: (7) refused")); err == nil {
		t.Error("garbage must be an error")
	}
}

func TestParseTelemtLimits(t *testing.T) {
	raw := `{"ok":true,"data":[{"username":"extra_1","enabled":true,"max_unique_ips":3,"data_quota_bytes":10737418240,"links":{"tls":["tg://secret"]}},{"username":"amnezia","enabled":true}]}`
	got, err := parseTelemt([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].MaxDevices != 3 || got[0].QuotaGB != 10 || got[1].MaxDevices != 0 || got[1].QuotaGB != 0 {
		t.Errorf("limits: %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "secret") {
		t.Error("the probe must never carry links")
	}
}

func TestProxyLinkName(t *testing.T) {
	if got := proxyLinkName("amnezia", ""); got != "Основная ссылка" {
		t.Errorf("%q", got)
	}
	if got := proxyLinkName("extra_7", "Мама"); got != "Дополнительная ссылка 7 («Мама»)" {
		t.Errorf("%q", got)
	}
}
