package hostops

import (
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Observation struct {
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	User         string `json:"user"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	Network      string `json:"network"`
	Inbound      string `json:"inbound"`
	Outbound     string `json:"outbound"`
	Local        string `json:"local"`
	Remote       string `json:"remote"`
	ClientFamily string `json:"clientFamily"`
	EgressFamily string `json:"egressFamily"`
}

type ObservationSnapshot struct {
	Ready   bool          `json:"ready"`
	Dropped int           `json:"dropped"`
	Records []Observation `json:"records"`
}

type Collector struct {
	mu      sync.Mutex
	ready   bool
	dropped int
	records []Observation
}

var Observations = &Collector{}

func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready, c.dropped, c.records = false, 0, nil
}

func endpointFamily(address string) string {
	address = strings.TrimPrefix(strings.TrimPrefix(address, "tcp:"), "udp:")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "unknown"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	if ip.Unmap().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// Feed consumes MZCONN v1 from an explicitly instrumented core. It returns true
// even for malformed observer lines so they never spill into general panel logs.
func (c *Collector) Feed(line string) bool {
	if !strings.HasPrefix(line, "MZCONN ") {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var event struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
		Observation
	}
	if len(line) > 8192 || json.Unmarshal([]byte(strings.TrimPrefix(line, "MZCONN ")), &event) != nil || event.Version != 1 {
		c.dropped++
		return true
	}
	if event.Event == "ready" {
		c.ready = true
		return true
	}
	if event.Event != "dial" || event.User == "" || len(event.User) > 256 || event.ID == "" {
		c.dropped++
		return true
	}
	if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
		c.dropped++
		return true
	}
	event.Network = strings.ToLower(event.Network)
	if event.Network != "tcp" && event.Network != "udp" {
		c.dropped++
		return true
	}
	event.ClientFamily = endpointFamily(event.Source)
	event.EgressFamily = endpointFamily(event.Remote)
	if event.EgressFamily == "unknown" {
		c.dropped++
		return true
	}
	c.ready = true
	c.records = append(c.records, event.Observation)
	if len(c.records) > 500 {
		c.records = c.records[len(c.records)-500:]
	}
	return true
}

func (c *Collector) Snapshot() ObservationSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := ObservationSnapshot{Ready: c.ready, Dropped: c.dropped, Records: make([]Observation, len(c.records))}
	for i := range c.records {
		result.Records[len(c.records)-1-i] = c.records[i]
	}
	return result
}
