package service

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Bypass the in-process traffic queue deliberately: separate SQL transactions
// must lock the same account before reading a balance or idempotency record.
func TestPortalPostgresTransactions(t *testing.T) {
	db := durablePostgresDB(t)
	suffix := time.Now().UnixNano()
	s := &CommerceService{}
	account := model.PortalAccount{Username: fmt.Sprintf("pg%d", suffix), IsActive: true, AuthVersion: 1}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	ib := durableTestInbound(nil, fmt.Sprintf("pg-portal-%d", suffix), 20000+int(suffix%20000))
	if err := db.Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	host := model.Host{InboundId: ib.Id, Address: "198.51.100.5"}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	plan, err := s.SavePlan(0, model.PortalPlan{Name: fmt.Sprintf("PG portal %d", suffix), PriceMinor: 1299, Currency: "CNY", DurationDays: 30, InboundIDs: []int{ib.Id}, HostIDs: []int{host.Id}, IsVisible: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean := db.Session(&gorm.Session{SkipHooks: true})
		var current model.PortalAccount
		clean.First(&current, account.ID)
		for _, m := range []any{&model.PortalAction{}, &model.PortalWalletTransaction{}, &model.PortalPurchase{}, &model.PortalSubscription{}, &model.PortalRuntimeChange{}} {
			clean.Where("account_id = ?", account.ID).Delete(m)
		}
		clean.Delete(&account)
		if current.ClientID != nil {
			clean.Where("client_id = ?", *current.ClientID).Delete(&model.PortalIdentity{})
			clean.Where("client_id = ?", *current.ClientID).Delete(&model.PortalAccess{})
			clean.Where("email LIKE ?", "%."+account.Username).Delete(&xray.ClientTraffic{})
			clean.Delete(&model.ClientRecord{}, *current.ClientID)
		}
		clean.Delete(plan)
		clean.Delete(&host)
		clean.Delete(ib)
	})
	if _, err = s.RunMutation(account.ID, "credit", PortalMutation{Kind: "admin_credit", AmountMinor: 2000, Actor: "synthetic-admin"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			var value PortalMutationResult
			results <- db.Transaction(func(tx *gorm.DB) error {
				return s.runPortalMutationTx(tx, account.ID, "same-purchase", PortalMutation{Kind: "self_purchase", PlanID: plan.ID}, &value)
			})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.Account(account.ID)
	if err != nil || view.WalletBalanceMinor != 701 {
		t.Fatal("cross-session replay charged multiple times")
	}
	var count int64
	db.Model(&model.PortalPurchase{}).Where("account_id = ?", account.ID).Count(&count)
	if count != 1 {
		t.Fatal("purchase was not unique")
	}
	if _, err = s.RunMutation(account.ID, "overspend", PortalMutation{Kind: "self_purchase", PlanID: plan.ID}); !errors.Is(err, ErrPortalBalance) {
		t.Fatal("PostgreSQL wallet overdrawn")
	}
	// A true COMMIT failure (not a statement failure) must roll back every side.
	installDeferredCommitFailure(t, db, "create", "portal-pg-deferred", "portal_wallet_transactions", "portal_pg_parent", "portal_pg_child")
	if _, err = s.RunMutation(account.ID, "commit-fails", PortalMutation{Kind: "admin_credit", AmountMinor: 50, Actor: "synthetic-admin"}); err == nil {
		t.Fatal("deferred commit failure was reported as success")
	}
	after, err := s.Account(account.ID)
	if err != nil || after.WalletBalanceMinor != 701 {
		t.Fatal("deferred commit failure retained the credited balance")
	}
}

// A dedicated schema keeps this fresh-target import independent of all existing
// PostgreSQL regression fixtures, and exercises explicit-ID sequence recovery.
func TestPortalPostgresOfflineImport(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_DB_DSN"))
	if os.Getenv("XUI_DB_TYPE") != "postgres" || dsn == "" {
		t.Skip("set a dedicated PostgreSQL test DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("portal_import_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; _ = sqlDB.Close() })
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("XUI_DB_DSN", dsn)
	TestPortalOfflineImportReadbackAndHistoricalReplay(t)
}
