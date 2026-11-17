package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/hostops"
)

func TestServiceHostMembershipRoutesAndCredentialBoundary(t *testing.T) {
	setupBulkDB(t)
	db := database.GetDB()
	a := model.Node{Name: "service-a", Address: "127.0.0.1", Port: 2001, Enable: true, ApiToken: "synthetic-secret-token"}
	b := model.Node{Name: "service-b", Address: "127.0.0.2", Port: 2002, Enable: true}
	for _, node := range []*model.Node{&a, &b} {
		if err := db.Create(node).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := &ServiceHostService{}
	host, err := svc.Save(0, ServiceHostInput{Name: "physical-a", NodeIds: []int{0, a.Id}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Save(0, ServiceHostInput{Name: "physical-b", NodeIds: []int{b.Id}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(host.Id, ServiceHostInput{Name: "must-roll-back", NodeIds: []int{0, b.Id}}); err == nil {
		t.Fatal("stole another host's service")
	}
	local := mkInbound(t, 26201, model.VLESS, `{"clients":[{"id":"synthetic-private-uuid"}]}`)
	remote := mkInbound(t, 26202, model.Hysteria, `{"clients":[{"auth":"synthetic-private-auth"}]}`)
	if err := db.Model(remote).Update("node_id", a.Id).Error; err != nil {
		t.Fatal(err)
	}
	foreign := mkInbound(t, 26203, model.VLESS, `{"clients":[]}`)
	if err := db.Model(foreign).Update("node_id", b.Id).Error; err != nil {
		t.Fatal(err)
	}
	for _, entry := range []model.Host{{InboundId: local.Id, Remark: "v4", Address: "192.0.2.1"}, {InboundId: local.Id, Remark: "v6", Address: "2001:db8::a"}} {
		if err := db.Create(&entry).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Existing migration annotations remain readable, but are not editable
	// forwarding policies. A record for another host must stay out of this view.
	if err := db.Create(&[]model.ServiceLine{
		{InboundId: local.Id, EgressMode: "user_ipv6"},
		{InboundId: foreign.Id, EgressMode: "tunnel"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := svc.Get(host.Id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "physical-a" || len(detail.Services) != 2 || len(detail.Lines) != 2 {
		t.Fatalf("detail=%+v", detail)
	}
	if len(detail.Lines[0].Endpoints) != 2 || detail.Lines[0].Endpoints[1].Family != "IPv6" || detail.Lines[0].EgressMode != "user_ipv6" {
		t.Fatalf("entry classification=%+v", detail.Lines[0])
	}
	raw, _ := json.Marshal(detail)
	for _, secret := range []string{"synthetic-secret-token", "synthetic-private-uuid", "synthetic-private-auth", "settings"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("host DTO leaked %s", secret)
		}
	}
	if _, err := svc.Get(other.Id); err != nil {
		t.Fatal(err)
	}
}

type hostTestRuntime struct {
	fakeNodeRuntime
	report  hostops.Report
	applied int
}

func (r *hostTestRuntime) HostReport(context.Context) (*hostops.Report, error) { return &r.report, nil }

func (r *hostTestRuntime) SaveHostPolicy(context.Context, hostops.Policy) error {
	r.applied++
	return nil
}

func TestServiceHostRuntimeIsBoundedByMembershipAndKeepsUnavailableServices(t *testing.T) {
	setupBulkDB(t)
	mgr := useTestRuntimeManager(t)
	a := model.Node{Name: "host-runtime-a", Address: "127.0.0.1", Enable: true}
	b := model.Node{Name: "host-runtime-b", Address: "127.0.0.2", Enable: true}
	for _, node := range []*model.Node{&a, &b} {
		if err := database.GetDB().Create(node).Error; err != nil {
			t.Fatal(err)
		}
	}
	provider := &hostTestRuntime{report: hostops.Report{Installed: true}}
	mgr.SetRuntimeOverride(a.Id, provider)
	mgr.SetRuntimeOverride(b.Id, &fakeNodeRuntime{})
	svc := &ServiceHostService{}
	host, err := svc.Save(0, ServiceHostInput{Name: "runtime-machine", NodeIds: []int{a.Id, b.Id}})
	if err != nil {
		t.Fatal(err)
	}
	reports, err := svc.Runtime(context.Background(), host.Id)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports=%+v err=%v", reports, err)
	}
	if !reports[0].Installed || reports[1].Error == "" {
		t.Fatal("unavailable service was hidden or fabricated")
	}
	policy := hostops.Policy{Enabled: true, Namespace: "test", Interface: "eth0", Prefix: "2001:db8:1::/64", MarkPrefix: 100, Inbounds: []string{"entry"}}
	if err := svc.ApplyPolicy(context.Background(), host.Id, a.Id, policy); err != nil {
		t.Fatal(err)
	}
	if provider.applied != 1 {
		t.Fatal("selected service did not receive policy")
	}
	if err := svc.ApplyPolicy(context.Background(), host.Id, 999, policy); err == nil {
		t.Fatal("foreign service accepted")
	}
}

func TestLocalHostReportDoesNotVerifyAStoppedCore(t *testing.T) {
	setupBulkDB(t)
	t.Cleanup(SetXrayProcessForTest(nil))
	hostops.Observations.Reset()
	t.Cleanup(hostops.Observations.Reset)
	hostops.Observations.Feed(`MZCONN {"version":1,"event":"ready"}`)
	report := LocalHostReport()
	if report.Observer.Ready || report.Status.Verified || report.Status.Reason != "core_stopped" {
		t.Fatalf("stopped core was reported as ready: %+v", report)
	}
}

func TestDisabledHostPolicyCanReferToARemovedInbound(t *testing.T) {
	setupBulkDB(t)
	t.Setenv("XUI_EGRESS_CONFIG", "")
	t.Setenv("XUI_XRAY_LAUNCHER", "")
	policy := hostops.Policy{Namespace: "test", Interface: "eth0", Prefix: "2001:db8:1::/64", MarkPrefix: 100, Inbounds: []string{"removed-entry"}}
	if err := SaveLocalHostPolicy(policy); !errors.Is(err, hostops.ErrNotInstalled) {
		t.Fatalf("disable was blocked by removed inbound before loading bootstrap: %v", err)
	}
	policy.Enabled = true
	if err := SaveLocalHostPolicy(policy); err == nil || !strings.Contains(err.Error(), "local service inbounds") {
		t.Fatalf("enable accepted an unknown inbound: %v", err)
	}
}
