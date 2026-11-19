package service

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// Re-importing clients must preserve usage accrued since the initial import.
func TestClientImportPreservesIdentityAndTraffic(t *testing.T) {
	setupBulkDB(t)
	db := database.GetDB()
	for _, inbound := range []model.Inbound{
		{
			Id: 101, Tag: "test-migration-vision", Enable: true, Port: 26101, Protocol: model.VLESS,
			Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp","security":"reality"}`,
		},
		{
			Id: 102, Tag: "test-migration-xhttp", Enable: true, Port: 26102, Protocol: model.VLESS,
			Settings: `{"clients":[]}`, StreamSettings: `{"network":"xhttp","security":"tls"}`, DisableFlow: true,
		},
		{
			Id: 103, Tag: "test-migration-hy2", Enable: true, Port: 26103, Protocol: model.Hysteria,
			Settings: `{"clients":[]}`, StreamSettings: `{"network":"hysteria","security":"tls"}`,
		},
	} {
		if err := db.Create(&inbound).Error; err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("testdata/client_import_identities.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []ClientCreatePayload
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	svc, inboundSvc := &ClientService{}, &InboundService{}
	result, _, err := svc.ImportClients(inboundSvc, items)
	if err != nil || result.Created != len(items) || len(result.Skipped) != 0 {
		t.Fatalf("native import: result=%+v err=%v", result, err)
	}
	for _, item := range items {
		want := item.Client
		record := lookupClientRecord(t, want.Email)
		if record.Email != want.Email || record.UUID != want.ID || record.Auth != want.Auth ||
			record.SubID != want.SubID || record.Enable != want.Enable ||
			record.TotalGB != want.TotalGB || record.ExpiryTime != want.ExpiryTime || record.TrafficReset != "never" {
			t.Fatalf("identity, credentials, state, or quota changed for synthetic client %s: %+v", want.Email, record)
		}
		var links []model.ClientInbound
		if err := db.Where("client_id = ?", record.Id).Order("inbound_id ASC").Find(&links).Error; err != nil {
			t.Fatal(err)
		}
		ids := make([]int, 0, len(links))
		for _, link := range links {
			ids = append(ids, link.InboundId)
			flow := ""
			if link.InboundId == 101 {
				flow = "xtls-rprx-vision"
			}
			if link.FlowOverride != flow {
				t.Fatalf("inbound %d flow=%q, want %q", link.InboundId, link.FlowOverride, flow)
			}
		}
		if !reflect.DeepEqual(ids, item.InboundIds) {
			t.Fatalf("permission projection=%v, want %v", ids, item.InboundIds)
		}
		var traffic xray.ClientTraffic
		if err := db.Where("email = ?", want.Email).First(&traffic).Error; err != nil {
			t.Fatal(err)
		}
		if traffic.Up != item.Traffic.Up || traffic.Down != item.Traffic.Down ||
			traffic.Total != want.TotalGB || traffic.ExpiryTime != want.ExpiryTime || traffic.Enable != want.Enable {
			t.Fatalf("accounting baseline changed for %s: %+v", want.Email, traffic)
		}
	}
	// Importing the same preparation bundle must never reset usage accrued later.
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", items[0].Client.Email).
		Updates(map[string]any{"up": 9000, "down": 200000}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ImportClients(inboundSvc, items); err != nil {
		t.Fatal(err)
	}
	var live xray.ClientTraffic
	if err := db.Where("email = ?", items[0].Client.Email).First(&live).Error; err != nil {
		t.Fatal(err)
	}
	if live.Up != 9000 || live.Down != 200000 {
		t.Fatalf("re-import rewrote live counters: %+v", live)
	}
}
