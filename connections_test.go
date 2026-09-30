package main

import (
	"strings"
	"testing"
	"time"
)

// Samples shaped like the real server output on 2026-09-30 (keys shortened).

func TestParseWG(t *testing.T) {
	raw := "KEYA=\t1000\nKEYB=\t0\nKEYC=\t500\n@@@\n" +
		"KEYA=\t22030620664\t4139787252\nKEYB=\t0\t0\nKEYC=\t180\t124\n@@@\n" +
		`[{"clientId":"KEYB=","userData":{"clientName":"Phone"}},{"clientId":"KEYA=","userData":{"clientName":"Admin [iOS 27.2]","dataReceived":"27 MiB"}}]`
	got, err := parseWG([]byte(raw), 1100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 clients, got %+v", got)
	}
	if got[0].Name != "Phone" || got[0].Online || got[0].LastSeen != 0 {
		t.Errorf("never-connected client: %+v", got[0])
	}
	if got[1].Name != "Admin [iOS 27.2]" || !got[1].Online || got[1].Down != 4139787252 || got[1].Up != 22030620664 {
		t.Errorf("online client: %+v", got[1])
	}
	if !strings.HasPrefix(got[2].Name, "без имени") || got[2].Online {
		t.Errorf("unnamed stale client: %+v", got[2])
	}
}

func TestParseOpenVPN(t *testing.T) {
	raw := "OpenVPN CLIENT LIST\nUpdated,2026-09-30 09:10:38\nCommon Name,Real Address,Bytes Received,Bytes Sent,Connected Since\n" +
		"PqFz,1.2.3.4:49481,30715037,312378424,2026-09-30 08:22:04\nROUTING TABLE\n" +
		"Virtual Address,Common Name,Real Address,Last Ref\n10.8.0.2,PqFz,1.2.3.4:49481,2026-09-30 09:10:37\nGLOBAL STATS\nEND\n@@@\n" +
		`[{"clientId":"476y","userData":{"clientName":"router"}},{"clientId":"PqFz","userData":{"clientName":"laptop"}}]`
	got, err := parseOpenVPN([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "router" || got[0].Online {
		t.Fatalf("offline client: %+v", got)
	}
	c := got[1]
	if c.Name != "laptop" || !c.Online || c.Down != 312378424 || c.Up != 30715037 || c.Since != time.Date(2026, 9, 30, 8, 22, 4, 0, time.UTC).Unix() {
		t.Errorf("online client: %+v", c)
	}
}

func TestParseTelemtDropsLinks(t *testing.T) {
	raw := `{"ok":true,"data":[{"username":"extra_3","enabled":true,"current_connections":3,"active_unique_ips":1,` +
		`"active_unique_ips_list":["188.243.1.2"],"recent_unique_ips_list":["188.243.1.2","10.0.0.9"],` +
		`"total_octets":33455843,"links":{"tls":["tg://proxy?secret=ee00"]}}]}`
	got, err := parseTelemt([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Online || got[0].Devices != 1 || got[0].Conns != 3 || got[0].Total != 33455843 {
		t.Errorf("%+v", got)
	}
	if len(got[0].IPs) != 1 || len(got[0].RecentIPs) != 1 || got[0].RecentIPs[0] != "10.0.0.9" {
		t.Errorf("recent must exclude addresses that are on air now: %+v", got[0])
	}
	if _, err := parseTelemt([]byte("curl: (7) Failed to connect")); err == nil {
		t.Error("want error for non-JSON output")
	}
}
