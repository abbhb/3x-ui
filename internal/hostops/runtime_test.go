package hostops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadLocalPolicyReadbackExpiresAndCannotChangeOwnership(t *testing.T) {
	root := t.TempDir()
	config := Config{Policy: Policy{Enabled: true, Namespace: "test", Interface: "eth0", Prefix: "2001:db8:1::/64", MarkPrefix: 25903, Inbounds: []string{"entry"}}, State: root, Binary: filepath.Join(root, "xray"), Allocations: filepath.Join(root, "allocations.sqlite3")}
	path := filepath.Join(root, "node.json")
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_EGRESS_CONFIG", path)
	t.Setenv("XUI_XRAY_LAUNCHER", filepath.Join(root, "launch.py"))
	socket, err := net.Listen("unix", filepath.Join(root, "control.sock"))
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	if err := os.WriteFile(filepath.Join(root, "base.json"), []byte(`{"pid":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	status := Status{Enabled: true, Verified: true, PID: 42, UpdatedAt: time.Now().UnixMilli(), PolicyHash: hex.EncodeToString(hash[:]), Users: []Allocation{{Identity: "7", Address: "2001:db8:1::7"}}}
	writeStatus := func() {
		data, _ := json.Marshal(status)
		if err := os.WriteFile(filepath.Join(root, "status.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus()
	report := ReadLocal()
	if !report.Installed || !report.Status.Verified {
		t.Fatalf("fresh report=%+v", report)
	}
	status.UpdatedAt = time.Now().Add(-2 * time.Minute).UnixMilli()
	writeStatus()
	if ReadLocal().Status.Verified {
		t.Fatal("stale report claimed verified")
	}
	changed := config.Policy
	changed.Prefix = "2001:db8:2::/64"
	if err := SavePolicy(changed); err == nil {
		t.Fatal("online prefix migration was accepted")
	}
	changed = config.Policy
	changed.Enabled = false
	if err := SavePolicy(changed); err != nil {
		t.Fatal(err)
	}
	if ReadLocal().Status.Verified {
		t.Fatal("new desired policy reused old readback")
	}
	data, _ := os.ReadFile(path)
	var saved Config
	_ = json.Unmarshal(data, &saved)
	if saved.Binary != config.Binary || saved.Allocations != config.Allocations {
		t.Fatal("API changed local paths")
	}
}

func TestCollectorFourFamiliesBoundedAndNoCredentials(t *testing.T) {
	c := &Collector{}
	if c.Feed("ordinary core log") {
		t.Fatal("ordinary line consumed")
	}
	c.Feed(`MZCONN {"version":1,"event":"ready"}`)
	for i := 0; i < 504; i++ {
		source := "tcp:127.0.0.1:9000"
		remote := "127.0.0.1:443"
		if i%2 == 1 {
			source = "tcp:[::1]:9000"
		}
		if i%4 >= 2 {
			remote = "[2001:db8::10]:443"
		}
		event := map[string]any{"version": 1, "event": "dial", "id": time.Now().Add(time.Duration(i) * time.Nanosecond).Format(time.RFC3339Nano), "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "user": "7.synthetic", "source": source, "target": "example.test:443", "remote": remote, "local": "[2001:db8::a]:60000", "network": "TCP", "password": "must-not-survive"}
		data, _ := json.Marshal(event)
		c.Feed("MZCONN " + string(data))
	}
	snapshot := c.Snapshot()
	if !snapshot.Ready || len(snapshot.Records) != 500 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	families := map[string]bool{}
	for _, row := range snapshot.Records {
		families[row.ClientFamily+"->"+row.EgressFamily] = true
		if row.Network != "tcp" {
			t.Fatal("uppercase network not normalized")
		}
	}
	if len(families) != 4 {
		t.Fatalf("families=%v", families)
	}
	data, _ := json.Marshal(snapshot)
	if strings.Contains(string(data), "must-not-survive") || strings.Contains(string(data), "password") {
		t.Fatal("credentials retained in report")
	}
	if !c.Feed("MZCONN malformed private input") {
		t.Fatal("malformed observer payload reached generic logs")
	}
	c.Reset()
	if c.Snapshot().Ready || len(c.Snapshot().Records) != 0 {
		t.Fatal("old core observations survived restart")
	}
}
