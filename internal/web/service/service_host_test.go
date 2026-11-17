package service

import (
	"context"
	"encoding/json"
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
	// Old annotations may remain in an archived database, but no longer
	// participate in the current host contract or fresh schema.
	if err := db.Exec("CREATE TABLE service_lines (inbound_id INTEGER PRIMARY KEY, egress_mode TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO service_lines VALUES (?, ?)", local.Id, "retired-annotation").Error; err != nil {
		t.Fatal(err)
	}
	detail, err := svc.Get(host.Id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "physical-a" || len(detail.Services) != 2 || len(detail.Lines) != 2 {
		t.Fatalf("detail=%+v", detail)
	}
	if len(detail.Lines[0].Endpoints) != 2 || detail.Lines[0].Endpoints[1].Family != "IPv6" {
		t.Fatalf("entry classification=%+v", detail.Lines[0])
	}
	raw, _ := json.Marshal(detail)
	for _, secret := range []string{"synthetic-secret-token", "synthetic-private-uuid", "synthetic-private-auth", "settings", "egressMode", "retired-annotation"} {
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
	report hostops.Report
}

func (r *hostTestRuntime) HostReport(context.Context) (*hostops.Report, error) { return &r.report, nil }

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
