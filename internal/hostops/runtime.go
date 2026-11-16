// Package hostops contains the credential-free host runtime report contract.
package hostops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

type Policy struct {
	Enabled    bool     `json:"enabled"`
	Namespace  string   `json:"namespace"`
	Interface  string   `json:"interface"`
	Prefix     string   `json:"prefix"`
	MarkPrefix int      `json:"mark_prefix"`
	Inbounds   []string `json:"inbounds"`
}

// Paths are provisioned locally; API callers cannot choose files or executables.
type Config struct {
	Policy
	State       string `json:"state"`
	Binary      string `json:"binary"`
	Allocations string `json:"allocations"`
}

type Allocation struct {
	Identity string `json:"identity"`
	Address  string `json:"address"`
}

type Status struct {
	Enabled      bool         `json:"enabled"`
	Verified     bool         `json:"verified"`
	PID          int          `json:"pid"`
	UpdatedAt    int64        `json:"updatedAt"`
	Reason       string       `json:"reason"`
	PolicyHash   string       `json:"policyHash"`
	Users        []Allocation `json:"users"`
	BlockedUsers []string     `json:"blockedUsers"`
}

type Report struct {
	Installed bool                `json:"installed"`
	Policy    *Policy             `json:"policy,omitempty"`
	Status    Status              `json:"status"`
	Observer  ObservationSnapshot `json:"observer"`
	Error     string              `json:"error,omitempty"`
	Inbounds  []InboundRef        `json:"inbounds"`
}

type InboundRef struct {
	ID       int    `json:"id"`
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

var (
	namespacePattern = regexp.MustCompile(`^[a-z0-9_]{1,20}$`)
	interfacePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)
	ErrNotInstalled  = errors.New("host egress controller is not installed")
)

func (p Policy) Validate() error {
	if !namespacePattern.MatchString(p.Namespace) || !interfacePattern.MatchString(p.Interface) || p.MarkPrefix < 1 || p.MarkPrefix > 32767 {
		return errors.New("invalid egress namespace, interface or mark prefix")
	}
	prefix, err := netip.ParsePrefix(p.Prefix)
	if err != nil || !prefix.Addr().Is6() || prefix.Bits() != 64 || prefix != prefix.Masked() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() {
		return errors.New("egress requires a public IPv6 /64")
	}
	if len(p.Inbounds) == 0 || len(p.Inbounds) > 128 {
		return errors.New("select at least one managed inbound (maximum 128)")
	}
	seen := map[string]bool{}
	for _, tag := range p.Inbounds {
		if tag == "" || len(tag) > 256 || seen[tag] || tag == "mz-egress-control" {
			return errors.New("invalid or repeated managed inbound")
		}
		seen[tag] = true
	}
	return nil
}

func Load() (*Config, []byte, error) {
	path := os.Getenv("XUI_EGRESS_CONFIG")
	if path == "" || os.Getenv("XUI_XRAY_LAUNCHER") == "" {
		return nil, nil, ErrNotInstalled
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, errors.New("egress bootstrap configuration is unreadable")
	}
	var config Config
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	flag := string(fields["enabled"])
	if len(data) > 64<<10 || (flag != "true" && flag != "false") || json.Unmarshal(data, &config) != nil || config.Policy.Validate() != nil ||
		!filepath.IsAbs(config.State) || !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Allocations) {
		return nil, nil, errors.New("invalid egress bootstrap configuration")
	}
	return &config, data, nil
}

func ReadLocal() Report {
	report := Report{Status: Status{Users: []Allocation{}, BlockedUsers: []string{}}, Observer: Observations.Snapshot()}
	config, raw, err := Load()
	if err != nil {
		if !errors.Is(err, ErrNotInstalled) {
			report.Error = err.Error()
		}
		return report
	}
	report.Installed = true
	report.Policy = &config.Policy
	data, err := os.ReadFile(filepath.Join(config.State, "status.json"))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &report.Status) != nil {
		report.Status = Status{Reason: "awaiting_reconcile", Users: []Allocation{}, BlockedUsers: []string{}}
		return report
	}
	hash := sha256.Sum256(raw)
	var base struct {
		PID int `json:"pid"`
	}
	data, err = os.ReadFile(filepath.Join(config.State, "base.json"))
	baseValid := err == nil && len(data) <= 2<<20 && json.Unmarshal(data, &base) == nil && base.PID > 0 && base.PID == report.Status.PID
	socket, sockErr := os.Stat(filepath.Join(config.State, "control.sock"))
	fresh := report.Status.UpdatedAt > 0 && time.Since(time.UnixMilli(report.Status.UpdatedAt)) < time.Minute && report.Status.UpdatedAt <= time.Now().Add(5*time.Second).UnixMilli()
	if !baseValid || sockErr != nil || socket.Mode()&os.ModeSocket == 0 || !fresh || report.Status.Enabled != config.Enabled || report.Status.PolicyHash != hex.EncodeToString(hash[:]) {
		report.Status.Verified = false
		report.Status.Reason = "stale_or_pending"
	}
	return report
}

func SavePolicy(policy Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	config, _, err := Load()
	if err != nil {
		return err
	}
	// Prefix and ownership changes require a deliberate offline allocation migration.
	if policy.Namespace != config.Namespace || policy.Prefix != config.Prefix || policy.MarkPrefix != config.MarkPrefix || policy.Interface != config.Interface {
		return errors.New("prefix, interface and mark ownership are fixed by the installed instance")
	}
	policy.Inbounds = slices.Clone(policy.Inbounds)
	config.Policy = policy
	path := os.Getenv("XUI_EGRESS_CONFIG")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("egress configuration must be a regular local file")
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".egress-*.json")
	if err != nil {
		return errors.New("cannot stage egress policy")
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist egress policy")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("cannot replace egress policy")
	}
	return nil
}
