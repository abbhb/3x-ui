package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestInboundDialerEditIsolationAndRemoval(t *testing.T) {
	s := compatDB(t)
	inbound := model.Inbound{UserId: 13, Tag: "test-landing", Port: 34019, Protocol: model.VLESS}
	if err := s.DB.Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	initial, err := s.InboundDialer(13, inbound.Id)
	if err != nil || initial.Name != "" {
		t.Fatalf("new inbound: %v", err)
	}
	config := "- {name: first hop, type: socks5, server: hk.test, port: 2501.0, password: synthetic-private}"
	if _, err := s.SetInboundDialer(14, inbound.Id, initial.Revision, &config, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	saved, err := s.SetInboundDialer(13, inbound.Id, initial.Revision, &config, false)
	if err != nil || saved.Server != "hk.test" || saved.Port != 2501 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if _, err := s.SetInboundDialer(13, inbound.Id, initial.Revision, nil, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	inbound.Tag = "renamed"
	if err := s.DB.Save(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.First(&inbound, inbound.Id).Error; err != nil {
		t.Fatal(err)
	}
	if inbound.MihomoDialer == "" || strings.Contains(inbound.MihomoDialer, "synthetic-private") {
		t.Fatal("expected encrypted credential after ordinary inbound edit")
	}
	if b, _ := json.Marshal([]any{saved, inbound}); strings.Contains(string(b), "synthetic-private") || strings.Contains(string(b), inbound.MihomoDialer) {
		t.Fatal("credential visible in inbound detail or editor summary")
	}
	bad := "- {name: a, type: socks5, server: hk.test, port: 20}\n- {name: b}"
	if _, err := s.SetInboundDialer(13, inbound.Id, saved.Revision, &bad, false); err == nil {
		t.Fatal("multiple hops accepted")
	}
	cleared, err := s.SetInboundDialer(13, inbound.Id, saved.Revision, nil, true)
	if err != nil || cleared.Name != "" {
		t.Fatalf("clear: %v", err)
	}
}

func TestInboundDialerMigrationDoesNotRestoreRemovedCredentials(t *testing.T) {
	s := compatDB(t)
	inbound := model.Inbound{UserId: 1, Tag: "landing", Protocol: model.VLESS, Port: 34020}
	if err := s.DB.Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hop.yaml")
	if err := os.WriteFile(path, []byte("landing: {name: HK, type: socks5, server: hk.test, port: 80}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateInboundDialers(s.DB, path); err != nil {
		t.Fatal(err)
	}
	view, err := s.InboundDialer(1, inbound.Id)
	if err != nil || view.Name != "HK" {
		t.Fatalf("import: %v", err)
	}
	if _, err := s.SetInboundDialer(1, inbound.Id, view.Revision, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := MigrateInboundDialers(s.DB, path); err != nil {
		t.Fatal(err)
	}
	if view, err = s.InboundDialer(1, inbound.Id); err != nil || view.Name != "" {
		t.Fatal("cleared hop restored")
	}
}
