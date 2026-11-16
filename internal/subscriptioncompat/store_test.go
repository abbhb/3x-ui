package subscriptioncompat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func compatDB(t *testing.T) *Service {
	t.Helper()
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "xui.db"))
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	return &Service{DB: database.GetDB()}
}

func TestExternalRefreshKeepsLastGoodAndSelectedIdentity(t *testing.T) {
	s := compatDB(t)
	source, err := s.CreateSource(nil, SourceInput{Name: "Airport", GroupName: "Airport", Enabled: true, RefreshMinutes: 15, URL: "https://synthetic.example.test/private?token=synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	good := []byte(`proxies: [{name: airport, type: socks5, server: synthetic.example.test, port: 1080, password: first}]`)
	if _, err = s.ImportSnapshot(source.ID, nil, good); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Nodes(source.ID, nil, 1)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%+v err=%v", nodes, err)
	}
	originalID := nodes[0].ID
	p := model.PortalPlan{Name: "External only", ExternalSelections: []model.ExternalSelection{{SourceID: source.ID, Mode: "selected", NodeIDs: []int{originalID}}}}
	if err = s.DB.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	s.Fetch = func(context.Context, string) ([]byte, error) { return []byte("invalid"), nil }
	if _, err = s.Refresh(context.Background(), source.ID, nil); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("invalid refresh=%v", err)
	}
	var saved model.SubscriptionSource
	s.DB.First(&saved, source.ID)
	if saved.NodeCount != 1 || saved.LastSuccessAt == 0 || saved.LastErrorCode == "" {
		t.Fatalf("LKG lost=%+v", saved)
	}
	s.Fetch = func(context.Context, string) ([]byte, error) {
		return []byte(strings.ReplaceAll(string(good), "first", "second")), nil
	}
	if _, err = s.Refresh(context.Background(), source.ID, nil); err != nil {
		t.Fatal(err)
	}
	nodes, _ = s.Nodes(source.ID, nil, 1)
	if len(nodes) != 1 || nodes[0].ID != originalID {
		t.Fatalf("credential replacement changed selection=%+v", nodes)
	}
	b, _ := json.Marshal(nodes)
	if strings.Contains(string(b), "password") || strings.Contains(string(b), "synthetic.example") || strings.Contains(string(b), "Ciphertext") {
		t.Fatalf("metadata leaked: %s", b)
	}
	if err = s.DeleteSource(source.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted granted source: %v", err)
	}
	if _, err = s.ImportSnapshot(source.ID, nil, []byte(`proxies: [{name: replacement, type: ss, server: replacement.test, port: 443}]`)); err != nil {
		t.Fatal(err)
	}
	var tomb model.SubscriptionNode
	s.DB.First(&tomb, originalID)
	if tomb.IsActive {
		t.Fatal("missing explicitly selected node remained active")
	}
}

func TestPrivateSourceCannotCrossAccountsOrBecomePlanGrant(t *testing.T) {
	s := compatDB(t)
	a := model.PortalAccount{Username: "owner", IsActive: true}
	b := model.PortalAccount{Username: "other", IsActive: true}
	s.DB.Create(&a)
	s.DB.Create(&b)
	source, err := s.CreateSource(&a.ID, SourceInput{Name: "Private", GroupName: "Private", Enabled: true, RefreshMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportSnapshot(source.ID, &b.ID, []byte(`proxies: [{name: private, type: socks5, server: private.test, port: 1080}]`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account import=%v", err)
	}
	if err = ValidateSelections(s.DB, []model.ExternalSelection{{SourceID: source.ID, Mode: "all"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("private source granted to public plan: %v", err)
	}
	if _, err = s.ReplaceCredential(source.ID, &b.ID, "https://synthetic.example.test/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account credential=%v", err)
	}
}
