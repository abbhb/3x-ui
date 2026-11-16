package service

import (
	_ "embed"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The versioned APNIC snapshot is copied unchanged from the source repository.
//
//go:embed portal_cn.cidr
var portalChinaCIDRs string

var (
	portalChinaOnce  sync.Once
	portalChinaIndex map[netip.Prefix]struct{}
)

func portalNetwork(value string) (string, error) {
	value = strings.TrimSpace(value)
	if ip, err := netip.ParseAddr(value); err == nil {
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()).String(), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "", ErrPortalInvalid
	}
	return prefix.Masked().String(), nil
}

// TCP always uses its peer. A real, explicitly configured Unix listener may
// trust the reverse proxy's overwritten X-Real-IP; forwarding chains are ignored.
func PortalRealClientIP(request *http.Request) (string, error) {
	value := request.RemoteAddr
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	ip, err := netip.ParseAddr(value)
	if err != nil && (request.RemoteAddr == "" || request.RemoteAddr == "@") && (os.Getenv("XUI_WEB_UNIX_SOCKET") != "" || os.Getenv("XUI_SUB_UNIX_SOCKET") != "") {
		ip, err = netip.ParseAddr(request.Header.Get("X-Real-IP"))
	}
	if err != nil || ip.Zone() != "" {
		return "", ErrPortalAuth
	}
	return ip.Unmap().String(), nil
}

func portalSourceAllowed(source string, p model.PortalSettings) bool {
	if p.SourceMode == "any" {
		return true
	}
	ip, err := netip.ParseAddr(source)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	custom := false
	if p.SourceMode == "custom" || p.SourceMode == "china_or_custom" {
		for _, value := range p.CustomCIDRs {
			if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Contains(ip) {
				custom = true
				break
			}
		}
	}
	china := false
	if p.SourceMode == "china" || p.SourceMode == "china_or_custom" {
		portalChinaOnce.Do(func() {
			index := map[netip.Prefix]struct{}{}
			for line := range strings.SplitSeq(portalChinaCIDRs, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				prefix, err := netip.ParsePrefix(line)
				if err != nil {
					return
				}
				index[prefix.Masked()] = struct{}{}
			}
			portalChinaIndex = index
		})
		for bits := ip.BitLen(); bits >= 0; bits-- {
			if _, ok := portalChinaIndex[netip.PrefixFrom(ip, bits).Masked()]; ok {
				china = true
				break
			}
		}
	}
	return custom || china
}

func (s *CommerceService) IPBlocked(ip string) (bool, error) {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return true, nil
	}
	var rows []model.PortalIPBlock
	if err := database.GetDB().Where("revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)", s.now()).Find(&rows).Error; err != nil {
		return true, err
	}
	for _, row := range rows {
		if prefix, err := netip.ParsePrefix(row.Network); err == nil && prefix.Contains(address.Unmap()) {
			return true, nil
		}
	}
	return false, nil
}

func (s *CommerceService) AddIPBlock(row model.PortalIPBlock) (*model.PortalIPBlock, error) {
	value, err := portalNetwork(row.Network)
	if err != nil {
		return nil, err
	}
	row.Reason = strings.TrimSpace(row.Reason)
	if row.Reason == "" || len(row.Reason) > 500 || row.ActorAdmin == "" || (row.ExpiresAt != 0 && row.ExpiresAt <= s.now()) {
		return nil, ErrPortalInvalid
	}
	row.ID = 0
	row.Network = value
	row.RevokedAt = 0
	row.Source = "manual"
	err = database.GetDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "network"}}, DoUpdates: clause.AssignmentColumns([]string{"reason", "source", "actor_admin", "expires_at", "revoked_at"})}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	var stored model.PortalIPBlock
	err = database.GetDB().Where("network = ?", value).First(&stored).Error
	return &stored, err
}

func (s *CommerceService) RevokeIPBlock(id int) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		var block model.PortalIPBlock
		if err := tx.First(&block, id).Error; err != nil {
			return ErrPortalUnavailable
		}
		if err := tx.Model(&block).Update("revoked_at", s.now()).Error; err != nil {
			return err
		}
		prefix, err := netip.ParsePrefix(block.Network)
		if err == nil && prefix.Bits() == prefix.Addr().BitLen() {
			return tx.Where("source_ip = ?", prefix.Addr().Unmap().String()).Delete(&model.PortalSecurityAttempt{}).Error
		}
		return nil
	})
}

func (s *CommerceService) RecordAuthAttempt(ip, kind string, success bool) error {
	if kind != "portal_login" && kind != "admin_login" && kind != "portal_registration" {
		return ErrPortalInvalid
	}
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	if !settings.Enabled || !settings.AutoBlockEnabled {
		return nil
	}
	return submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.PortalSettings{}).Where("id = 1").UpdateColumn("auto_block_enabled", gorm.Expr("auto_block_enabled")).Error; err != nil {
				return err
			}
			if success {
				return tx.Where("source_ip = ? AND kind = ?", ip, kind).Delete(&model.PortalSecurityAttempt{}).Error
			}
			now := s.now()
			window := settings.LoginWindowSeconds * 1000
			limit := settings.LoginFailureLimit
			if kind == "portal_registration" {
				window = settings.RegistrationWindowSeconds * 1000
				limit = settings.RegistrationFailureLimit
			}
			var row model.PortalSecurityAttempt
			lookup := tx.Where("source_ip = ? AND kind = ?", ip, kind).First(&row).Error
			if lookup != nil && !errors.Is(lookup, gorm.ErrRecordNotFound) {
				return lookup
			}
			if row.ID == 0 {
				// The attacker-controlled key space is bounded, including fresh keys.
				if err := tx.Where("updated_at < ?", now-86400000).Delete(&model.PortalSecurityAttempt{}).Error; err != nil {
					return err
				}
				var count int64
				if err := tx.Model(&model.PortalSecurityAttempt{}).Count(&count).Error; err != nil {
					return err
				}
				if count >= 10000 {
					var ids []int
					if err := tx.Model(&model.PortalSecurityAttempt{}).Order("updated_at,id").Limit(int(count-9999)).Pluck("id", &ids).Error; err != nil {
						return err
					}
					if len(ids) > 0 {
						if err := tx.Delete(&model.PortalSecurityAttempt{}, ids).Error; err != nil {
							return err
						}
					}
				}
				row = model.PortalSecurityAttempt{SourceIP: ip, Kind: kind, WindowStart: now}
			}
			if row.WindowStart < now-window {
				row.WindowStart = now
				row.FailureCount = 0
			}
			row.FailureCount++
			row.UpdatedAt = now
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			if row.FailureCount == limit {
				network, err := portalNetwork(ip)
				if err != nil {
					return err
				}
				expires := int64(0)
				if settings.AutoBlockSeconds > 0 {
					expires = now + settings.AutoBlockSeconds*1000
				}
				return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "network"}}, DoUpdates: clause.AssignmentColumns([]string{"reason", "source", "expires_at", "revoked_at"}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "portal_ip_blocks.revoked_at > 0 OR (portal_ip_blocks.expires_at > 0 AND portal_ip_blocks.expires_at <= ?)", Vars: []any{now}}}}}).Create(&model.PortalIPBlock{Network: network, Source: kind, Reason: "automatic block after authentication failures", ExpiresAt: expires, CreatedAt: now}).Error
			}
			return nil
		})
	})
}

// A bounded short-window throttle complements the persistent failed-login policy.
// It retains no passwords, bearer tokens or raw request bodies.
type portalRateEntry struct {
	Until int64
	Count int
}

var portalRate = struct {
	sync.Mutex
	Rows map[string]portalRateEntry
}{Rows: map[string]portalRateEntry{}}

func PortalAllowAttempt(key string, limit int, window time.Duration) bool {
	now := time.Now().UnixMilli()
	portalRate.Lock()
	defer portalRate.Unlock()
	row := portalRate.Rows[key]
	if row.Until <= now {
		row = portalRateEntry{Until: now + window.Milliseconds()}
	}
	if _, ok := portalRate.Rows[key]; !ok && len(portalRate.Rows) >= 10000 {
		for value, entry := range portalRate.Rows {
			if entry.Until <= now {
				delete(portalRate.Rows, value)
			}
		}
		if len(portalRate.Rows) >= 10000 {
			return false
		}
	}
	row.Count++
	portalRate.Rows[key] = row
	return row.Count <= limit
}

func PortalClearAttempt(key string) {
	portalRate.Lock()
	delete(portalRate.Rows, key)
	portalRate.Unlock()
}
