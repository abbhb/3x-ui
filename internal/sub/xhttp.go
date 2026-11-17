package sub

import (
	"encoding/json"
	"strings"
)

// These conversions render native inbound XHTTP settings in Mihomo format.
// Per-host commerce route overrides are no longer supported.
func effectiveXHTTP(v map[string]any) map[string]any {
	if extra, ok := v["extra"].(map[string]any); ok {
		raw, _ := json.Marshal(extra)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		for _, key := range []string{"host", "path", "mode"} {
			out[key] = v[key]
		}
		if out["mode"] == nil || out["mode"] == "" {
			out["mode"] = "auto"
		}
		return out
	}
	return v
}

func mihomoConnection(v map[string]any) map[string]any {
	sec, _ := v["security"].(string)
	tls, _ := v[sec+"Settings"].(map[string]any)
	address, _ := v["address"].(string)
	out := map[string]any{"tls": sec == "tls" || sec == "reality", "reality-opts": map[string]any{"public-key": ""}, "servername": address, "skip-cert-verify": false, "client-fingerprint": "chrome", "alpn": []string{"http/1.1"}}
	if address != "" {
		out["server"] = strings.Trim(address, "[]")
	}
	if port, exists := v["port"]; exists {
		out["port"] = port
	}
	if sec == "tls" || sec == "reality" {
		out["alpn"] = []string{"h2"}
	}
	for _, pair := range [][2]string{{"serverName", "servername"}, {"allowInsecure", "skip-cert-verify"}, {"fingerprint", "client-fingerprint"}, {"alpn", "alpn"}} {
		if value, ok := tls[pair[0]]; ok {
			out[pair[1]] = value
		}
	}
	if sec == "reality" {
		pk := tls["password"]
		if pk == nil {
			pk = tls["publicKey"]
		}
		sid := tls["shortId"]
		if sid == nil {
			sid = ""
		}
		out["reality-opts"] = map[string]any{"public-key": pk, "short-id": sid, "support-x25519mlkem768": true}
	}
	return out
}
