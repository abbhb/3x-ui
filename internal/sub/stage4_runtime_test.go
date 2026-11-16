package sub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func writeRuntimeProfile(t *testing.T, name, profile string) {
	t.Helper()
	root := os.Getenv("XUI_TEST_SUBSCRIPTION_OUTPUT")
	if root == "" {
		return
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(profile), &doc); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runtimePort(t *testing.T, key string) int {
	t.Helper()
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 1024 || n > 65535 {
		t.Fatal("invalid synthetic runtime port")
	}
	return n
}

func TestStage4RuntimeFixtures(t *testing.T) {
	if os.Getenv("XUI_TEST_SUBSCRIPTION_OUTPUT") == "" {
		t.Skip("set XUI_TEST_SUBSCRIPTION_OUTPUT for real-core runtime lab")
	}
	seedSubDB(t)
	db := database.GetDB()
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	landingPort := runtimePort(t, "XUI_TEST_CASCADE_LANDING_PORT")
	entryPort := runtimePort(t, "XUI_TEST_CASCADE_ENTRY_PORT")
	ib := model.Inbound{Tag: "synthetic-cascade-runtime", Listen: "127.0.0.1", Port: landingPort, Enable: true, Protocol: model.Shadowsocks, Settings: `{"method":"aes-128-gcm","password":"test-landing-only","clients":[{"email":"runtime-cascade","password":"test-landing-only","enable":true}]}`, StreamSettings: `{"network":"tcp","security":"none"}`}
	if err := db.Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	rec := model.ClientRecord{Email: "runtime-cascade", SubID: "runtime-cascade", Password: "test-landing-only", Enable: true}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}
	h := model.Host{InboundId: ib.Id, Address: "127.0.0.1", Remark: "Runtime landing"}
	if err := db.Create(&h).Error; err != nil {
		t.Fatal(err)
	}
	compat := &subscriptioncompat.Service{DB: db}
	if _, err := compat.SaveHost(h.Id, subscriptioncompat.HostInput{CascadeMode: "replace", EntryYAML: fmt.Sprintf("{name: Runtime entry, type: socks5, server: 127.0.0.1, port: %d}", entryPort)}); err != nil {
		t.Fatal(err)
	}
	body, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(rec.SubID, "synthetic.example.test")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeProfile(t, "cascade.json", body)
	up := runtimePort(t, "XUI_TEST_XHTTP_UP_PORT")
	down := runtimePort(t, "XUI_TEST_XHTTP_DOWN_PORT")
	stream := fmt.Sprintf(`{"network":"xhttp","security":"none","xhttpSettings":{"path":"/runtime-xhttp/","mode":"packet-up","extra":{"xmux":{"maxConcurrency":"4-8"},"downloadSettings":{"address":"::1","port":%d,"network":"xhttp","security":"none","xhttpSettings":{"path":"/runtime-xhttp/"}}}}}`, down)
	xhttp := seedSubInbound(t, "runtime-xhttp", "runtime-xhttp", up, 0, stream)
	xhttp.Listen = "127.0.0.1"
	if err := db.Save(xhttp).Error; err != nil {
		t.Fatal(err)
	}
	seedHost(t, &model.Host{InboundId: xhttp.Id, Address: "127.0.0.1", Remark: "Runtime XHTTP"})
	body, _, err = NewSubClashService(false, "", false, NewSubService("")).GetClash("runtime-xhttp", "synthetic.example.test")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeProfile(t, "xhttp.json", body)
	core := map[string]any{"log": map[string]any{"loglevel": "debug"}, "inbounds": []any{xhttp.GenXrayInboundConfig()}, "outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}}
	raw, err := json.Marshal(core)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(os.Getenv("XUI_TEST_SUBSCRIPTION_OUTPUT"), "xray.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
