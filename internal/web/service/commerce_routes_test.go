package service

import (
	"slices"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

func TestPlanConcreteRouteChangeRevokesSelectionButAllIncludesFuture(t *testing.T) {
	svc, a, p := portalFixture(t)
	db := database.GetDB()
	if _, err := svc.RunMutation(a.ID, "route-grant", PortalMutation{Kind: "admin_grant", PlanID: p.ID, Actor: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	var account model.PortalAccount
	db.First(&account, a.ID)
	var client model.ClientRecord
	db.First(&client, *account.ClientID)
	var host model.Host
	db.First(&host, p.HostIDs[0])
	host.Address = "changed.example.test"
	if err := db.Save(&host).Error; err != nil {
		t.Fatal(err)
	}
	grants, managed, err := subscriptioncompat.EffectiveHostGrants(db, client.Email)
	if err != nil || !managed || len(grants) != 0 {
		t.Fatalf("old selection silently adopted new endpoint: %v %v %v", grants, managed, err)
	}
	if err = svc.PrunePlanRoutes(); err != nil {
		t.Fatal(err)
	}
	var stale model.PortalPlan
	db.First(&stale, p.ID)
	if len(stale.InboundIDs) != 0 || stale.IsVisible {
		t.Fatalf("empty plan not hidden: %+v", stale)
	}
	p.HostSignatures = nil
	p.AutoHostInboundIDs = p.InboundIDs
	plan, err := svc.SavePlan(p.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	newHost := model.Host{InboundId: p.InboundIDs[0], Remark: "future route", Address: "future.example.test"}
	db.Create(&newHost)
	grants, managed, err = subscriptioncompat.EffectiveHostGrants(db, client.Email)
	if err != nil || !managed || !slices.Contains(grants, newHost.Id) || !slices.Contains(grants, host.Id) {
		t.Fatalf("all did not include new concrete host: %v %v", grants, err)
	}
	if len(plan.AutoHostInboundIDs) != 1 {
		t.Fatal("all scope lost")
	}
}
