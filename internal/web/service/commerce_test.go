package service

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func portalFixture(t *testing.T) (*CommerceService, model.PortalAccount, model.PortalPlan) {
	t.Helper()
	setupBulkDB(t)
	s := &CommerceService{Now: func() time.Time { return time.Unix(1794820000, 0) }}
	account := model.PortalAccount{ID: 100, Username: "synthetic-buyer", IsActive: true, AuthVersion: 1}
	if err := database.GetDB().Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.PortalIdentity{ID: 11}).Error; err != nil {
		t.Fatal(err)
	}
	ib := mkInbound(t, 26411, model.VLESS, `{"clients":[],"decryption":"none"}`)
	host := model.Host{InboundId: ib.Id, Address: "synthetic.example.test", Remark: "fixture endpoint"}
	if err := database.GetDB().Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	plan, err := s.SavePlan(0, model.PortalPlan{Name: "Synthetic plan", PriceMinor: 1299, Currency: "CNY", DurationDays: 30, DataLimit: 123456, InboundIDs: []int{ib.Id}, HostIDs: []int{host.Id}, IsVisible: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, account, *plan
}

func TestPortalCommerceAtomicPurchaseReplayAndNativeIdentity(t *testing.T) {
	s, a, p := portalFixture(t)
	if _, err := s.RunMutation(a.ID, "credit-1", PortalMutation{Kind: "admin_credit", AmountMinor: 3000, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunMutation(a.ID, "purchase-1", PortalMutation{Kind: "self_purchase", PlanID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.WalletBalanceMinor != 1701 || result.Subscription == nil || result.Subscription.PricePaidMinor != 1299 || result.ClientID == nil || !result.RuntimePending {
		t.Fatalf("purchase result=%+v", result)
	}
	var client model.ClientRecord
	if err := database.GetDB().First(&client, *result.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if client.Email != "12.synthetic-buyer" || !client.StrictHostBindings || client.TotalGB != p.DataLimit || len(client.SubID) != 43 || client.UUID == "" {
		t.Fatalf("native client identity=%+v", client)
	}
	if _, err := s.RunMutation(a.ID, "credit-2", PortalMutation{Kind: "admin_credit", AmountMinor: 500, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	replay, err := s.RunMutation(a.ID, "purchase-1", PortalMutation{Kind: "self_purchase", PlanID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.PurchaseID != result.PurchaseID || replay.WalletBalanceMinor != result.WalletBalanceMinor {
		t.Fatalf("idempotent response changed: %+v", replay)
	}
	if _, err := s.RunMutation(a.ID, "purchase-1", PortalMutation{Kind: "admin_renewal", Days: 3, Actor: "synthetic-admin"}); !errors.Is(err, ErrPortalConflict) {
		t.Fatalf("key reuse=%v", err)
	}
	if _, err := s.RunMutation(a.ID, "purchase-2", PortalMutation{Kind: "self_purchase", PlanID: p.ID}); err != nil {
		t.Fatal(err)
	}
	var after model.ClientRecord
	if err := database.GetDB().First(&after, client.Id).Error; err != nil {
		t.Fatal(err)
	}
	if after.Email != client.Email || after.UUID != client.UUID || after.Auth != client.Auth || after.SubID != client.SubID {
		t.Fatal("purchase rotated permanent identity or credentials")
	}
	if _, err := s.RunMutation(a.ID, "purchase-too-much", PortalMutation{Kind: "self_purchase", PlanID: p.ID}); !errors.Is(err, ErrPortalBalance) {
		t.Fatalf("overspend=%v", err)
	}
}

func TestPortalCommerceRollsBackAllMoneyAndEntitlements(t *testing.T) {
	s, a, p := portalFixture(t)
	if _, err := s.RunMutation(a.ID, "credit", PortalMutation{Kind: "admin_credit", AmountMinor: 2000, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	const callback = "portal-fixture-fail-ledger"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "portal_wallet_transactions" {
			tx.AddError(errors.New("synthetic ledger unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	if _, err := s.RunMutation(a.ID, "purchase", PortalMutation{Kind: "self_purchase", PlanID: p.ID}); err == nil {
		t.Fatal("purchase succeeded without its ledger")
	}
	view, err := s.Account(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.WalletBalanceMinor != 2000 || view.ClientID != nil || view.Subscription != nil {
		t.Fatalf("transaction partially committed: %+v", view)
	}
	for _, table := range []any{&model.PortalPurchase{}, &model.PortalRuntimeChange{}, &model.ClientRecord{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial rows in %T: %d", table, count)
		}
	}
}

func TestPortalCommerceConcurrentBuyersCannotOverdrawOrReplayTwice(t *testing.T) {
	s, a, p := portalFixture(t)
	if _, err := s.RunMutation(a.ID, "credit", PortalMutation{Kind: "admin_credit", AmountMinor: 2000, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			_, err := s.RunMutation(a.ID, "same-purchase", PortalMutation{Kind: "self_purchase", PlanID: p.ID})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.Account(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.WalletBalanceMinor != 701 {
		t.Fatalf("balance=%d", view.WalletBalanceMinor)
	}
	var count int64
	if err := database.GetDB().Model(&model.PortalPurchase{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("purchases=%d", count)
	}
	var ledger model.PortalWalletTransaction
	if err := database.GetDB().Where("kind = ?", "purchase_debit").First(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Model(&ledger).Update("amount_minor", 0).Error; err == nil {
		t.Fatal("edited immutable ledger")
	}
	if err := database.GetDB().Delete(&ledger).Error; err == nil {
		t.Fatal("deleted immutable ledger")
	}
}

func TestPortalCommerceHiddenPlansSnapshotsAndResetBarrier(t *testing.T) {
	s, a, p := portalFixture(t)
	p.IsVisible = false
	if _, err := s.SavePlan(p.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunMutation(a.ID, "hidden", PortalMutation{Kind: "self_purchase", PlanID: p.ID}); !errors.Is(err, ErrPortalUnavailable) {
		t.Fatalf("hidden purchase=%v", err)
	}
	grant, err := s.RunMutation(a.ID, "grant", PortalMutation{Kind: "admin_grant", PlanID: p.ID, Actor: "synthetic-admin"})
	if err != nil {
		t.Fatal(err)
	}
	var rec model.ClientRecord
	if err := database.GetDB().First(&rec, *grant.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&InboundService{}).AddTraffic(nil, []*xray.ClientTraffic{{Email: rec.Email, Up: 100, Down: 200}}); err != nil {
		t.Fatal(err)
	}
	var traffic xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", rec.Email).First(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	if traffic.Up != 0 || traffic.Down != 0 {
		t.Fatal("old core counters were charged after the period reset")
	}
	if err := database.GetDB().Model(&model.PortalRuntimeChange{}).Where("client_id = ?", rec.Id).Updates(map[string]any{"local_reset_pending": false, "finished_at": s.now()}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&InboundService{}).AddTraffic(nil, []*xray.ClientTraffic{{Email: rec.Email, Up: 100, Down: 200}}); err != nil {
		t.Fatal(err)
	}
	p.Name = "Renamed plan"
	p.DataLimit = 999999
	if _, err := s.SavePlan(p.ID, p); err != nil {
		t.Fatal(err)
	}
	view, err := s.Account(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Subscription.PlanName != p.Name || view.Subscription.DataLimit == p.DataLimit || view.Usage.UsedTraffic != 300 {
		t.Fatalf("plan edit rewrote a snapshot: %+v", view)
	}
	if view.Subscription.PricePaidMinor != 0 {
		t.Fatal("admin grant charged the plan price")
	}
	wallet, err := s.Wallet(a.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallet) != 0 {
		t.Fatal("free grant changed wallet")
	}
	if strings.Contains(rec.Email, "100.") {
		t.Fatal("account ID was reused as proxy identity")
	}
}

func TestPortalPlanEditsReconcileRoutesAndPreserveFinancialTerms(t *testing.T) {
	s, a, p := portalFixture(t)
	result, err := s.RunMutation(a.ID, "grant", PortalMutation{Kind: "admin_grant", PlanID: p.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var before model.ClientRecord
	if err := database.GetDB().First(&before, *result.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	next := mkInbound(t, 26412, model.VLESS, `{"clients":[],"decryption":"none"}`)
	host := model.Host{InboundId: next.Id, Address: "198.51.100.9"}
	if err := database.GetDB().Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	p.InboundIDs = []int{next.Id}
	p.HostIDs = []int{host.Id}
	p.PriceMinor = 9999
	p.DataLimit = 500000
	p.DurationDays = 90
	if _, err := s.SavePlan(p.ID, p); err != nil {
		t.Fatal(err)
	}
	var after model.ClientRecord
	database.GetDB().First(&after, before.Id)
	if after.UUID != before.UUID || after.SubID != before.SubID || after.TotalGB != before.TotalGB || after.ExpiryTime != before.ExpiryTime {
		t.Fatal("route edit rewrote credentials or purchased financial terms")
	}
	var links []model.ClientInbound
	database.GetDB().Where("client_id = ?", after.Id).Find(&links)
	if len(links) != 1 || links[0].InboundId != next.Id || len(after.ClientHostRuleIds) != 1 || after.ClientHostRuleIds[0] != host.Id {
		t.Fatal("current route grants did not follow the plan edit")
	}
	var sub model.PortalSubscription
	database.GetDB().First(&sub, "account_id = ?", a.ID)
	if sub.DataLimit != before.TotalGB || sub.DurationDays != 30 || sub.PriceMinor != 1299 {
		t.Fatal("commercial snapshot was rewritten")
	}
}

func TestPortalPlanAddsMissingProtocolCredentialWithoutRotatingKnownIdentity(t *testing.T) {
	s, a, p := portalFixture(t)
	result, err := s.RunMutation(a.ID, "grant", PortalMutation{Kind: "admin_grant", PlanID: p.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var before model.ClientRecord
	if err := database.GetDB().First(&before, *result.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	// A source account with VLESS only has no HY2 credential to preserve.
	if err := database.GetDB().Model(&before).Update("auth", "").Error; err != nil {
		t.Fatal(err)
	}
	hy2 := mkInbound(t, 26415, model.Hysteria, `{"clients":[]}`)
	h := model.Host{InboundId: hy2.Id, Address: "198.51.100.10"}
	if err := database.GetDB().Create(&h).Error; err != nil {
		t.Fatal(err)
	}
	p.InboundIDs = append(p.InboundIDs, hy2.Id)
	p.HostIDs = append(p.HostIDs, h.Id)
	if _, err = s.SavePlan(p.ID, p); err != nil {
		t.Fatal(err)
	}
	var after model.ClientRecord
	if err := database.GetDB().First(&after, before.Id).Error; err != nil {
		t.Fatal(err)
	}
	if after.Auth == "" || after.UUID != before.UUID || after.SubID != before.SubID || after.Email != before.Email {
		t.Fatal("protocol addition lost a credential or rotated an existing identity")
	}
}
