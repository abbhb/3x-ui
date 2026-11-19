package sub

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// These rows exercise credentials and encrypted snapshots already stored by earlier versions.
func seedPersistedSubscriptions(t *testing.T) {
	t.Helper()
	seedSubDB(t)
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-mgma-pepper-at-least-thirty-two-bytes")
	t.Setenv("XUI_MARZBAN_SUBSCRIPTION_KEY", "synthetic-legacy-key-at-least-thirty-two-bytes")
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	raw, err := os.ReadFile("testdata/persisted_subscriptions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Exec(string(raw)).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPersistedSubscriptionsAndCredentials(t *testing.T) {
	seedPersistedSubscriptions(t)
	db := database.GetDB()
	var client model.ClientRecord
	if err := db.First(&client, 7).Error; err != nil {
		t.Fatal(err)
	}
	out, header, err := NewSubClashService(false, "", false, NewSubService("")).GetClash(client.SubID, "panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "host-2.example.test") || !strings.Contains(out, "private.example.test") || strings.Contains(out, "unselected.example.test") || strings.Contains(out, "Disabled profile") || !strings.Contains(out, client.UUID) || header == "" {
		t.Fatalf("imported authorized profile=%s header=%s", out, header)
	}
	if strings.Contains(out, "_mgma-") || strings.Contains(out, "dialer-proxy:") || strings.Contains(out, "2001:db8::8") {
		t.Fatal("retired route override was applied")
	}
	var doc map[string]any
	if err = yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	writeRuntimeProfile(t, "persisted-subscription.json", out)
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
