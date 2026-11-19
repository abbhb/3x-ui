package sub

import (
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

// Only native entries carry this Go type. Imported YAML cannot claim an inbound's hop.
type nativeInboundTag string

const inboundDialerMarker = "_3xui-mihomo-inbound"

// Add hops after plan, group and name projection. A hop is delivered only if the
// final profile still contains its native landing, and is never added to a selector.
func applyMihomoInboundDialers(config map[string]any, dialers map[string]string) ([]map[string]any, error) {
	proxies, _ := config["proxies"].([]map[string]any)
	groups, _ := config["proxy-groups"].([]map[string]any)
	used := map[string]bool{}
	for _, entries := range [][]map[string]any{proxies, groups} {
		for _, entry := range entries {
			name, _ := entry["name"].(string)
			used[name] = true
		}
	}
	names := map[nativeInboundTag]string{}
	var hops []map[string]any
	for _, proxy := range proxies {
		tag, native := proxy[inboundDialerMarker].(nativeInboundTag)
		delete(proxy, inboundDialerMarker)
		if !native {
			continue
		}
		raw, configured := dialers[string(tag)]
		if !configured {
			continue
		}
		name, exists := names[tag]
		if !exists {
			hop, err := subscriptioncompat.OpenInboundDialer(raw)
			if err != nil {
				return nil, err
			}
			name = "MGMA-HOP · " + string(tag)
			for used[name] {
				name += "_"
			}
			used[name] = true
			names[tag] = name
			hop["name"] = name
			hops = append(hops, hop)
		}
		proxy["dialer-proxy"] = name
	}
	config["proxies"] = append(proxies, hops...)
	return hops, nil
}
