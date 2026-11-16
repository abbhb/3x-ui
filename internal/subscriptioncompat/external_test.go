package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestExternalSnapshotRejectsReferencesAndNeverImportsUpstreamPolicy(t *testing.T) {
	raw := []byte(`dns: {nameserver: [system]}
proxy-groups: [{name: unsafe, type: select, proxies: [DIRECT]}]
rules: [MATCH,DIRECT]
proxies:
- {name: airport, type: vless, server: synthetic.example.test, port: 443, uuid: synthetic, network: xhttp, xhttp-opts: {path: /x/, download-settings: {server: '2001:db8::8', tls: true}}}
- {name: bypass, type: socks5, server: bad.test, port: 80, dialer-proxy: DIRECT}
`)
	snapshot, err := ParseSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Nodes) != 1 || snapshot.Skipped != 1 || snapshot.Nodes[0].Payload["name"] != "airport" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	b, _ := json.Marshal(snapshot.Nodes[0].Payload)
	if strings.Contains(string(b), "nameserver") || !strings.Contains(string(b), "2001:db8::8") {
		t.Fatalf("node projection=%s", b)
	}
	for _, bad := range []string{
		"proxies: [&node {name: a, type: ss, server: a, port: 1}, *node]",
		"proxies: [{name: a, name: b, type: ss, server: a, port: 1}]",
		"proxies: [{name: a, type: ss, server: a, port: 1, plugin-opts: {detour: hidden}}]",
		"proxies: [{name: a, type: socks5, server: a, port: true}]",
	} {
		if _, err := ParseSnapshot([]byte(bad)); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("unsafe snapshot accepted: %v", err)
		}
	}
	newer, err := ParseSnapshot([]byte(`proxies: [{name: airport, type: vless, server: synthetic.example.test, port: 443, uuid: changed}]`))
	if err != nil || newer.Nodes[0].Key != snapshot.Nodes[0].Key {
		t.Fatal("credential refresh changed permanent node identity")
	}
}

func TestExternalCipherIsDomainSeparatedAndRejectsCorruption(t *testing.T) {
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "synthetic-external-key-at-least-thirty-two-bytes")
	cipher, err := Encrypt("url", []byte("https://synthetic.example.test/private?token=synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipher, "synthetic") {
		t.Fatal("stored plaintext URL")
	}
	got, err := Decrypt("url", cipher)
	if err != nil || string(got) != "https://synthetic.example.test/private?token=synthetic" {
		t.Fatal("URL round trip failed")
	}
	if _, err = Decrypt("proxy", cipher); !errors.Is(err, ErrEncryptedUnavailable) {
		t.Fatalf("cross-domain decrypt=%v", err)
	}
	t.Setenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY", "different-synthetic-key-at-least-thirty-two-bytes")
	if _, err = Decrypt("url", cipher); !errors.Is(err, ErrEncryptedUnavailable) {
		t.Fatalf("wrong-key decrypt=%v", err)
	}
}

func TestXHTTPProfileRejectsMistypedPublicConnectionFields(t *testing.T) {
	base := map[string]any{"network": "xhttp", "security": "none", "xhttpSettings": map[string]any{"path": "/x/"}}
	for name, change := range map[string]func(map[string]any){
		"network":  func(v map[string]any) { v["network"] = true },
		"security": func(v map[string]any) { v["security"] = false },
		"port":     func(v map[string]any) { v["port"] = 443.5 },
		"header": func(v map[string]any) {
			v["xhttpSettings"].(map[string]any)["headers"] = map[string]any{"x-test": "bad\r\nInjected: yes"}
		},
		"boolean": func(v map[string]any) { v["xhttpSettings"].(map[string]any)["noGRPCHeader"] = "false" },
		"range": func(v map[string]any) {
			v["xhttpSettings"].(map[string]any)["xmux"] = map[string]any{"maxConcurrency": "8-4"}
		},
		"keepalive": func(v map[string]any) {
			v["xhttpSettings"].(map[string]any)["xmux"] = map[string]any{"hKeepAlivePeriod": "1-2"}
		},
		"alpn": func(v map[string]any) {
			v["security"] = "tls"
			v["tlsSettings"] = map[string]any{"serverName": "cdn.test", "alpn": []any{"invalid"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := cloneMap(base)
			change(v)
			if err := ValidateConnection(v, "/x/", false); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %s: %v", name, err)
			}
		})
	}
}
