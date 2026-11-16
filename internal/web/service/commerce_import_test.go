package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func portalImportFixture(t *testing.T) string {
	t.Helper()
	setupBulkDB(t)
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-mgma-pepper-at-least-thirty-two-bytes")
	t.Setenv("XUI_MARZBAN_SUBSCRIPTION_KEY", "synthetic-legacy-key-at-least-thirty-two-bytes")
	for _, ib := range []model.Inbound{
		{Id: 101, Tag: "portal-import-vision", Enable: true, Port: 26101, Protocol: model.VLESS, Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp","security":"reality"}`},
		{Id: 102, Tag: "portal-import-xhttp", Enable: true, Port: 26102, Protocol: model.VLESS, Settings: `{"clients":[]}`, StreamSettings: `{"network":"xhttp","security":"tls"}`, DisableFlow: true},
		{Id: 103, Tag: "portal-import-hy2", Enable: true, Port: 26103, Protocol: model.Hysteria, Settings: `{"clients":[]}`, StreamSettings: `{"network":"hysteria","security":"tls"}`},
	} {
		if err := database.GetDB().Create(&ib).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		h := model.Host{Id: 1000 + i, InboundId: 100 + i, Address: "198.51.100.8", Remark: "synthetic only"}
		if err := database.GetDB().Create(&h).Error; err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	blob, err := os.ReadFile("testdata/marzban_stage3_portal.json")
	if err != nil {
		t.Fatal(err)
	}
	writePortalTestBundle(t, root, blob)
	return root
}

func writePortalTestBundle(t *testing.T, root string, blob []byte) {
	t.Helper()
	members := map[string][]byte{"portal.json": blob, "identities.json": []byte("[]"), "stage4-private.json": []byte("{}")}
	checks := map[string]string{}
	for name, data := range members {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		checks[name] = portalHash(string(data))
	}
	manifest, _ := json.Marshal(map[string]any{"schema": portalBundleSchema, "ready_for_cutover": false, "files": checks, "client_count": 2, "account_count": 2})
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPortalOfflineImportReadbackAndHistoricalReplay(t *testing.T) {
	root := portalImportFixture(t)
	s := &CommerceService{}
	if _, err := s.ImportPortalBundle(root, false, false, false); err == nil {
		t.Fatal("offline acknowledgement was not required")
	}
	report, err := s.ImportPortalBundle(root, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Clients != 2 || report.Accounts != 2 || report.VerifiedRows < 25 || report.ReadyForCutover {
		t.Fatalf("incomplete readback: %+v", report)
	}
	a, err := s.Account(5)
	if err != nil || a.ClientID == nil || a.WalletBalanceMinor != 1701 || a.UserID == nil || *a.UserID != 7 || a.Usage.UsedTraffic != 123456 || a.Usage.LifetimeUsedTraffic != 123553 {
		t.Fatal("import lost identity, balance, or usage history")
	}
	disabled, err := s.Account(6)
	if err != nil || disabled.IsActive || disabled.ClientID != nil {
		t.Fatal("disabled, unactivated customer was not preserved")
	}
	settings, err := s.Settings()
	if err != nil || settings.Enabled || settings.TTLSeconds != 0 || settings.Mode != "dual" {
		t.Fatal("import opened the portal or changed MGMA settings")
	}
	replay, err := s.RunMutation(5, "source-purchase", PortalMutation{Kind: "self_purchase", PlanID: 4})
	if err != nil || !replay.Replayed || replay.WalletBalanceMinor != 1701 {
		t.Fatal("old purchase was not idempotent after import")
	}
	after, _ := s.Account(5)
	if after.WalletBalanceMinor != 1701 {
		t.Fatal("old purchase charged again")
	}
	var rec model.ClientRecord
	database.GetDB().First(&rec, 9)
	if rec.Enable || !rec.RequireMGMA {
		t.Fatal("disabled imported native client became usable")
	}
	if _, err := s.ImportPortalBundle(root, true, false, false); err == nil {
		t.Fatal("reimport overwrote an existing target")
	}
	created, err := s.CreateManagedAccount("post-import-buyer", 4, "synthetic-admin")
	if err != nil {
		t.Fatal(err)
	}
	newAccount := created["account"].(PortalAccountView)
	if newAccount.ID <= 6 || newAccount.UserID == nil || *newAccount.UserID <= 9 {
		t.Fatal("explicit-ID import did not advance account or identity allocation")
	}
}

func TestPortalOfflineImportInvalidLastRecordRollsBackEverything(t *testing.T) {
	root := portalImportFixture(t)
	blob, err := os.ReadFile(filepath.Join(root, "portal.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A late historical-record failure must undo clients and the entire ledger.
	blob = []byte(strings.Replace(string(blob), `"used_traffic": 97`, `"used_traffic": -1`, 1))
	writePortalTestBundle(t, root, blob)
	if _, err := (&CommerceService{}).ImportPortalBundle(root, true, false, false); err == nil {
		t.Fatal("invalid usage history imported")
	}
	for _, m := range []any{&model.ClientRecord{}, &model.PortalAccount{}, &model.PortalPurchase{}, &model.PortalWalletTransaction{}, &model.PortalIdentity{}} {
		var count int64
		if err := database.GetDB().Model(m).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("offline import partially committed")
		}
	}
}

func TestPortalOfflineImportFingerprintsAndTamperBoundary(t *testing.T) {
	root := portalImportFixture(t)
	s := &CommerceService{}
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "wrong-synthetic-pepper-still-thirty-two-bytes")
	if _, err := s.ImportPortalBundle(root, true, false, false); err == nil {
		t.Fatal("mismatched pepper accepted")
	}
	t.Setenv("XUI_MARZBAN_SUBSCRIPTION_KEY", "")
	if _, err := s.ImportPortalBundle(root, true, false, true); err == nil {
		t.Fatal("missing legacy key accepted")
	}
	if _, err := s.ImportPortalBundle(root, true, true, true); err != nil {
		t.Fatal("explicit revocation was rejected")
	}
	var access model.PortalAccess
	database.GetDB().First(&access, "client_id = ?", 7)
	if access.Digest != "" {
		t.Fatal("explicit revoked bearer was retained")
	}
	p, _ := s.Settings()
	if p.Mode != "ephemeral" {
		t.Fatal("legacy revocation did not close legacy access")
	}
}

func TestPortalImportCLIRequiresAcknowledgementBeforeOpeningTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", root)
	if err := RunPortalImportCLI([]string{"--bundle", "missing-synthetic-bundle"}); err == nil {
		t.Fatal("offline acknowledgement was optional")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid CLI invocation created or migrated a target database")
	}
}
