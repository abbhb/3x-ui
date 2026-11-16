package sub

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

func stage4Import(t *testing.T) (string, []byte) {
	t.Helper()
	seedSubDB(t)
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-mgma-pepper-at-least-thirty-two-bytes")
	t.Setenv("XUI_MARZBAN_SUBSCRIPTION_KEY", "synthetic-legacy-key-at-least-thirty-two-bytes")
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	db := database.GetDB()
	for _, ib := range []model.Inbound{
		{Id: 101, Tag: "stage4-vision", Enable: true, Port: 26481, Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["vision.example.test"],"shortIds":["abcd"],"settings":{"publicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}}`},
		{Id: 102, Tag: "stage4-xhttp", Enable: true, Port: 26482, Protocol: model.VLESS, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"xhttp","security":"none","xhttpSettings":{"path":"/images/synthetic/"}}`, DisableFlow: true},
		{Id: 103, Tag: "stage4-hy2", Enable: true, Port: 26483, Protocol: model.Hysteria, Settings: `{"clients":[],"version":2}`, StreamSettings: `{"network":"hysteria","security":"tls"}`},
	} {
		if err := db.Create(&ib).Error; err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "web", "service", "testdata", "marzban_stage4_portal.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeStage4Bundle(t, root, raw)
	return root, raw
}

func writeStage4Bundle(t *testing.T, root string, raw []byte) {
	t.Helper()
	files := map[string][]byte{"portal.json": raw, "identities.json": []byte("[]"), "stage4-private.json": []byte("{}")}
	checks := map[string]string{}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		checks[name] = hashStage4(content)
	}
	manifest, _ := json.Marshal(map[string]any{"schema": "marzban-portal-to-3x-ui/v1", "ready_for_cutover": false, "client_count": 2, "account_count": 2, "files": checks})
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStage4AtomicImportProducesAuthorizedMihomoAndOldCredentials(t *testing.T) {
	root, _ := stage4Import(t)
	report, err := (&service.CommerceService{}).ImportPortalBundle(root, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReadyForCutover || report.VerifiedRows < 35 {
		t.Fatalf("import=%+v", report)
	}
	db := database.GetDB()
	var client model.ClientRecord
	if err = db.First(&client, 7).Error; err != nil {
		t.Fatal(err)
	}
	out, header, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(client.SubID, "panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2001:db8::8") || !strings.Contains(out, "private.example.test") || strings.Contains(out, "unselected.example.test") || strings.Contains(out, "Disabled profile") || !strings.Contains(out, client.UUID) || header == "" {
		t.Fatalf("imported authorized profile=%s header=%s", out, header)
	}
	if strings.Contains(out, "_mgma-") || !strings.Contains(out, "dialer-proxy:") || !strings.Contains(out, "download-settings:") {
		t.Fatal("cascade or XHTTP metadata was not materialized")
	}
	var doc map[string]any
	if err = yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	writeRuntimeProfile(t, "full-import.json", out)
	dns := doc["dns"].(map[string]any)
	if dns["ipv6"] != false || doc["ipv6"] != true {
		t.Fatalf("DNS IPv6 separation=%+v", dns)
	}
	s := &subscriptioncompat.Service{DB: db}
	nodes, err := s.Nodes(12, nil, 1)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("imported source=%+v %v", nodes, err)
	}
	if _, err = s.ImportSnapshot(12, nil, []byte(`proxies: [{name: 'Shared & source', type: socks5, server: entry.example.test, port: 1080, password: refreshed}]`)); err != nil {
		t.Fatal(err)
	}
	var stable model.SubscriptionNode
	if err = db.First(&stable, 31).Error; err != nil || stable.PayloadCiphertext == "" || !stable.IsActive {
		t.Fatal("source credential rotation lost selected permanent node ID")
	}
	db.Model(&model.PortalSettings{}).Where("id = 1").Update("enabled", true)
	old, err := (&service.CommerceService{}).Authenticate("sample_a", "synthetic-password")
	if err != nil || old.ID != 5 {
		t.Fatal("old login password changed")
	}
}

func TestStage4WrongExternalKeyRollsBackHostsAccountsAndMoney(t *testing.T) {
	root, _ := stage4Import(t)
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "wrong-synthetic-key-at-least-thirty-two-bytes")
	if _, err := (&service.CommerceService{}).ImportPortalBundle(root, true, false, false); err == nil {
		t.Fatal("wrong encryption key imported")
	}
	for _, table := range []any{&model.Host{}, &model.ClientRecord{}, &model.PortalAccount{}, &model.PortalWalletTransaction{}, &model.SubscriptionSource{}, &model.SubscriptionNode{}, &model.SubscriptionPolicy{}} {
		var n int64
		if err := database.GetDB().Model(table).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("partial import in %T: %d %v", table, n, err)
		}
	}
}

func hashStage4(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
