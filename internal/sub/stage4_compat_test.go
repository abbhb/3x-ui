package sub

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func TestXHTTPExtraReplacesOuterAndDownloadNeverInheritsReality(t *testing.T) {
	input := map[string]any{"path": "/images/synthetic/", "host": "up.example.test", "mode": "packet-up", "scMaxEachPostBytes": "outer-discarded", "extra": map[string]any{"scMaxEachPostBytes": float64(2000000), "noGRPCHeader": false, "xmux": map[string]any{"maxConcurrency": "16-32"}, "downloadSettings": map[string]any{"address": "2001:db8::8", "port": float64(443), "network": "xhttp", "security": "tls", "tlsSettings": map[string]any{"serverName": "cdn.example.test", "alpn": []any{"h2"}}, "xhttpSettings": map[string]any{"path": "/images/synthetic/"}}}}
	got := buildXhttpClashOpts(input)
	if got["path"] != "/images/synthetic/" || got["host"] != "up.example.test" || got["sc-max-each-post-bytes"] != "2000000" {
		t.Fatalf("extra precedence=%+v", got)
	}
	down, ok := got["download-settings"].(map[string]any)
	if !ok || down["server"] != "2001:db8::8" || down["tls"] != true || down["host"] != "cdn.example.test" {
		t.Fatalf("download=%+v", down)
	}
	if down["reality-opts"].(map[string]any)["public-key"] != "" {
		t.Fatal("download inherited uplink reality")
	}
}

func TestStage4ExternalOnlySubscriptionAndPrivateIsolation(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	client := model.ClientRecord{Email: "external-customer", UUID: "11111111-1111-4111-8111-111111111111", SubID: "stage4-only", Enable: true, StrictHostBindings: true, RequireMGMA: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	a := model.PortalAccount{Username: "external-owner", IsActive: true, ClientID: &client.Id}
	db.Create(&a)
	other := model.PortalAccount{Username: "external-other", IsActive: true}
	db.Create(&other)
	compat := &subscriptioncompat.Service{DB: db}
	source, err := compat.CreateSource(nil, subscriptioncompat.SourceInput{Name: "Shared", Enabled: false, RefreshMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compat.ImportSnapshot(source.ID, nil, []byte(`proxies: [{name: shared, type: socks5, server: shared.test, port: 1080, password: public-test}]`)); err != nil {
		t.Fatal(err)
	}
	private, err := compat.CreateSource(&other.ID, subscriptioncompat.SourceInput{Name: "Private", Enabled: true, RefreshMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compat.ImportSnapshot(private.ID, &other.ID, []byte(`proxies: [{name: private, type: socks5, server: cross-account.test, port: 1080}]`)); err != nil {
		t.Fatal(err)
	}
	plan := model.PortalPlan{Name: "External plan", ExternalSelections: []model.ExternalSelection{{SourceID: source.ID, Mode: "all"}}}
	db.Create(&plan)
	db.Create(&model.PortalSubscription{AccountID: a.ID, PortalPlanSnapshot: model.PortalPlanSnapshot{PlanID: plan.ID}})
	out, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(client.SubID, "panel.example.test")
	if err != nil || !strings.Contains(out, "shared.test") || strings.Contains(out, "cross-account.test") {
		t.Fatalf("external-only projection=%s err=%v", out, err)
	}
	var document map[string]any
	if err = yaml.Unmarshal([]byte(out), &document); err != nil {
		t.Fatal(err)
	}
	if document["tun"] != nil || strings.Contains(out, "DOMAIN,shared.test,DIRECT") {
		t.Fatal("external-only subscription altered endpoint routing")
	}
	if document["proxy-groups"] == nil {
		t.Fatal("external-only subscription has no choices")
	}
	db.Model(&client).Update("enable", false)
	disabled, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(client.SubID, "panel.example.test")
	if err != nil || strings.Contains(disabled, "shared.test") {
		t.Fatal("disabled account retained external grant")
	}
}

func TestRetiredCommerceRoutesDoNotOverrideNativeSubscriptions(t *testing.T) {
	seedSubDB(t)
	db := database.GetDB()
	stream := `{"network":"xhttp","security":"none","xhttpSettings":{"path":"/native/","mode":"packet-up"}}`
	inbound := seedSubInbound(t, "native-route", "native-route", 26475, 0, stream)
	host := seedHost(t, &model.Host{InboundId: inbound.Id, Address: "native.test", Remark: "Native XHTTP"})
	archived := model.SubscriptionHost{HostID: host.Id, CascadeMode: "replace", EntryCiphertext: "unavailable", ClientStream: `{"address":"retired.test","network":"tcp"}`, MihomoOnly: true}
	if err := db.Create(&archived).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, _, err := NewSubService("").GetSubs("native-route", "panel.test")
	if err != nil || len(raw) == 0 || !strings.Contains(strings.Join(raw, ""), "native.test") {
		t.Fatalf("native raw subscription: %v %v", raw, err)
	}
	jsonBody, _, err := NewSubJsonService("", "", "", "", NewSubService("")).GetJson("native-route", "panel.test", false)
	if err != nil || !strings.Contains(jsonBody, "native.test") {
		t.Fatalf("native JSON subscription: %s %v", jsonBody, err)
	}
	clash, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash("native-route", "panel.test")
	if err != nil || !strings.Contains(clash, "/native/") || !strings.Contains(clash, "native.test") || strings.Contains(clash, "retired.test") || strings.Contains(clash, "dialer-proxy") {
		t.Fatalf("native Mihomo subscription: %s %v", clash, err)
	}
}

func TestXHTTPIPv6AuthoritiesPreservePaddingURLs(t *testing.T) {
	proxy := map[string]any{"server": "2001:db8::1"}
	NewSubClashService(false, "", false, NewSubService("")).applyTransport(proxy, "xhttp", map[string]any{"xhttpSettings": map[string]any{}})
	if proxy["xhttp-opts"].(map[string]any)["host"] != "[2001:db8::1]" {
		t.Fatalf("IPv6 upload authority = %#v", proxy)
	}
	got := buildXhttpClashOpts(map[string]any{"path": "/x/", "downloadSettings": map[string]any{"address": "::1", "port": 443, "security": "none", "xhttpSettings": map[string]any{"path": "/x/"}}})
	down := got["download-settings"].(map[string]any)
	if down["host"] != "[::1]" || down["server"] != "::1" || down["servername"] != "::1" {
		t.Fatalf("IPv6 download authority = %#v", down)
	}
}
