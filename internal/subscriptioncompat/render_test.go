package subscriptioncompat

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestRetiredRouteSettingsDoNotAlterNativeProxies(t *testing.T) {
	s := compatDB(t)
	ib := model.Inbound{Protocol: model.VLESS, Tag: "native", Port: 26450, Enable: true, StreamSettings: `{"network":"tcp","security":"none"}`}
	if err := s.DB.Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	h := model.Host{InboundId: ib.Id, Remark: "Native", Address: "native.test"}
	if err := s.DB.Create(&h).Error; err != nil {
		t.Fatal(err)
	}
	archived := model.SubscriptionHost{HostID: h.Id, CascadeMode: "additional", EntryCiphertext: "unavailable", ClientStream: "invalid retired settings", MihomoOnly: true}
	if err := s.DB.Create(&archived).Error; err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		config := map[string]any{"proxies": []map[string]any{{"name": "Native", "type": "vless", "server": "native.test", "port": 443, "uuid": "user-one", "_mgma-host-id": h.Id, "_mgma-inbound-id": ib.Id}}, "proxy-groups": []map[string]any{{"name": "PROXY", "type": "select", "proxies": []string{"Native"}}}, "rules": []string{"MATCH,PROXY"}}
		if err := s.Render(config, Projection{}, legacy); err != nil {
			t.Fatal(err)
		}
		proxies := mapList(config["proxies"])
		if len(proxies) != 1 || proxies[0]["uuid"] != "user-one" || proxies[0]["server"] != "native.test" || proxies[0]["dialer-proxy"] != nil {
			t.Fatalf("archived settings changed native proxy: %+v", proxies)
		}
		if strings.Contains(string(mustJSON(config)), "_mgma-") {
			t.Fatal("internal metadata escaped")
		}
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

func TestExternalNodesDoNotBroadenOtherPolicyGroups(t *testing.T) {
	s := compatDB(t)
	ib := model.Inbound{Protocol: model.VLESS, Tag: "isolation", Port: 26455, Enable: true, StreamSettings: `{"network":"tcp"}`}
	s.DB.Create(&ib)
	h := model.Host{InboundId: ib.Id, Remark: "A", Address: "landing.test"}
	s.DB.Create(&h)
	config := map[string]any{"proxies": []map[string]any{{"name": "A", "type": "socks5", "server": "a.test", "port": 1080, "_mgma-host-id": h.Id}, {"name": "B", "type": "socks5", "server": "b.test", "port": 1080}}, "proxy-groups": []map[string]any{{"name": "Only A", "type": "select", "proxies": []string{"A"}}, {"name": "Reject", "type": "select", "proxies": []string{"REJECT"}}, {"name": "Auto", "type": "select", "include-all-proxies": true, "filter": "^A"}}}
	projection := Projection{HasExternal: true, Sources: []Overlay{{ID: 999, GroupName: "Airport", Nodes: []ProjectedNode{{Proxy: map[string]any{"name": "Airport node", "type": "socks5", "server": "airport.test", "port": 1080}}}}}}
	if err := s.Render(config, projection, false); err != nil {
		t.Fatal(err)
	}
	groups := mapList(config["proxy-groups"])
	for _, g := range groups {
		members := stringList(g["proxies"])
		switch g["name"] {
		case "Only A":
			if slices.Contains(members, "B") {
				t.Fatal("projection added unrelated local node")
			}
		case "Reject":
			if len(members) != 1 || members[0] != "REJECT" {
				t.Fatal("projection expanded a reject-only policy")
			}
		case "Auto":
			if len(members) != 1 || !slices.Contains(members, "A") {
				t.Fatalf("auto group expanded upstream nodes: %v", members)
			}
			if _, exists := g["include-all-proxies"]; exists {
				t.Fatal("deferred include-all would admit helper/upstream nodes")
			}
		}
	}
}
