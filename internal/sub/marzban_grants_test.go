package sub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// Python checks its SQLite conversion against the same fixture. This test
// imports that payload and exercises the real subscription host projection.
func TestMarzbanStrictGrantsImportAndSubscription(t *testing.T) {
	seedSubDB(t)
	db := database.GetDB()
	for _, ib := range []model.Inbound{
		{Id: 101, Tag: "grant-vision", Port: 26301, Protocol: model.VLESS, Enable: true, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"reality"}`},
		{Id: 102, Tag: "grant-xhttp", Port: 26302, Protocol: model.VLESS, Enable: true, Settings: `{"clients":[],"decryption":"none"}`, StreamSettings: `{"network":"xhttp","security":"tls"}`, DisableFlow: true},
		{Id: 103, Tag: "grant-hy2", Port: 26303, Protocol: model.Hysteria, Enable: true, Settings: `{"clients":[]}`, StreamSettings: `{"network":"hysteria","security":"tls"}`},
	} {
		if err := db.Create(&ib).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []model.Host{
		{Id: 1001, InboundId: 101, Remark: "not-in-plan", Address: "not-in-plan.example.test"},
		{Id: 1004, InboundId: 101, Remark: "granted", Address: "granted.example.test"},
		{Id: 1002, InboundId: 102, Remark: "denied-inbound", Address: "denied.example.test"},
		{Id: 1003, InboundId: 103, Remark: "hy2-granted", Address: "hy2.example.test"},
	} {
		if err := db.Create(&host).Error; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "web", "service", "testdata", "marzban_stage2_clients.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload []service.ClientCreatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	clients := &service.ClientService{}
	inbounds := &service.InboundService{}
	result, _, err := clients.ImportClients(inbounds, payload)
	if err != nil || result.Created != 2 || len(result.Skipped) != 0 {
		t.Fatalf("import=%+v err=%v", result, err)
	}
	var stored model.ClientRecord
	if err := db.Where("email = ?", payload[0].Client.Email).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.StrictHostBindings || !reflect.DeepEqual(stored.ClientHostRuleIds, []int{1003, 1004}) {
		t.Fatalf("grant projection lost: %+v", stored)
	}
	links, _, _, _, err := NewSubService("").GetSubs(payload[0].Client.SubID, "request.example.test")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "granted.example.test") || strings.Contains(joined, "not-in-plan.example.test") || strings.Contains(joined, "denied.example.test") {
		t.Fatalf("subscription grants broadened: %s", joined)
	}
	updated := stored.ToClient()
	updated.StrictHostBindings = false
	updated.Comment = "ordinary UI update"
	if _, err := clients.Update(inbounds, stored.Id, *updated, 0); err != nil {
		t.Fatal(err)
	}
	var after model.ClientRecord
	if err := db.First(&after, stored.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !after.StrictHostBindings {
		t.Fatal("ordinary edit downgraded strict migration mode")
	}
	var ib model.Inbound
	_ = db.First(&ib, 102).Error
	state, _ := NewSubService("").clientHostBinding(&ib, *after.ToClient())
	if state != hostBindingClosed {
		t.Fatal("missing inbound grant fell back to all endpoints")
	}
	empty := *after.ToClient()
	empty.ClientHostRuleIds = nil
	state, _ = NewSubService("").clientHostBinding(&ib, empty)
	if state != hostBindingClosed {
		t.Fatal("empty strict allowlist fell back to all endpoints")
	}
}
