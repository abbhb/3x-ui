package sub

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var mihomoDecimalInteger = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE]([+-]?[0-9]{1,3}))?$`)

// mihomoIntegerScalar preserves integer precision and range strings. Mihomo
// decodes these XHTTP options as strings and then calls strconv.Atoi, so a
// YAML float such as 1000000.0 becomes the invalid string "1E+06" there.
// Invalid or fractional values stay invalid rather than silently being rounded.
func mihomoIntegerScalar(value any) any {
	var raw string
	switch v := value.(type) {
	case string:
		if !strings.ContainsAny(v, ".eE") {
			return value
		}
		raw = v
	case json.Number:
		raw = string(v)
	case float64:
		raw = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return value
	}
	if len(raw) > 128 {
		return value
	}
	match := mihomoDecimalInteger.FindStringSubmatch(raw)
	if match == nil {
		return value
	}
	if match[1] != "" {
		exponent, _ := strconv.Atoi(match[1])
		if exponent < -128 || exponent > 128 {
			return value
		}
	}
	number, ok := new(big.Rat).SetString(raw)
	if !ok || !number.IsInt() || !number.Num().IsInt64() {
		return value
	}
	return number.Num().String()
}

// Normalize after all native, external and custom-routing content is merged.
// Persisted snapshots remain untouched; subsequent refreshes use this same path.
func normalizeMihomoConfig(config map[string]any) {
	fingerprint, _ := config["global-client-fingerprint"].(string)
	delete(config, "global-client-fingerprint")

	normalizeProxy := func(proxy map[string]any) {
		if existing, _ := proxy["client-fingerprint"].(string); existing == "" && fingerprint != "" {
			tls, _ := proxy["tls"].(bool)
			switch proxy["type"] {
			case "trojan", "anytls":
				proxy["client-fingerprint"] = fingerprint
			case "vmess", "vless":
				if tls {
					proxy["client-fingerprint"] = fingerprint
				}
			}
		}
		if options, ok := proxy["xhttp-opts"].(map[string]any); ok {
			for _, key := range []string{"sc-max-each-post-bytes", "sc-min-posts-interval-ms"} {
				if value, exists := options[key]; exists {
					options[key] = mihomoIntegerScalar(value)
				}
			}
		}
	}
	proxies, _ := asAnySlice(config["proxies"])
	for _, raw := range proxies {
		if proxy, ok := raw.(map[string]any); ok {
			normalizeProxy(proxy)
		}
	}
	providers, _ := config["proxy-providers"].(map[string]any)
	for _, raw := range providers {
		provider, _ := raw.(map[string]any)
		payload, _ := asAnySlice(provider["payload"])
		for _, rawProxy := range payload {
			if proxy, ok := rawProxy.(map[string]any); ok {
				normalizeProxy(proxy)
			}
		}
	}
}
