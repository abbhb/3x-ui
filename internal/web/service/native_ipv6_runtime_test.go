package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Explicitly invoked inside the disposable Linux network namespace. Regular
// unit tests never prepare addresses on the developer's host.
func TestNativeIPv6RuntimeFixture(t *testing.T) {
	root := os.Getenv("XUI_NATIVE_IPV6_LAB_ROOT")
	if root == "" {
		t.Skip("explicit disposable namespace required")
	}
	t.Setenv("XUI_DB_FOLDER", root)
	if os.Getenv("XUI_NATIVE_IPV6_LAB_VALIDATE") == "1" {
		if err := database.InitDB(filepath.Join(root, "panel.db")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.CloseDB() })
	} else {
		dbtest.InitDB(t, filepath.Join(root, "panel.db"))
	}
	ctx := context.Background()
	if os.Getenv("XUI_NATIVE_IPV6_LAB_VALIDATE") == "1" {
		raw, err := os.ReadFile(filepath.Join(root, "core.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := (&XraySettingService{}).SaveXraySetting(string(raw)); err != nil {
			t.Fatal(err)
		}
		value, err := (&SettingService{}).GetXrayConfigTemplate()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "core.json"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	db := database.GetDB()
	users := []model.ClientRecord{{Id: 7, Email: "7.sample", UUID: "11111111-1111-4111-8111-111111111111", Enable: true}, {Id: 9, Email: "9.sample", UUID: "22222222-2222-4222-8222-222222222222", Enable: true}}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	inbounds := []map[string]any{}
	for i, listen := range []string{"127.0.0.1", "2001:db8:aa::1"} {
		tag := []string{"LAB4", "LAB6"}[i]
		ib := &model.Inbound{Tag: tag, Remark: tag, Protocol: model.VLESS, Listen: listen, Port: 11080 + i, Settings: `{"clients":[],"decryption":"none"}`}
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
		peers := []map[string]any{}
		for _, user := range users {
			if err := db.Create(&model.ClientInbound{ClientId: user.Id, InboundId: ib.Id}).Error; err != nil {
				t.Fatal(err)
			}
			peers = append(peers, map[string]any{"email": user.Email, "id": user.UUID})
		}
		inbounds = append(inbounds, map[string]any{"tag": tag, "listen": listen, "port": 11080 + i, "protocol": "vless", "settings": map[string]any{"clients": peers, "decryption": "none"}})
	}
	pool := NativeIPv6Pool{Interface: os.Getenv("XUI_NATIVE_IPV6_LAB_INTERFACE"), Prefix: "2001:db8:bb::/64"}
	if err := ConfigureNativeIPv6Pool(ctx, pool); err != nil {
		t.Fatal(err)
	}
	leases, err := AllocateNativeIPv6(ctx, []int{7, 9})
	if err != nil {
		t.Fatal(err)
	}
	clients, err := NativeIPv6Clients()
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"api":       map[string]any{"tag": "api", "listen": "127.0.0.1:62789", "services": []string{"HandlerService", "StatsService", "RoutingService"}},
		"dns":       map[string]any{"hosts": map[string]any{"dual.test": []string{"192.0.2.2", "2001:db8:aa::2"}, "v4-only.test": "192.0.2.2"}},
		"inbounds":  inbounds,
		"outbounds": []map[string]any{{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "ForceIP", "finalRules": []map[string]any{{"action": "allow", "ip": []string{"2001:db8::/32", "192.0.2.0/24"}}}}}, {"tag": "blocked", "protocol": "blackhole"}},
		"routing":   map[string]any{"rules": []map[string]any{{"type": "field", "ip": []string{"2001:db8:dead::/48"}, "outboundTag": "blocked"}, {"type": "field", "inboundTag": []string{"LAB4", "LAB6"}, "outboundTag": "direct"}}},
	}
	raw, _ := json.Marshal(map[string]any{"template": config, "clients": clients, "leases": leases})
	if err := os.WriteFile(filepath.Join(root, "fixture.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
