package sub

import (
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
)

// The socket peer can differ from the TLS name and HTTP Host. Only an
// independently dialed upload or download endpoint needs a local bypass.
func mihomoEndpointHosts(proxy map[string]any) []string {
	if dialer, _ := proxy["dialer-proxy"].(string); dialer != "" && dialer != "DIRECT" {
		return nil
	}
	hosts := make([]string, 0, 2)
	xhttp, _ := proxy["xhttp-opts"].(map[string]any)
	download, _ := xhttp["download-settings"].(map[string]any)
	for _, endpoint := range []any{proxy["server"], download["server"]} {
		raw, ok := endpoint.(string)
		if !ok {
			continue
		}
		host, err := netsafe.NormalizeHost(strings.TrimSuffix(raw, "."))
		if err != nil {
			continue
		}
		if ip, err := netip.ParseAddr(host); err == nil && !ip.IsGlobalUnicast() {
			continue
		}
		hosts = append(hosts, strings.ToLower(host))
	}
	return hosts
}

func applyNativeEndpointBypass(config map[string]any, native []map[string]any) {
	if len(native) == 0 {
		return
	}
	authorized := make(map[string]bool)
	for _, proxy := range native {
		for _, host := range mihomoEndpointHosts(proxy) {
			authorized[host] = true
		}
	}
	// A custom routing template may have replaced the proxy list. Only emit
	// exclusions for endpoints still present in this rendered subscription.
	hosts := make(map[string]bool)
	proxies, _ := asAnySlice(config["proxies"])
	for _, raw := range proxies {
		proxy, _ := raw.(map[string]any)
		for _, host := range mihomoEndpointHosts(proxy) {
			if authorized[host] {
				hosts[host] = true
			}
		}
	}
	if len(hosts) == 0 {
		return
	}

	tun, _ := config["tun"].(map[string]any)
	exclusions, _ := asAnySlice(tun["route-exclude-address"])
	seen := make(map[string]bool)
	for _, item := range exclusions {
		if cidr, ok := item.(string); ok {
			seen[cidr] = true
		}
	}
	direct := make(map[string]bool)
	rules := make([]any, 0, len(hosts))
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		rule := "DOMAIN," + host + ",DIRECT"
		if ip, err := netip.ParseAddr(host); err == nil {
			prefix := netip.PrefixFrom(ip, ip.BitLen()).String()
			if !seen[prefix] {
				exclusions = append(exclusions, prefix)
				seen[prefix] = true
			}
			rule = "IP-CIDR," + prefix + ",DIRECT,no-resolve"
		}
		direct[rule] = true
		rules = append(rules, rule)
	}
	existing, _ := asAnySlice(config["rules"])
	for _, item := range existing {
		if rule, ok := item.(string); !ok || !direct[rule] {
			rules = append(rules, item)
		}
	}
	config["rules"] = rules
	if len(exclusions) > 0 {
		if tun == nil {
			tun = make(map[string]any)
			config["tun"] = tun
		}
		tun["route-exclude-address"] = exclusions
	}
}
