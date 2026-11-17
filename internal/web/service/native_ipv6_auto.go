package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Auto-enrolment is expressed by ordinary, visible native routing rules. The
// pending IPv6 blackhole precedes the direct fallback so a newly hot-added
// credential cannot use a shared IPv6 before its address/rule is prepared.
// There is no policy table or background writer separate from native reconcile.
type nativeIPv6AutoScope struct {
	inbound string
	guard   map[string]any
	base    map[string]any
}

func nativeIPv6Strings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result = append(result, value)
		}
	}
	return result
}

func nativeIPv6Empty(value any) bool {
	if value == nil || value == "" || value == false || value == json.Number("0") {
		return true
	}
	if array, ok := value.([]any); ok {
		return len(array) == 0
	}
	return false
}

func nativeIPv6Direct(base map[string]any) bool {
	if base == nil || !strings.EqualFold(fmt.Sprint(base["protocol"]), "freedom") ||
		!nativeIPv6Empty(base["sendThrough"]) || !nativeIPv6Empty(base["proxySettings"]) {
		return false
	}
	settings, _ := base["settings"].(map[string]any)
	stream, _ := base["streamSettings"].(map[string]any)
	socket, _ := stream["sockopt"].(map[string]any)
	return nativeIPv6Empty(settings["redirect"]) && nativeIPv6Empty(socket["mark"]) &&
		nativeIPv6Empty(socket["dialerProxy"]) && nativeIPv6Empty(socket["interface"]) &&
		(nativeIPv6Empty(stream["security"]) || stream["security"] == "none")
}

func nativeIPv6AutoScopes(raw string) (map[string]any, []any, []nativeIPv6AutoScope, error) {
	var config map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return nil, nil, nil, err
	}
	routing, _ := config["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	outbounds, _ := config["outbounds"].([]any)
	byTag := map[string]map[string]any{}
	for _, item := range outbounds {
		if outbound, ok := item.(map[string]any); ok {
			byTag[fmt.Sprint(outbound["tag"])] = outbound
		}
	}
	scopes := []nativeIPv6AutoScope{}
	seen := map[string]bool{}
	for at, item := range rules {
		rule, _ := item.(map[string]any)
		tag, _ := rule["ruleTag"].(string)
		if !strings.HasPrefix(tag, "user6-auto-") || rule["enabled"] == false {
			continue
		}
		inbound := strings.TrimPrefix(tag, "user6-auto-")
		invalid := func() (map[string]any, []any, []nativeIPv6AutoScope, error) {
			return nil, nil, nil, fmt.Errorf("native IPv6 auto rule %q requires an unbound direct fallback and an earlier IPv6 pending blackhole for the same inbound", tag)
		}
		if inbound == "" || seen[inbound] || !slices.Equal(nativeIPv6Strings(rule["inboundTag"]), []string{inbound}) ||
			!nativeIPv6Empty(routing["script"]) || routing["domainStrategy"] != "IPOnDemand" {
			return invalid()
		}
		for key, value := range rule {
			if !slices.Contains([]string{"type", "enabled", "comment", "ruleTag", "inboundTag", "outboundTag"}, key) && !nativeIPv6Empty(value) {
				return invalid()
			}
		}
		base := byTag[fmt.Sprint(rule["outboundTag"])]
		if !nativeIPv6Direct(base) {
			return invalid()
		}
		var guard map[string]any
		for _, earlier := range rules[:at] {
			candidate, _ := earlier.(map[string]any)
			if candidate["ruleTag"] == "user6-pending-"+inbound && candidate["enabled"] != false {
				guard = candidate
			}
		}
		if guard == nil || !slices.Equal(nativeIPv6Strings(guard["inboundTag"]), []string{inbound}) ||
			!slices.Equal(nativeIPv6Strings(guard["ip"]), []string{"::/0"}) ||
			byTag[fmt.Sprint(guard["outboundTag"])]["protocol"] != "blackhole" {
			return invalid()
		}
		for key, value := range guard {
			if !slices.Contains([]string{"type", "enabled", "comment", "ruleTag", "inboundTag", "ip", "outboundTag"}, key) && !nativeIPv6Empty(value) {
				return invalid()
			}
		}
		seen[inbound] = true
		scopes = append(scopes, nativeIPv6AutoScope{inbound: inbound, guard: guard, base: base})
	}
	return config, rules, scopes, nil
}

func reconcileNativeAutoIPv6(ctx context.Context, raw string) (string, error) {
	config, rules, scopes, err := nativeIPv6AutoScopes(raw)
	if err != nil || len(scopes) == 0 {
		return raw, err
	}
	clients, err := NativeIPv6Clients()
	if err != nil {
		return "", err
	}
	changed := false
	for _, scope := range scopes {
		for _, client := range clients {
			if !slices.Contains(client.Inbounds, scope.inbound) {
				continue
			}
			tag := fmt.Sprintf("user6-%d-%s", client.ID, scope.inbound)
			var existing map[string]any
			for _, item := range rules {
				if rule, ok := item.(map[string]any); ok && rule["ruleTag"] == tag {
					existing = rule
					break
				}
			}
			if existing != nil {
				// The reserved tag follows canonical identity; all routing fields,
				// disabled states and operator-renamed outbounds remain untouched.
				if users := nativeIPv6Strings(existing["user"]); len(users) == 1 && users[0] != client.Email {
					existing["user"] = []string{client.Email}
					changed = true
				}
				continue
			}
			for _, item := range config["outbounds"].([]any) {
				if outbound, ok := item.(map[string]any); ok && outbound["tag"] == tag {
					return "", fmt.Errorf("native IPv6 outbound %q already exists without its user rule; repair it in Routing", tag)
				}
			}
			leases, err := AllocateNativeIPv6(ctx, []int{client.ID})
			if err != nil {
				return "", err
			}
			base, _ := json.Marshal(scope.base)
			var outbound map[string]any
			_ = json.Unmarshal(base, &outbound)
			outbound["tag"], outbound["sendThrough"] = tag, leases[0].Address
			delete(outbound, "targetStrategy")
			settings, _ := outbound["settings"].(map[string]any)
			if settings == nil {
				settings = map[string]any{}
				outbound["settings"] = settings
			}
			delete(settings, "domainStrategy")
			delete(settings, "targetStrategy")
			stream, _ := outbound["streamSettings"].(map[string]any)
			if stream == nil {
				stream = map[string]any{}
				outbound["streamSettings"] = stream
			}
			socket, _ := stream["sockopt"].(map[string]any)
			if socket == nil {
				socket = map[string]any{}
				stream["sockopt"] = socket
			}
			socket["domainStrategy"] = "ForceIPv6"
			config["outbounds"] = append(config["outbounds"].([]any), outbound)
			rule := map[string]any{
				"type": "field", "ruleTag": tag, "comment": client.Email + " → " + leases[0].Address,
				"inboundTag": []string{scope.inbound}, "user": []string{client.Email}, "ip": []string{"::/0"}, "outboundTag": tag,
			}
			at := slices.IndexFunc(rules, func(item any) bool {
				candidate, _ := item.(map[string]any)
				return candidate["ruleTag"] == scope.guard["ruleTag"]
			})
			rules = slices.Insert(rules, at, any(rule))
			changed = true
		}
	}
	if !changed {
		return raw, nil
	}
	config["routing"].(map[string]any)["rules"] = rules
	updated, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	// Never overwrite a simultaneous native-page save with an older snapshot.
	result := database.GetDB().Model(&model.Setting{}).Where("key = ? AND value = ?", "xrayTemplateConfig", raw).Update("value", string(updated))
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", errors.New("native IPv6 template changed concurrently; retry native reconcile")
	}
	return string(updated), nil
}
