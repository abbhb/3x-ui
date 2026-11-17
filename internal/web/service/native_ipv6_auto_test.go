package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func nativeAutoTemplate(inbound string) string {
	raw, _ := json.Marshal(map[string]any{
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "ForceIP", "finalRules": []any{map[string]any{"action": "block", "ip": []string{"geoip:private"}}}}},
			map[string]any{"tag": "blocked", "protocol": "blackhole"},
		},
		"routing": map[string]any{"domainStrategy": "IPOnDemand", "rules": []any{
			map[string]any{"type": "field", "domain": []string{"blocked.test"}, "outboundTag": "blocked"},
			map[string]any{"type": "field", "ruleTag": "user6-pending-" + inbound, "inboundTag": []string{inbound}, "ip": []string{"::/0"}, "outboundTag": "blocked"},
			map[string]any{"type": "field", "ruleTag": "user6-auto-" + inbound, "inboundTag": []string{inbound}, "outboundTag": "direct"},
		}},
	})
	return string(raw)
}

func TestNativeIPv6AutoReconcilesCanonicalGrantsAndPreservesNativeEdits(t *testing.T) {
	client, inbound, addresses, _ := nativeIPv6Fixture(t)
	raw := nativeAutoTemplate(inbound.Tag)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", raw); err != nil {
		t.Fatal(err)
	}
	first, err := reconcileNativeAutoIPv6(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	config, rules, _, err := nativeIPv6AutoScopes(first)
	if err != nil || len(rules) != 4 || rules[0].(map[string]any)["outboundTag"] != "blocked" || rules[2].(map[string]any)["ruleTag"] != "user6-pending-"+inbound.Tag {
		t.Fatalf("first-match restrictions/guard changed: %v %v", rules, err)
	}
	outbounds := config["outbounds"].([]any)
	generated := outbounds[2].(map[string]any)
	address := generated["sendThrough"].(string)
	if !addresses[address] || generated["settings"].(map[string]any)["finalRules"] == nil || generated["settings"].(map[string]any)["domainStrategy"] != nil {
		t.Fatalf("unprepared or weakened native outbound: %v", generated)
	}
	// A native outbound rename/disabled rule is an operator decision. Identity
	// follows the permanent local Client ID, without reassigning its address.
	generated["tag"] = "operator-name"
	userRule := rules[1].(map[string]any)
	userRule["outboundTag"], userRule["enabled"] = "operator-name", false
	if err := database.GetDB().Model(client).Update("email", "renamed").Error; err != nil {
		t.Fatal(err)
	}
	// Another service's client must not be enrolled here.
	foreign := model.ClientRecord{Email: "foreign"}
	if err := database.GetDB().Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	modified, _ := json.Marshal(config)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", string(modified)); err != nil {
		t.Fatal(err)
	}
	next, err := reconcileNativeAutoIPv6(context.Background(), string(modified))
	if err != nil {
		t.Fatal(err)
	}
	_, updated, _, _ := nativeIPv6AutoScopes(next)
	rule := updated[1].(map[string]any)
	if rule["enabled"] != false || rule["outboundTag"] != "operator-name" || nativeIPv6Strings(rule["user"])[0] != "renamed" {
		t.Fatalf("native edit or identity lost: %v", rule)
	}
	if again, err := reconcileNativeAutoIPv6(context.Background(), next); err != nil || again != next {
		t.Fatalf("reconcile not idempotent: %v", err)
	}
	var leases []model.NativeIPv6Lease
	if err := database.GetDB().Find(&leases).Error; err != nil || len(leases) != 1 || leases[0].Address != address {
		t.Fatalf("foreign allocation or changed lease: %v %v", leases, err)
	}
}

func TestNativeIPv6AutoPreparationFailureKeepsGuardAndStablePendingLease(t *testing.T) {
	_, inbound, _, fail := nativeIPv6Fixture(t)
	raw := nativeAutoTemplate(inbound.Tag)
	_ = (&SettingService{}).saveSetting("xrayTemplateConfig", raw)
	*fail = true
	if _, err := reconcileNativeAutoIPv6(context.Background(), raw); err == nil {
		t.Fatal("claimed ready after address failure")
	}
	saved, _ := (&SettingService{}).GetXrayConfigTemplate()
	if saved != raw {
		t.Fatal("failed preparation replaced the guarded native template")
	}
	report, _ := NativeIPv6PoolReport(context.Background())
	if len(report.Leases) != 1 || report.Leases[0].Ready {
		t.Fatalf("pending reservation lost: %v", report)
	}
	address := report.Leases[0].Address
	*fail = false
	ready, err := reconcileNativeAutoIPv6(context.Background(), raw)
	if err != nil || !strings.Contains(ready, address) {
		t.Fatalf("retry did not preserve the reservation: %v", err)
	}
}

func TestNativeIPv6AutoRefusesUnsafeScopeOrConcurrentOverwrite(t *testing.T) {
	_, inbound, _, _ := nativeIPv6Fixture(t)
	raw := nativeAutoTemplate(inbound.Tag)
	for _, bad := range []string{
		strings.Replace(raw, "user6-pending-", "removed-pending-", 1),
		strings.Replace(raw, `"::/0"`, `"192.0.2.0/24"`, 1),
		strings.Replace(raw, `"IPOnDemand"`, `"AsIs"`, 1),
		strings.Replace(raw, `"protocol":"freedom"`, `"protocol":"freedom","sendThrough":"10.66.66.3"`, 1),
	} {
		if _, _, _, err := nativeIPv6AutoScopes(bad); err == nil {
			t.Fatal("accepted an unguarded, bound or unresolved scope")
		}
	}
	manual := `{"outbounds":[],"routing":{"rules":[]}}`
	_ = (&SettingService{}).saveSetting("xrayTemplateConfig", manual)
	if _, err := reconcileNativeAutoIPv6(context.Background(), raw); err == nil {
		t.Fatal("overwrote a concurrent native save")
	}
	if saved, _ := (&SettingService{}).GetXrayConfigTemplate(); saved != manual {
		t.Fatal("concurrent operator template was lost")
	}
}
