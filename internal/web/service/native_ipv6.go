package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type NativeIPv6Pool struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
}

type NativeIPv6Client struct {
	ID       int      `json:"id"`
	Email    string   `json:"email"`
	Inbounds []string `json:"inbounds"`
}

type NativeIPv6Address struct {
	model.NativeIPv6Lease
	Ready bool `json:"ready"`
}

type NativeIPv6Report struct {
	Pool       *NativeIPv6Pool     `json:"pool"`
	Interfaces []string            `json:"interfaces"`
	Clients    []NativeIPv6Client  `json:"clients"`
	Leases     []NativeIPv6Address `json:"leases"`
	Error      string              `json:"error"`
}

type NativeIPv6AllocationInput struct {
	ClientIDs []int `json:"clientIds"`
}

const nativeIPv6PoolKey = "nativeIPv6Pool"

var (
	nativeIPv6Mu               sync.Mutex
	nativeIPv6InterfacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)
	// Test seam; production executes only iproute2 with validated argv, never a shell.
	nativeIPv6IP = func(ctx context.Context, args ...string) ([]byte, error) {
		if runtime.GOOS != "linux" {
			return nil, errors.New("native IPv6 address preparation requires Linux and iproute2")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ip", args...).Output()
		if err != nil {
			return nil, errors.New("IPv6 address operation failed; check interface, prefix ownership and CAP_NET_ADMIN")
		}
		return out, nil
	}
)

type nativeIPv6Interface struct {
	Name string `json:"ifname"`
	Info []struct {
		Local     string   `json:"local"`
		Flags     []string `json:"flags"`
		Tentative bool     `json:"tentative"`
		DadFailed bool     `json:"dadfailed"`
	} `json:"addr_info"`
}

func nativeIPv6Network(ctx context.Context) ([]nativeIPv6Interface, error) {
	raw, err := nativeIPv6IP(ctx, "-j", "-6", "address", "show")
	if err != nil {
		return nil, err
	}
	var interfaces []nativeIPv6Interface
	if len(raw) > 2<<20 || json.Unmarshal(raw, &interfaces) != nil {
		return nil, errors.New("invalid IPv6 interface report")
	}
	return interfaces, nil
}

func loadNativeIPv6Pool() (*NativeIPv6Pool, error) {
	var row model.Setting
	if err := database.GetDB().Where("key = ?", nativeIPv6PoolKey).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var pool NativeIPv6Pool
	if json.Unmarshal([]byte(row.Value), &pool) != nil || pool.validate() != nil {
		return nil, errors.New("invalid native IPv6 pool; repair the service's local pool setting")
	}
	return &pool, nil
}

func (p NativeIPv6Pool) validate() error {
	prefix, err := netip.ParsePrefix(p.Prefix)
	if !nativeIPv6InterfacePattern.MatchString(p.Interface) || err != nil || !prefix.Addr().Is6() ||
		prefix.Bits() != 64 || prefix != prefix.Masked() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() {
		return errors.New("provide an existing interface and an owned global IPv6 /64")
	}
	return nil
}

func NativeIPv6Clients() ([]NativeIPv6Client, error) {
	var rows []struct {
		ID    int
		Email string
		Tag   string
	}
	err := database.GetDB().Table("clients c").Select("c.id, c.email, i.tag").
		Joins("JOIN client_inbounds ci ON ci.client_id = c.id").
		Joins("JOIN inbounds i ON i.id = ci.inbound_id").
		Where("i.node_id IS NULL AND i.protocol IN ?", []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria"}).
		Order("c.id, i.id").Scan(&rows).Error
	clients := []NativeIPv6Client{}
	for _, row := range rows {
		if len(clients) == 0 || clients[len(clients)-1].ID != row.ID {
			clients = append(clients, NativeIPv6Client{ID: row.ID, Email: row.Email, Inbounds: []string{}})
		}
		clients[len(clients)-1].Inbounds = append(clients[len(clients)-1].Inbounds, row.Tag)
	}
	return clients, err
}

func NativeIPv6PoolReport(ctx context.Context) (*NativeIPv6Report, error) {
	pool, err := loadNativeIPv6Pool()
	if err != nil {
		return nil, err
	}
	clients, err := NativeIPv6Clients()
	if err != nil {
		return nil, err
	}
	report := &NativeIPv6Report{Pool: pool, Clients: clients, Interfaces: []string{}, Leases: []NativeIPv6Address{}}
	interfaces, netErr := nativeIPv6Network(ctx)
	if netErr != nil {
		report.Error = netErr.Error()
	}
	ready := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Name != "lo" && nativeIPv6InterfacePattern.MatchString(iface.Name) {
			report.Interfaces = append(report.Interfaces, iface.Name)
		}
		if pool != nil && iface.Name == pool.Interface {
			for _, addr := range iface.Info {
				if !addr.Tentative && !addr.DadFailed && !slices.Contains(addr.Flags, "tentative") && !slices.Contains(addr.Flags, "dadfailed") {
					ready[addr.Local] = true
				}
			}
		}
	}
	var leases []model.NativeIPv6Lease
	if err := database.GetDB().Order("client_id").Find(&leases).Error; err != nil {
		return nil, err
	}
	for _, lease := range leases {
		report.Leases = append(report.Leases, NativeIPv6Address{NativeIPv6Lease: lease, Ready: ready[lease.Address]})
	}
	return report, nil
}

func ConfigureNativeIPv6Pool(ctx context.Context, pool NativeIPv6Pool) error {
	if err := pool.validate(); err != nil {
		return err
	}
	nativeIPv6Mu.Lock()
	defer nativeIPv6Mu.Unlock()
	previous, err := loadNativeIPv6Pool()
	if err != nil {
		return err
	}
	var count int64
	if err := database.GetDB().Model(&model.NativeIPv6Lease{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 && (previous == nil || *previous != pool) {
		return errors.New("pool ownership is fixed while leases exist; migrate addresses offline")
	}
	interfaces, err := nativeIPv6Network(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(interfaces, func(i nativeIPv6Interface) bool { return i.Name == pool.Interface && i.Name != "lo" }) {
		return errors.New("pool interface does not exist")
	}
	raw, _ := json.Marshal(pool)
	return (&SettingService{}).saveSetting(nativeIPv6PoolKey, string(raw))
}

func AllocateNativeIPv6(ctx context.Context, ids []int) ([]NativeIPv6Address, error) {
	if len(ids) == 0 || len(ids) > 128 {
		return nil, errors.New("select 1-128 local clients")
	}
	nativeIPv6Mu.Lock()
	defer nativeIPv6Mu.Unlock()
	pool, err := loadNativeIPv6Pool()
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, errors.New("configure the selected service's IPv6 pool first")
	}
	clients, err := NativeIPv6Clients()
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] || !slices.ContainsFunc(clients, func(c NativeIPv6Client) bool { return c.ID == id }) {
			return nil, errors.New("client must belong to a local identifiable inbound")
		}
		seen[id] = true
	}
	report, err := NativeIPv6PoolReport(ctx)
	if err != nil {
		return nil, err
	}
	if report.Error != "" {
		return nil, errors.New(report.Error)
	}
	occupied := map[string]bool{}
	interfaces, err := nativeIPv6Network(ctx)
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		for _, a := range iface.Info {
			occupied[a.Local] = true
		}
	}
	for _, lease := range report.Leases {
		occupied[lease.Address] = true
	}
	prefix := netip.MustParsePrefix(pool.Prefix)
	result := []NativeIPv6Address{}
	for _, id := range ids {
		var lease model.NativeIPv6Lease
		var matches []model.NativeIPv6Lease
		if err := database.GetDB().Where("client_id = ?", id).Find(&matches).Error; err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			lease = matches[0]
		} else {
			for {
				bytes := prefix.Addr().As16()
				if _, err := rand.Read(bytes[8:]); err != nil {
					return nil, err
				}
				address := netip.AddrFrom16(bytes).String()
				if occupied[address] || address == prefix.Addr().String() {
					continue
				}
				lease = model.NativeIPv6Lease{ClientID: id, Address: address}
				if err := database.GetDB().Create(&lease).Error; err != nil {
					return nil, err
				}
				occupied[address] = true
				break
			}
		}
		// Reserve first: a failed host operation stays pending with the same address.
		if err := prepareNativeIPv6Address(ctx, *pool, lease.Address); err != nil {
			return nil, err
		}
		result = append(result, NativeIPv6Address{NativeIPv6Lease: lease, Ready: true})
	}
	return result, nil
}

func prepareNativeIPv6Address(ctx context.Context, pool NativeIPv6Pool, address string) error {
	ip, err := netip.ParseAddr(address)
	if err != nil || !netip.MustParsePrefix(pool.Prefix).Contains(ip) {
		return errors.New("lease is outside the configured pool")
	}
	// Linux DAD includes a randomized initial delay plus retransmission. Two
	// seconds can expire just before a valid restored address leaves tentative.
	for attempt := range 50 {
		interfaces, err := nativeIPv6Network(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, iface := range interfaces {
			if iface.Name != pool.Interface {
				continue
			}
			for _, addr := range iface.Info {
				if addr.Local != address {
					continue
				}
				found = true
				if addr.DadFailed || slices.Contains(addr.Flags, "dadfailed") {
					return errors.New("IPv6 duplicate address detection failed")
				}
				if !addr.Tentative && !slices.Contains(addr.Flags, "tentative") {
					return nil
				}
			}
		}
		if !found && attempt == 0 {
			// Explicit sendThrough bindings can use a valid deprecated address;
			// unrelated unbound connections must keep their normal source selection.
			if _, err := nativeIPv6IP(ctx, "-6", "address", "add", address+"/128", "dev", pool.Interface, "preferred_lft", "0", "noprefixroute"); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("IPv6 address %s is not ready on %s", address, pool.Interface)
}

// Prepare only leases actually referenced by the native template. No routes,
// marks, NAT, listeners, legacy controllers or other services are changed.
func prepareNativeIPv6Bindings(ctx context.Context, template string) error {
	nativeIPv6Mu.Lock()
	defer nativeIPv6Mu.Unlock()
	pool, err := loadNativeIPv6Pool()
	if err != nil {
		return err
	}
	if pool == nil {
		return nil
	}
	var config struct {
		Outbounds []struct {
			SendThrough string `json:"sendThrough"`
		} `json:"outbounds"`
	}
	if json.Unmarshal([]byte(template), &config) != nil {
		return nil
	}
	var leases []model.NativeIPv6Lease
	if err := database.GetDB().Find(&leases).Error; err != nil {
		return err
	}
	if len(leases) == 0 {
		return nil
	}
	for _, outbound := range config.Outbounds {
		if slices.ContainsFunc(leases, func(l model.NativeIPv6Lease) bool { return l.Address == outbound.SendThrough }) {
			if err := prepareNativeIPv6Address(ctx, *pool, outbound.SendThrough); err != nil {
				return err
			}
		}
	}
	return nil
}
