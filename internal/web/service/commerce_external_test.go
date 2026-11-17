package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func TestPortalExternalOnlyPlanChargesOnceWithoutGrantingLocalListeners(t *testing.T) {
	svc, a, _ := portalFixture(t)
	db := database.GetDB()
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	external := &subscriptioncompat.Service{DB: db}
	source, err := external.CreateSource(nil, subscriptioncompat.SourceInput{Name: "External grant", Enabled: true, RefreshMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = external.ImportSnapshot(source.ID, nil, []byte(`proxies: [{name: external, type: socks5, server: shared.test, port: 1080}]`)); err != nil {
		t.Fatal(err)
	}
	p, err := svc.SavePlan(0, model.PortalPlan{Name: "External only", Currency: "CNY", PriceMinor: 199, DurationDays: 30, ExternalSelections: []model.ExternalSelection{{SourceID: source.ID, Mode: "all"}}, IsVisible: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RunMutation(a.ID, "external-credit", PortalMutation{Kind: "admin_credit", AmountMinor: 500, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	purchase, err := svc.RunMutation(a.ID, "external-buy", PortalMutation{Kind: "self_purchase", PlanID: p.ID})
	if err != nil || purchase.ClientID == nil || purchase.WalletBalanceMinor != 301 {
		t.Fatalf("external purchase=%+v err=%v", purchase, err)
	}
	var links int64
	if err = db.Model(&model.ClientInbound{}).Where("client_id = ?", *purchase.ClientID).Count(&links).Error; err != nil || links != 0 {
		t.Fatalf("external purchase granted local listeners: %d %v", links, err)
	}
	again, err := svc.RunMutation(a.ID, "external-buy", PortalMutation{Kind: "self_purchase", PlanID: p.ID})
	if err != nil || !again.Replayed || again.WalletBalanceMinor != 301 {
		t.Fatalf("external replay=%+v err=%v", again, err)
	}
}

func TestPortalArchivedPlanCanReplaceItsLegacyMarkerWithExternalEntitlements(t *testing.T) {
	svc, account, _ := portalFixture(t)
	db := database.GetDB()
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	ext := &subscriptioncompat.Service{DB: db}
	source, err := ext.CreateSource(nil, subscriptioncompat.SourceInput{Name: "Configured external", Enabled: true, RefreshMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ext.ImportSnapshot(source.ID, nil, []byte(`proxies: [{name: external, type: socks5, server: example.test, port: 1080}]`)); err != nil {
		t.Fatal(err)
	}
	plan := model.PortalPlan{Name: "Archived empty plan", Currency: "CNY", DurationDays: 30, CompatibilityPending: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunMutation(account.ID, "empty-grant", PortalMutation{Kind: "admin_grant", PlanID: plan.ID, Actor: "test-admin"}); err == nil {
		t.Fatal("empty legacy plan was grantable")
	}
	rows, err := svc.Plans(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == plan.ID && row.HasRoutes {
			t.Fatal("empty plan reported configured entitlements")
		}
	}
	plan.ExternalSelections = []model.ExternalSelection{{SourceID: source.ID, Mode: "all"}}
	plan.IsVisible = true
	saved, err := svc.SavePlan(plan.ID, plan)
	if err != nil || !saved.HasRoutes || saved.CompatibilityPending {
		t.Fatalf("could not configure archived plan: %+v %v", saved, err)
	}
	public, err := svc.Plans(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range public {
		if row.ID == plan.ID && (!row.HasRoutes || len(row.ExternalSelections) != 0) {
			t.Fatal("public plan lost availability or leaked private selections")
		}
	}
	result, err := svc.RunMutation(account.ID, "external-grant", PortalMutation{Kind: "admin_grant", PlanID: plan.ID, Actor: "test-admin"})
	if err != nil || result.ClientID == nil {
		t.Fatalf("configured external grant failed: %+v %v", result, err)
	}
}
