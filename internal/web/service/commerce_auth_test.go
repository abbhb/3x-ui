package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestPortalInvitationOneUseAndIndependentJWT(t *testing.T) {
	s, _, _ := portalFixture(t)
	t.Setenv("XUI_MGMA_TOKEN_PEPPER", "synthetic-pepper-minimum-thirty-two-bytes")
	p, _ := s.Settings()
	p.Enabled = true
	p.PublicURL = "https://portal.example.test"
	if err := s.SaveSettings(p); err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvitation(model.PortalInvitation{MaxUses: 1, ActorAdmin: "synthetic-admin"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := []model.PortalAccount{}
	for _, name := range []string{"Invited_A", "Invited_B"} {
		wg.Go(func() {
			a, err := s.Register(name, "synthetic-password", inv.Code, "192.0.2.1")
			if err == nil {
				mu.Lock()
				winners = append(winners, *a)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(winners) != 1 {
		t.Fatalf("successful registrations=%d", len(winners))
	}
	a := winners[0]
	if a.ClientID != nil {
		t.Fatal("registration created a proxy without a purchase")
	}
	logged, err := s.Authenticate(a.Username, "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.LoginToken(*logged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyLoginToken(token); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangePassword(a.ID, "synthetic-password", "synthetic-new-password", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyLoginToken(token); !errors.Is(err, ErrPortalAuth) {
		t.Fatal("password change retained an old session")
	}
	if _, err = bcrypt.Cost([]byte(portalDummyHash)); err != nil {
		t.Fatal("dummy hash does not perform bcrypt")
	}
}

func TestPortalManagedCreateRollbackRenewalAndQueuedPlan(t *testing.T) {
	s, _, p := portalFixture(t)
	if _, err := s.CreateManagedAccount("managed-buyer", p.ID+999, "admin"); err == nil {
		t.Fatal("missing plan accepted")
	}
	var count int64
	database.GetDB().Model(&model.PortalAccount{}).Where("username = ?", "managed-buyer").Count(&count)
	if count != 0 {
		t.Fatal("failed creation left an empty account")
	}
	result, err := s.CreateManagedAccount("managed-buyer", p.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	a := result["account"].(PortalAccountView)
	var rec model.ClientRecord
	database.GetDB().First(&rec, *a.ClientID)
	if !rec.RequireMGMA {
		t.Fatal("managed client lacks the node subscription fence")
	}
	database.GetDB().Model(&xray.ClientTraffic{}).Where("email = ?", rec.Email).Update("down", rec.TotalGB)
	if _, err = s.RunMutation(a.ID, "renew", PortalMutation{Kind: "admin_renewal", Days: 3, Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
	database.GetDB().First(&rec, rec.Id)
	if rec.Enable {
		t.Fatal("renewal re-enabled an exhausted quota")
	}
	// Preserve the historical add_remaining_traffic=true behavior: no carry.
	if err = s.SaveNextPlan(model.PortalNextPlan{ClientID: rec.Id, DataLimit: 100, Expire: time.Now().Add(time.Hour).Unix(), AddRemainingTraffic: true, FireOnEither: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyNextPlans(); err != nil {
		t.Fatal(err)
	}
	database.GetDB().First(&rec, rec.Id)
	if !rec.Enable || rec.TotalGB != 100 {
		t.Fatal("queued entitlement not applied")
	}
	var traffic xray.ClientTraffic
	database.GetDB().Where("email = ?", rec.Email).First(&traffic)
	if traffic.Down != 0 {
		t.Fatal("queued plan retained old usage")
	}
	view, err := s.Account(a.ID)
	if err != nil || view.Usage.LifetimeUsedTraffic != p.DataLimit {
		t.Fatal("usage reset history was lost")
	}
}

func TestPortalSecuritySourceAndDurableRecovery(t *testing.T) {
	s, _, _ := portalFixture(t)
	if !portalSourceAllowed("192.0.2.8", model.PortalSettings{SourceMode: "custom", CustomCIDRs: []string{"192.0.2.0/24"}}) || portalSourceAllowed("2001:db8::1", model.PortalSettings{SourceMode: "custom", CustomCIDRs: []string{"192.0.2.0/24"}}) {
		t.Fatal("CIDR source boundary failed")
	}
	r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r.RemoteAddr = "198.51.100.1:9000"
	r.Header.Set("X-Real-IP", "192.0.2.1")
	ip, err := PortalRealClientIP(r)
	if err != nil || ip != "198.51.100.1" {
		t.Fatal("TCP trusted a forged real IP")
	}
	t.Setenv("XUI_WEB_UNIX_SOCKET", "/tmp/synthetic.sock")
	r.RemoteAddr = "@"
	ip, err = PortalRealClientIP(r)
	if err != nil || ip != "192.0.2.1" {
		t.Fatal("configured Unix peer rejected the overwritten real IP")
	}
	block, err := s.AddIPBlock(model.PortalIPBlock{Network: "192.0.2.1", Reason: "synthetic", ActorAdmin: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.IPBlocked("192.0.2.1"); err != nil || !blocked {
		t.Fatal("block was not durable")
	}
	if err = s.RevokeIPBlock(block.ID); err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.IPBlocked("192.0.2.1"); err != nil || blocked {
		t.Fatal("recovery did not revoke the block")
	}
}

func TestPortalRenewalPreservesManualDisableAndAccumulatesDuration(t *testing.T) {
	s, a, p := portalFixture(t)
	result, err := s.RunMutation(a.ID, "grant", PortalMutation{Kind: "admin_grant", PlanID: p.ID, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunMutation(a.ID, "disable", PortalMutation{Kind: "admin_disable", Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveNextPlan(model.PortalNextPlan{ClientID: *result.ClientID, DataLimit: 100, Expire: 0, FireOnEither: true, AddRemainingTraffic: true}); err != nil {
		t.Fatal(err)
	}
	database.GetDB().Model(&xray.ClientTraffic{}).Where("email LIKE ?", "%."+a.Username).Update("down", p.DataLimit)
	if err = s.ApplyNextPlans(); err != nil {
		t.Fatal(err)
	}
	renewed, err := s.RunMutation(a.ID, "renew-disabled", PortalMutation{Kind: "admin_renewal", Days: 3, Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	var rec model.ClientRecord
	database.GetDB().First(&rec, *result.ClientID)
	if rec.Enable || renewed.Subscription.DurationDays != 33 || renewed.Subscription.DisabledAt == 0 {
		t.Fatal("renewal lost manual status or accumulated duration")
	}
}

func TestPortalIPBlockReusesNetworkAndRevocationClearsFailures(t *testing.T) {
	s, _, _ := portalFixture(t)
	first, err := s.AddIPBlock(model.PortalIPBlock{Network: "192.0.2.9", Reason: "first", ActorAdmin: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddIPBlock(model.PortalIPBlock{Network: "192.0.2.9/32", Reason: "replacement", ActorAdmin: "admin", ExpiresAt: s.now() + 60000})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Reason != "replacement" || second.ExpiresAt == 0 {
		t.Fatal("same network created conflicting duplicate blocks")
	}
	if err = s.RevokeIPBlock(second.ID); err != nil {
		t.Fatal(err)
	}
	if blocked, err := s.IPBlocked("192.0.2.9"); err != nil || blocked {
		t.Fatal("revoke left a hidden duplicate block")
	}
}
