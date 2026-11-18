package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func TestClientCatalogAndNativeLinksShareSubscriptionFormats(t *testing.T) {
	initSubDB(t)
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-source-key-for-integration-tests")
	db := database.GetDB()
	svc := &subscriptioncompat.Service{DB: db, ParseSource: ParseSourceSnapshot}
	client := model.ClientRecord{Email: "owner@test", SubID: "owner-native", Enable: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	owner := model.PortalAccount{Username: "owner", ClientID: &client.Id, IsActive: true}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	other := model.PortalAccount{Username: "other", IsActive: true}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	create := func(id *int, name, body string) model.SubscriptionSource {
		t.Helper()
		s, err := svc.CreateSource(id, subscriptioncompat.SourceInput{Name: name, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.ImportSnapshot(s.ID, id, []byte(body)); err != nil {
			t.Fatal(err)
		}
		return s
	}
	pub := create(nil, "Plan feed", `proxies:
- {name: selected, type: hysteria2, server: selected.test, port: 443, password: "demo:p", sni: tls.test}
- {name: omitted, type: trojan, server: omitted.test, port: 443, password: test}
- {name: advanced, type: ss, server: advanced.test, port: 443, cipher: aes-128-gcm, password: test, plugin: obfs, plugin-opts: {mode: http}}
`)
	var ids []int
	if err := db.Model(&model.SubscriptionNode{}).Where("source_id = ? AND name IN ?", pub.ID, []string{"selected", "advanced"}).Pluck("id", &ids).Error; err != nil || len(ids) != 2 {
		t.Fatalf("selected ids=%v err=%v", ids, err)
	}
	plan := model.PortalPlan{Name: "Premium", ExternalSelections: []model.ExternalSelection{{SourceID: pub.ID, Mode: "selected", NodeIDs: ids}}}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PortalSubscription{AccountID: owner.ID, PortalPlanSnapshot: model.PortalPlanSnapshot{PlanID: plan.ID}}).Error; err != nil {
		t.Fatal(err)
	}
	uri := "vless://11111111-1111-4111-8111-111111111111@private.test:443?security=tls&type=ws&host=cdn.test&path=%2Fedge#private"
	private := create(&owner.ID, "My source", base64.StdEncoding.EncodeToString([]byte(uri)))
	create(&other.ID, "Other source", "trojan://test@other.test:443#Other")
	direct := model.ClientExternalLink{ClientId: client.Id, Kind: model.ExternalLinkKindLink, Value: "trojan://native@direct.test:443#Direct", Remark: "Direct"}
	if err := db.Create(&direct).Error; err != nil {
		t.Fatal(err)
	}
	inventory, err := svc.ClientSourcesFor(client.Id, &owner.ID)
	if err != nil || !inventory.Linked || len(inventory.Sources) != 2 || len(inventory.Direct) != 1 {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
	metadata, _ := json.Marshal(inventory)
	if strings.Contains(string(metadata), "private.test") || strings.Contains(string(metadata), "11111111") || strings.Contains(string(metadata), "omitted") {
		t.Fatal("management response exposed credentials or an unassigned node")
	}
	if _, err := svc.ClientSourcesFor(client.Id, &other.ID); !errors.Is(err, subscriptioncompat.ErrNotFound) {
		t.Fatalf("cross-owner result %v", err)
	}
	base := NewSubService("")
	raw, _, _, _, err := base.GetSubs(client.SubID, "panel.test")
	if err != nil {
		t.Fatal(err)
	}
	js, _, err := NewSubJsonService("", "", "", "", base).GetJson(client.SubID, "panel.test", true)
	if err != nil {
		t.Fatal(err)
	}
	clash, _, err := NewSubClashService(false, "", false, base).GetClash(client.SubID, "panel.test")
	if err != nil {
		t.Fatal(err)
	}
	for format, out := range map[string]string{"uri": strings.Join(raw, "\n"), "json": js, "mihomo": clash} {
		for _, host := range []string{"selected.test", "private.test", "direct.test", "cdn.test"} {
			if !strings.Contains(out, host) {
				t.Errorf("%s missing %s", format, host)
			}
		}
		if strings.Contains(out, "advanced.test") != (format == "mihomo") || strings.Contains(out, "omitted.test") || strings.Contains(out, "other.test") {
			t.Errorf("incorrect selection in %s", format)
		}
	}
	if _, err := svc.UpdateSource(private.ID, &owner.ID, subscriptioncompat.SourceInput{Name: private.Name, GroupName: private.GroupName, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.PortalSubscription{}).Where("account_id = ?", owner.ID).Update("disabled_at", 1).Error; err != nil {
		t.Fatal(err)
	}
	raw, _, _, _, err = base.GetSubs(client.SubID, "panel.test")
	if err != nil || len(raw) != 1 || !strings.Contains(raw[0], "direct.test") {
		t.Fatalf("withdrawn grants persisted: count=%d err=%v", len(raw), err)
	}
}

func TestNativeClientURLAcceptsYAMLWithoutImportingUpstreamPolicy(t *testing.T) {
	initSubDB(t)
	resetSubscriptionCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies: [{name: Y, type: trojan, server: uri.test, port: 443, password: synthetic}, {name: M, type: socks5, server: yaml-only.test, port: 1080}]\nrules: [UPSTREAM-MARKER]\n"))
	}))
	defer srv.Close()
	db := database.GetDB()
	c := model.ClientRecord{Email: "standalone", SubID: "standalone", Enable: true}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientExternalLink{ClientId: c.Id, Kind: model.ExternalLinkKindSubscription, Value: srv.URL, NamePrefix: "feed/"}).Error; err != nil {
		t.Fatal(err)
	}
	base := NewSubService("")
	out, _, err := NewSubClashService(false, "", false, base).GetClash(c.SubID, "panel.test")
	if err != nil || !strings.Contains(out, "yaml-only.test") || !strings.Contains(out, "feed/M") || strings.Contains(out, "UPSTREAM-MARKER") {
		t.Fatalf("invalid YAML output: %v", err)
	}
	raw, _, _, _, err := base.GetSubs(c.SubID, "panel.test")
	if err != nil || len(raw) != 1 || !strings.Contains(raw[0], "uri.test") {
		t.Fatalf("native URI count=%d err=%v", len(raw), err)
	}
}

func TestSourceRefreshAcceptsBase64AndKeepsSnapshotOnInvalidInput(t *testing.T) {
	initSubDB(t)
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-source-key-for-integration-tests")
	body := base64.StdEncoding.EncodeToString([]byte("trojan://demo@feed.test:443#Test"))
	s := &subscriptioncompat.Service{DB: database.GetDB(), ParseSource: ParseSourceSnapshot, Fetch: func(context.Context, string) ([]byte, error) { return []byte(body), nil }}
	source, err := s.CreateSource(nil, subscriptioncompat.SourceInput{Name: "Private feed", URL: "https://provider.example.test/feed", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Refresh(context.Background(), source.ID, nil)
	if err != nil || first.NodeCount != 1 {
		t.Fatalf("refresh count=%d err=%v", first.NodeCount, err)
	}
	var old model.SubscriptionNode
	if err := s.DB.First(&old).Error; err != nil || old.ShareLinkCiphertext == "" || strings.Contains(old.ShareLinkCiphertext, "demo") {
		t.Fatalf("missing encrypted share link: %v", err)
	}
	body = "not a subscription"
	if _, err = s.Refresh(context.Background(), source.ID, nil); err == nil {
		t.Fatal("invalid refresh accepted")
	}
	var kept model.SubscriptionNode
	if err := s.DB.First(&kept, old.ID).Error; err != nil || !kept.IsActive || kept.ShareLinkCiphertext != old.ShareLinkCiphertext {
		t.Fatal("previous snapshot changed")
	}
}
