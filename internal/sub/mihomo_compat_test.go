package sub

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func TestMihomoIntegerScalarPreservesExactValues(t *testing.T) {
	for _, test := range []struct{ input, want any }{
		{float64(1000000), "1000000"}, {float64(30), "30"},
		{"1e+06", "1000000"}, {"3E+01", "30"}, {"30.0", "30"},
		{json.Number("9.007199254740993e15"), "9007199254740993"},
		{"9223372036854775807.0", "9223372036854775807"},
		{"524288-1048576", "524288-1048576"}, {"25-35", "25-35"},
		{float64(30.5), float64(30.5)}, {"3.05e1", "3.05e1"},
		{"1e999999999", "1e999999999"}, {"1e129", "1e129"},
		{"9223372036854775808.0", "9223372036854775808.0"},
		{"1.0/1", "1.0/1"}, {true, true}, {nil, nil},
	} {
		if got := mihomoIntegerScalar(test.input); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%#v became %#v, want %#v", test.input, got, test.want)
		}
	}
}

func TestMihomoConfigNormalizesInlineProvidersAndPreservesProxyOptions(t *testing.T) {
	proxy := map[string]any{"name": "explicit", "type": "vless", "tls": true, "client-fingerprint": "firefox", "xhttp-opts": map[string]any{"sc-max-each-post-bytes": float64(1000000), "sc-min-posts-interval-ms": "30", "x-padding-bytes": "100-1000", "reuse-settings": map[string]any{"h-keep-alive-period": 30.5}}}
	inline := map[string]any{"name": "inline", "type": "vmess", "tls": true, "xhttp-opts": map[string]any{"sc-max-each-post-bytes": "1e+06", "sc-min-posts-interval-ms": float64(30)}}
	plain := map[string]any{"name": "plain", "type": "vless", "tls": false}
	quic := map[string]any{"name": "quic", "type": "hysteria2"}
	anytls := map[string]any{"name": "anytls", "type": "anytls"}
	http := map[string]any{"name": "http", "type": "http", "tls": true}
	config := map[string]any{"global-client-fingerprint": "chrome", "proxies": []map[string]any{proxy, plain, quic, anytls, http}, "proxy-providers": map[string]any{"inline": map[string]any{"type": "inline", "payload": []any{inline}}}}
	normalizeMihomoConfig(config)
	if _, exists := config["global-client-fingerprint"]; exists {
		t.Fatal("deprecated global fingerprint escaped")
	}
	if proxy["client-fingerprint"] != "firefox" || inline["client-fingerprint"] != "chrome" || anytls["client-fingerprint"] != "chrome" || plain["client-fingerprint"] != nil || quic["client-fingerprint"] != nil || http["client-fingerprint"] != nil {
		t.Fatal("fingerprint fallback changed explicit or unsupported proxy settings")
	}
	opts := proxy["xhttp-opts"].(map[string]any)
	if opts["sc-max-each-post-bytes"] != "1000000" || opts["x-padding-bytes"] != "100-1000" || opts["reuse-settings"].(map[string]any)["h-keep-alive-period"] != 30.5 {
		t.Fatal("XHTTP normalization changed an unrelated option")
	}
	if inline["xhttp-opts"].(map[string]any)["sc-min-posts-interval-ms"] != "30" {
		t.Fatal("inline provider was not normalized")
	}
}

func TestMihomoExternalXHTTPImportProducesValidIntegerScalars(t *testing.T) {
	seedSubDB(t)
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	seedSubInbound(t, "external-xhttp", "local", 26811, 0, `{"network":"tcp","security":"none"}`)
	db := database.GetDB()
	var client model.ClientRecord
	if err := db.Where("sub_id = ?", "external-xhttp").First(&client).Error; err != nil {
		t.Fatal(err)
	}
	account := model.PortalAccount{Username: "synthetic-xhttp", ClientID: &client.Id, IsActive: true}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	svc := &subscriptioncompat.Service{DB: db}
	source, err := svc.CreateSource(&account.ID, subscriptioncompat.SourceInput{Name: "Synthetic external", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const snapshot = `{"proxies":[
		{"name":"Numeric","type":"vless","server":"numeric.example.test","port":443,"uuid":"11111111-1111-4111-8111-111111111111","tls":true,"network":"xhttp","xhttp-opts":{"path":"/","mode":"packet-up","sc-max-each-post-bytes":1000000.0,"sc-min-posts-interval-ms":30.0}},
		{"name":"Scientific","type":"vless","server":"scientific.example.test","port":443,"uuid":"11111111-1111-4111-8111-111111111111","tls":true,"network":"xhttp","xhttp-opts":{"path":"/","mode":"packet-up","sc-max-each-post-bytes":"1e+06","sc-min-posts-interval-ms":"3E+01"}},
		{"name":"Range","type":"vless","server":"range.example.test","port":443,"uuid":"11111111-1111-4111-8111-111111111111","tls":true,"client-fingerprint":"chrome","network":"xhttp","xhttp-opts":{"path":"/","mode":"packet-up","sc-max-each-post-bytes":"524288-1048576","sc-min-posts-interval-ms":"25-35"}}
	]}`
	if _, err := svc.ImportSnapshot(source.ID, &account.ID, []byte(snapshot)); err != nil {
		t.Fatal(err)
	}
	policy := model.SubscriptionPolicy{ID: 1, Enabled: true, PrimaryGroup: "PROXY", Document: `{"global-client-fingerprint":"firefox","proxy-groups":[{"name":"PROXY","type":"select","proxies":["__MGMA_LOCAL_NODES__","MGMA · External"]}],"rules":["MATCH,PROXY"]}`}
	if err := db.Save(&policy).Error; err != nil {
		t.Fatal(err)
	}
	body, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(client.SubID, "synthetic.example.test")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["global-client-fingerprint"]; exists {
		t.Fatal("imported policy leaked the removed global fingerprint")
	}
	proxies, _ := asAnySlice(doc["proxies"])
	if len(proxies) != 4 {
		t.Fatalf("authorized imported proxies = %d, want 4", len(proxies))
	}
	for _, raw := range proxies {
		proxy := raw.(map[string]any)
		if proxy["network"] != "xhttp" {
			continue
		}
		opts := proxy["xhttp-opts"].(map[string]any)
		bytes, interval, fingerprint := "1000000", "30", "firefox"
		if strings.HasSuffix(proxy["name"].(string), "Range") {
			bytes, interval, fingerprint = "524288-1048576", "25-35", "chrome"
		}
		if opts["sc-max-each-post-bytes"] != bytes || opts["sc-min-posts-interval-ms"] != interval || proxy["client-fingerprint"] != fingerprint {
			t.Fatalf("invalid XHTTP wire options: %#v", proxy)
		}
	}
	writeRuntimeProfile(t, "external-xhttp-import.json", body)
}
