package sub

import (
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestMihomoEndpointBypassUsesOnlyRenderedNativePeers(t *testing.T) {
	upload := map[string]any{
		"server": "203.0.113.7", "servername": "front.test",
		"xhttp-opts": map[string]any{"download-settings": map[string]any{"server": "[2001:db8::9]", "host": "cdn.test"}},
	}
	domain := map[string]any{"server": "Entry.Example."}
	chained := map[string]any{"server": "203.0.113.8", "dialer-proxy": "hop"}
	removed := map[string]any{"server": "203.0.113.99"}
	thirdParty := map[string]any{"server": "203.0.113.10", "_mgma-inbound-id": 1}
	native := []map[string]any{upload, upload, domain, chained, removed}
	const ipv4 = "IP-CIDR,203.0.113.7/32,DIRECT,no-resolve"
	config := map[string]any{
		"proxies": []map[string]any{upload, domain, chained, thirdParty},
		"rules":   []string{"DOMAIN,first.test,REJECT", ipv4, "MATCH,PROXY"},
		"tun":     map[string]any{"enable": false, "route-exclude-address": []string{"10.0.0.0/8", "203.0.113.7/32"}},
	}
	for range 2 {
		applyNativeEndpointBypass(config, native)
	}
	wantRules := []any{"IP-CIDR,2001:db8::9/128,DIRECT,no-resolve", ipv4, "DOMAIN,entry.example,DIRECT", "DOMAIN,first.test,REJECT", "MATCH,PROXY"}
	if !reflect.DeepEqual(config["rules"], wantRules) {
		t.Fatalf("rules = %v", config["rules"])
	}
	tun := config["tun"].(map[string]any)
	if tun["enable"] != false || !reflect.DeepEqual(tun["route-exclude-address"], []any{"10.0.0.0/8", "203.0.113.7/32", "2001:db8::9/128"}) {
		t.Fatalf("tun = %v", tun)
	}
}

func TestMihomoSubscriptionBypassesOnlyOwnIngress(t *testing.T) {
	seedSubDB(t)
	const id = "bypass-fixture"
	ib := seedSubInbound(t, id, "owned", 8043, 1, wsTLSStream)
	seedHost(t, &model.Host{InboundId: ib.Id, Address: "192.0.2.12", Remark: "Own ingress"})
	seedHost(t, &model.Host{InboundId: ib.Id, Address: "2001:db8::12", Remark: "Own IPv6 ingress"})
	var client model.ClientRecord
	db := database.GetDB()
	if err := db.Where("sub_id = ?", id).First(&client).Error; err != nil {
		t.Fatal(err)
	}
	link := model.ClientExternalLink{ClientId: client.Id, Kind: model.ExternalLinkKindLink, Value: "trojan://synthetic@198.51.100.31:443#Outside"}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	router := clashSubRouter(t)
	for _, format := range []string{"mihomo", "shadowrocket"} {
		resp := fetchClashSub(t, router, "/sub/"+id+"/"+format)
		body := resp.Body.String()
		if resp.Code != 200 || !strings.Contains(body, "198.51.100.31") {
			t.Fatalf("%s missing external node, HTTP %d", format, resp.Code)
		}
		var doc struct {
			Tun struct {
				Exclusions []string `yaml:"route-exclude-address"`
			} `yaml:"tun"`
			Rules []string `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatal(err)
		}
		wantRules := []string{
			"DOMAIN-SUFFIX,aki-game.com,DIRECT",
			"DOMAIN-SUFFIX,kurogames.com,DIRECT",
			"DOMAIN,cschannel.anticheatexpert.com,DIRECT",
			"MATCH,PROXY",
		}
		var wantExclusions []string
		if format == "mihomo" {
			wantRules = append([]string{"IP-CIDR,192.0.2.12/32,DIRECT,no-resolve", "IP-CIDR,2001:db8::12/128,DIRECT,no-resolve"}, wantRules...)
			wantExclusions = []string{"192.0.2.12/32", "2001:db8::12/128"}
			writeRuntimeProfile(t, "tun-native-bypass.yaml", body)
		}
		if !reflect.DeepEqual(doc.Rules, wantRules) || !reflect.DeepEqual(doc.Tun.Exclusions, wantExclusions) {
			t.Fatalf("%s: rules %v exclusions %v", format, doc.Rules, doc.Tun.Exclusions)
		}
	}
}
