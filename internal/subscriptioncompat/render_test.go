package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestCascadeMaterializesOnlyEntitledLandingAndFailsOnMissingEntry(t *testing.T) {
	s := compatDB(t)
	ib := model.Inbound{Protocol: model.VLESS, Tag: "landing", Port: 26450, Enable: true, StreamSettings: `{"network":"tcp","security":"none"}`}
	s.DB.Create(&ib)
	h := model.Host{InboundId: ib.Id, Remark: "Landing", Address: "landing.test"}
	s.DB.Create(&h)
	settings, err := s.SaveHost(h.Id, HostInput{CascadeMode: "replace", EntryYAML: `{name: entry, type: socks5, server: entry.test, port: 1080, password: synthetic}`})
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"proxies": []map[string]any{{"name": "Landing", "type": "vless", "server": "landing.test", "port": 443, "uuid": "user-one", "_mgma-host-id": h.Id, "_mgma-inbound-id": ib.Id}}, "proxy-groups": []map[string]any{{"name": "PROXY", "type": "select", "proxies": []string{"Landing"}}}, "rules": []string{"MATCH,PROXY"}}
	if err = s.Render(config, Projection{}, false); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(config)
	if !strings.Contains(string(b), `"dialer-proxy":"MGMA-HOP`) || strings.Contains(string(b), "_mgma-") {
		t.Fatalf("cascade=%s", b)
	}
	proxies := mapList(config["proxies"])
	if len(proxies) != 2 || proxies[0]["uuid"] != "user-one" {
		t.Fatalf("landing credentials lost: %+v", proxies)
	}
	groups := mapList(config["proxy-groups"])
	if strings.Contains(string(mustJSON(groups)), "MGMA-HOP") {
		t.Fatal("dependency entered normal choices")
	}
	if err = s.DB.Model(&settings).Update("entry_ciphertext", "invalid").Error; err != nil {
		t.Fatal(err)
	}
	denied := map[string]any{"proxies": []map[string]any{{"name": "Landing", "_mgma-host-id": h.Id}}}
	if err = s.Render(denied, Projection{}, false); !errors.Is(err, ErrCascade) {
		t.Fatalf("broken cascade silently bypassed: %v", err)
	}
	if err = s.Render(map[string]any{"proxies": []map[string]any{{"name": "Unrelated"}}}, Projection{}, false); err != nil {
		t.Fatal("unentitled broken route blocked user")
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestSecureDNSRejectsDirectAndCyclesAndKeepsBootstrapSeparate(t *testing.T) {
	for _, members := range [][]string{{"DIRECT"}, {"unknown"}, {"PROXY"}, {"node"}} {
		config := map[string]any{"proxies": []map[string]any{{"name": "node", "type": "socks5", "server": "synthetic.test"}}, "proxy-groups": []map[string]any{{"name": "PROXY", "proxies": members}}}
		SecureDNS(config, "PROXY", "https://panel.example.test/sub/")
		dns := config["dns"].(map[string]any)
		names := dns["nameserver"].([]string)
		expected := "#REJECT"
		if members[0] == "node" {
			expected = "#PROXY"
		}
		if !strings.HasSuffix(names[0], expected) || dns["ipv6"] != false || config["ipv6"] != true {
			t.Fatalf("DNS escaped selected proxy: %+v", dns)
		}
		if strings.Contains(string(mustJSON(dns)), `"system"`) || len(dns["fallback"].([]string)) != 0 {
			t.Fatal("plaintext/system DNS fallback enabled")
		}
		if _, exists := config["tun"].(map[string]any)["enable"]; exists {
			t.Fatal("subscription enabled client TUN")
		}
	}
}

func TestCascadeAndExternalNodesDoNotBroadenOtherPolicyGroups(t *testing.T) {
	s := compatDB(t)
	ib := model.Inbound{Protocol: model.VLESS, Tag: "isolation", Port: 26455, Enable: true, StreamSettings: `{"network":"tcp"}`}
	s.DB.Create(&ib)
	h := model.Host{InboundId: ib.Id, Remark: "A", Address: "landing.test"}
	s.DB.Create(&h)
	if _, err := s.SaveHost(h.Id, HostInput{CascadeMode: "additional", EntryYAML: `{name: entry, type: socks5, server: entry.test, port: 1080}`}); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"proxies": []map[string]any{{"name": "A", "type": "socks5", "server": "a.test", "port": 1080, "_mgma-host-id": h.Id}, {"name": "B", "type": "socks5", "server": "b.test", "port": 1080}}, "proxy-groups": []map[string]any{{"name": "Only A", "type": "select", "proxies": []string{"A"}}, {"name": "Reject", "type": "select", "proxies": []string{"REJECT"}}, {"name": "Auto", "type": "select", "include-all-proxies": true, "filter": "^A"}}}
	projection := Projection{HasExternal: true, Sources: []Overlay{{ID: 999, GroupName: "Airport", Nodes: []map[string]any{{"name": "Airport node", "type": "socks5", "server": "airport.test", "port": 1080}}}}}
	if err := s.Render(config, projection, false); err != nil {
		t.Fatal(err)
	}
	groups := mapList(config["proxy-groups"])
	for _, g := range groups {
		members := stringList(g["proxies"])
		switch g["name"] {
		case "Only A":
			if slices.Contains(members, "B") {
				t.Fatal("cascade added unrelated local node")
			}
		case "Reject":
			if len(members) != 1 || members[0] != "REJECT" {
				t.Fatal("cascade expanded a reject-only policy")
			}
		case "Auto":
			if len(members) != 2 || !slices.Contains(members, "A") {
				t.Fatalf("auto group expanded upstream nodes: %v", members)
			}
			if _, exists := g["include-all-proxies"]; exists {
				t.Fatal("deferred include-all would admit helper/upstream nodes")
			}
		}
	}
}
