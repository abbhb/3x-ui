package subscriptioncompat

import (
	"net/url"
)

func proxyOnly(doc map[string]any, name string, active map[string]bool) bool {
	if name == "REJECT" || name == "REJECT-DROP" {
		return true
	}
	if reserved[name] || active[name] {
		return false
	}
	for _, p := range mapList(doc["proxies"]) {
		if p["name"] == name {
			typ, _ := p["type"].(string)
			return p["server"] != nil && typ != "direct" && typ != "dns" && typ != "reject" && typ != "pass" && typ != "compatible"
		}
	}
	for _, g := range mapList(doc["proxy-groups"]) {
		if g["name"] != name {
			continue
		}
		for _, k := range []string{"use", "include-all", "include-all-proxies", "include-all-providers"} {
			if v, exists := g[k]; exists && v != false && v != nil {
				return false
			}
		}
		members := stringList(g["proxies"])
		if len(members) == 0 {
			return false
		}
		active[name] = true
		defer delete(active, name)
		for _, member := range members {
			if !proxyOnly(doc, member, active) {
				return false
			}
		}
		return true
	}
	return false
}

func SecureDNS(doc map[string]any, primary, subscriptionURL string) {
	outbound := primary
	if !proxyOnly(doc, primary, map[string]bool{}) {
		outbound = "REJECT"
	}
	bootstrap := []string{"https://223.5.5.5/dns-query#DIRECT", "https://223.6.6.6/dns-query#DIRECT"}
	policy := map[string]any{}
	if u, err := url.Parse(subscriptionURL); err == nil && u.Hostname() != "" {
		policy[u.Hostname()] = bootstrap
	}
	exceptions := []string{"+.lan", "+.local", "+.localhost", "+.internal", "+.localdomain", "+.home.arpa", "WORKGROUP", "time.*.com", "time.*.gov", "time.*.edu.cn", "time.*.apple.com", "time-ios.apple.com", "ntp.*.com", "*.time.edu.cn", "*.ntp.org.cn", "+.pool.ntp.org", "+.stun.*.*", "+.stun.*.*.*", "+.stun.*.*.*.*", "+.stun.*.*.*.*.*", "+.stun.playstation.net", "+.msftconnecttest.com", "+.msftncsi.com", "localhost.ptlogin2.qq.com", "localhost.sec.qq.com"}
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		exceptions = append(exceptions, "time"+n+".*.com", "ntp"+n+".*.com")
	}
	doc["dns"] = map[string]any{"enable": true, "listen": "127.0.0.1:1053", "ipv6": false, "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16", "fake-ip-filter-mode": "blacklist", "fake-ip-filter": exceptions, "use-hosts": true, "prefer-h3": false, "respect-rules": true, "default-nameserver": bootstrap, "proxy-server-nameserver": bootstrap, "nameserver": []string{"https://1.1.1.1/dns-query#" + outbound, "https://8.8.8.8/dns-query#" + outbound}, "nameserver-policy": policy, "direct-nameserver": []string{}, "direct-nameserver-follow-policy": true, "fallback": []string{}}
	doc["clash-for-android"] = map[string]any{"append-system-dns": false}
	doc["tun"] = map[string]any{"auto-route": true, "auto-detect-interface": true, "strict-route": true, "dns-hijack": []string{"any:53", "tcp://any:53"}, "inet6-address": []string{"fdfe:dcba:9876::1/126"}}
	doc["ipv6"] = true
}
