package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

var (
	portalBearerPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	ErrMGMARejected     = errors.New("subscription access unavailable")
)

func portalMGMADigest(token string) (string, error) {
	pepper := os.Getenv("XUI_MGMA_TOKEN_PEPPER")
	if len(pepper) < 32 {
		return "", ErrMGMARejected
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *CommerceService) mgmaClientEligible(tx *gorm.DB, id int) (model.ClientRecord, error) {
	var client model.ClientRecord
	if tx.First(&client, id).Error != nil || !client.Enable || (client.ExpiryTime > 0 && client.ExpiryTime <= s.now()) {
		return client, ErrMGMARejected
	}
	if client.SubID == "" {
		return client, ErrMGMARejected
	}
	var identities int64
	if tx.Model(&model.ClientRecord{}).Where("sub_id = ?", client.SubID).Count(&identities).Error != nil || identities != 1 {
		return client, ErrMGMARejected
	}
	var account model.PortalAccount
	if err := tx.Where("client_id = ?", id).First(&account).Error; err == nil && !account.IsActive {
		return client, ErrMGMARejected
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return client, err
	}
	var traffic xray.ClientTraffic
	if err := tx.Where("email = ?", client.Email).First(&traffic).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return client, err
	}
	rows := []xray.ClientTraffic{traffic}
	overlayGlobalTrafficValues(tx, rows)
	if client.TotalGB > 0 && clampedTrafficAdd(rows[0].Up, rows[0].Down) >= client.TotalGB {
		return client, ErrMGMARejected
	}
	return client, nil
}

type MGMAIssued struct {
	URL        string     `json:"url"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	TTLSeconds int64      `json:"ttl_seconds"`
}

func (s *CommerceService) IssueMGMA(clientID int) (MGMAIssued, error) {
	var result MGMAIssued
	settings, err := s.Settings()
	if err != nil {
		return result, err
	}
	if settings.PublicURL == "" {
		return result, ErrPortalUnavailable
	}
	plain, err := portalRandom()
	if err != nil {
		return result, err
	}
	digest, err := portalMGMADigest(plain)
	if err != nil {
		return result, err
	}
	var client model.ClientRecord
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		lock := tx.Model(&model.ClientRecord{}).Where("id = ?", clientID).UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected != 1 {
			return ErrMGMARejected
		}
		var err error
		client, err = s.mgmaClientEligible(tx, clientID)
		if err != nil {
			return err
		}
		if !client.RequireMGMA {
			if err := tx.Model(&client).Update("require_mgma", true).Error; err != nil {
				return err
			}
			var nodes []int
			if err := tx.Table("inbounds i").Select("DISTINCT i.node_id").Joins("JOIN client_inbounds l ON l.inbound_id = i.id").Where("l.client_id = ? AND i.node_id IS NOT NULL", clientID).Pluck("i.node_id", &nodes).Error; err != nil {
				return err
			}
			for _, node := range nodes {
				if err := (&NodeService{}).MarkNodeDirtyTx(tx, node); err != nil {
					return err
				}
			}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PortalAccess{ClientID: clientID}).Error; err != nil {
			return err
		}
		now := s.now()
		expires := int64(0)
		if settings.TTLSeconds > 0 {
			expires = now + settings.TTLSeconds*1000
			expiresTime := time.UnixMilli(expires).UTC()
			result.ExpiresAt = &expiresTime
		}
		result.IssuedAt = time.UnixMilli(now).UTC()
		result.TTLSeconds = settings.TTLSeconds
		return tx.Model(&model.PortalAccess{}).Where("client_id = ?", clientID).Updates(map[string]any{"digest": digest, "issued_at": now, "expires_at": expires, "consumed_at": 0, "generation": gorm.Expr("generation + 1")}).Error
	})
	if err != nil {
		return MGMAIssued{}, err
	}
	path, err := (&SettingService{}).GetSubPath()
	if err != nil {
		return MGMAIssued{}, err
	}
	result.URL = strings.TrimRight(settings.PublicURL, "/") + "/" + strings.Trim(path, "/") + "/" + url.PathEscape(client.SubID) + "/?token=" + plain
	return result, nil
}

func (s *CommerceService) RevokeMGMA(clientID int) error {
	return database.GetDB().Model(&model.PortalAccess{}).Where("client_id = ?", clientID).Updates(map[string]any{"digest": "", "issued_at": 0, "expires_at": 0, "consumed_at": 0, "generation": gorm.Expr("generation + 1")}).Error
}

// The guard is an immutable generation snapshot used after rendering, too.
type MGMAAuthorization struct {
	ClientID   int
	SubID      string
	Digest     string
	Generation int64
	LegacyPath string
	SourceIP   string
}

func (s *CommerceService) AuthorizeSubscription(path, token, source string, hasToken, consume bool) (*MGMAAuthorization, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, ErrMGMARejected
	}
	var client model.ClientRecord
	err = database.GetDB().Where("sub_id = ?", path).First(&client).Error
	legacy := false
	if errors.Is(err, gorm.ErrRecordNotFound) {
		client, err = s.legacySubscriptionClient(path, settings)
		if err == nil {
			legacy = true
		}
	}
	if err != nil {
		if hasToken {
			return nil, ErrMGMARejected
		}
		return nil, nil
	}
	var protected int64
	if database.GetDB().Model(&model.ClientRecord{}).Where("sub_id = ? AND (require_mgma = ? OR id IN (SELECT client_id FROM portal_accesses))", client.SubID, true).Count(&protected).Error != nil {
		return nil, ErrMGMARejected
	}
	if protected > 0 {
		var matches int64
		if database.GetDB().Model(&model.ClientRecord{}).Where("sub_id = ?", client.SubID).Count(&matches).Error != nil || matches != 1 {
			return nil, ErrMGMARejected
		}
	}
	var access model.PortalAccess
	err = database.GetDB().First(&access, "client_id = ?", client.Id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && !hasToken && !legacy && !client.RequireMGMA {
		return nil, nil
	}
	if err != nil {
		return nil, ErrMGMARejected
	}
	if _, err := s.mgmaClientEligible(database.GetDB(), client.Id); err != nil {
		return nil, ErrMGMARejected
	}
	if !portalSourceAllowed(source, settings) {
		return nil, ErrMGMARejected
	}
	if blocked, err := s.IPBlocked(source); err != nil || blocked {
		return nil, ErrMGMARejected
	}
	guard := &MGMAAuthorization{ClientID: client.Id, SubID: client.SubID, Generation: access.Generation, SourceIP: source}
	if legacy && !hasToken {
		guard.LegacyPath = path
		return guard, nil
	}
	if !hasToken || !portalBearerPattern.MatchString(token) {
		return nil, ErrMGMARejected
	}
	digest, err := portalMGMADigest(token)
	if err != nil || !hmac.Equal([]byte(access.Digest), []byte(digest)) || access.IssuedAt <= 0 || (access.ExpiresAt > 0 && access.ExpiresAt <= s.now()) || access.ConsumedAt != 0 {
		return nil, ErrMGMARejected
	}
	guard.Digest = digest
	if settings.SingleUse && consume {
		result := database.GetDB().Model(&model.PortalAccess{}).Where("client_id = ? AND digest = ? AND consumed_at = 0 AND generation = ? AND (expires_at = 0 OR expires_at > ?)", client.Id, digest, access.Generation, s.now()).Update("consumed_at", s.now())
		if result.Error != nil || result.RowsAffected != 1 {
			return nil, ErrMGMARejected
		}
	}
	return guard, nil
}

func (s *CommerceService) CheckSubscriptionGeneration(g *MGMAAuthorization) bool {
	settings, err := s.Settings()
	if err != nil || !portalSourceAllowed(g.SourceIP, settings) {
		return false
	}
	if blocked, err := s.IPBlocked(g.SourceIP); err != nil || blocked {
		return false
	}
	client, err := s.mgmaClientEligible(database.GetDB(), g.ClientID)
	if err != nil || client.SubID != g.SubID {
		return false
	}
	if g.LegacyPath != "" {
		old, err := s.legacySubscriptionClient(g.LegacyPath, settings)
		return err == nil && old.Id == g.ClientID
	}
	var access model.PortalAccess
	return database.GetDB().First(&access, "client_id = ?", g.ClientID).Error == nil && access.Generation == g.Generation && hmac.Equal([]byte(access.Digest), []byte(g.Digest)) && (access.ExpiresAt == 0 || access.ExpiresAt > s.now())
}

func (s *CommerceService) legacySubscriptionClient(token string, settings model.PortalSettings) (model.ClientRecord, error) {
	var client model.ClientRecord
	key := os.Getenv("XUI_MARZBAN_SUBSCRIPTION_KEY")
	if settings.Mode == "ephemeral" || len(key) < 32 || len(token) < 15 || len(token) > 2048 {
		return client, ErrMGMARejected
	}
	username := ""
	issued := int64(0)
	if strings.HasPrefix(token, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.") {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			return client, ErrMGMARejected
		}
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
			return client, ErrMGMARejected
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return client, ErrMGMARejected
		}
		var claim struct {
			Username  string `json:"sub"`
			Access    string `json:"access"`
			IssuedAt  int64  `json:"iat"`
			ExpiresAt int64  `json:"exp"`
		}
		if json.Unmarshal(payload, &claim) != nil || claim.Access != "subscription" || (claim.ExpiresAt > 0 && claim.ExpiresAt <= s.now()/1000) {
			return client, ErrMGMARejected
		}
		username, issued = claim.Username, claim.IssuedAt
	} else {
		body, signature := token[:len(token)-10], token[len(token)-10:]
		hash := sha256.Sum256([]byte(body + key))
		want := base64.URLEncoding.EncodeToString(hash[:])[:10]
		if !hmac.Equal([]byte(signature), []byte(want)) {
			return client, ErrMGMARejected
		}
		decoded, err := base64.RawURLEncoding.DecodeString(body)
		if err != nil {
			return client, ErrMGMARejected
		}
		fields := strings.Split(string(decoded), ",")
		if len(fields) != 2 {
			return client, ErrMGMARejected
		}
		username = fields[0]
		issued, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return client, ErrMGMARejected
		}
	}
	if issued <= 0 || issued > s.now()/1000+30 {
		return client, ErrMGMARejected
	}
	var legacy model.PortalLegacyUser
	if database.GetDB().Where("username = ?", username).First(&legacy).Error != nil || issued < legacy.CreatedAt/1000 || issued*1000 < legacy.RevokedAt {
		return client, ErrMGMARejected
	}
	if database.GetDB().First(&client, legacy.ClientID).Error != nil {
		return client, ErrMGMARejected
	}
	return client, nil
}

func (s *CommerceService) RegenerateSubscription(clientID, accountID int) error {
	return submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var rec model.ClientRecord
			if err := tx.First(&rec, clientID).Error; err != nil {
				return ErrPortalUnavailable
			}
			stable, err := portalRandom()
			if err != nil {
				return err
			}
			auth, err := portalRandom()
			if err != nil {
				return err
			}
			old := rec.SubID
			if err := tx.Model(&rec).Updates(map[string]any{"require_mgma": true, "sub_id": stable, "uuid": uuid.NewString(), "auth": auth}).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PortalAccess{ClientID: clientID}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PortalAccess{}).Where("client_id = ?", clientID).Updates(map[string]any{"digest": "", "issued_at": 0, "expires_at": 0, "consumed_at": 0, "generation": gorm.Expr("generation + 1")}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PortalLegacyUser{}).Where("client_id = ?", clientID).Update("revoked_at", s.now()).Error; err != nil {
				return err
			}
			if err := tx.Where("sub_id = ?", old).Delete(&model.ClientHwid{}).Error; err != nil {
				return err
			}
			_, err = s.queuePortalRuntime(tx, accountID, rec, false)
			return err
		})
	})
}

func (s *CommerceService) MGMAUsage(clientID int) (PortalUsage, error) {
	rec, err := s.mgmaClientEligible(database.GetDB(), clientID)
	if err != nil {
		return PortalUsage{}, err
	}
	var traffic xray.ClientTraffic
	if err := database.GetDB().Where("email = ?", rec.Email).First(&traffic).Error; err != nil {
		return PortalUsage{}, err
	}
	return PortalUsage{Status: "active", UsedTraffic: clampedTrafficAdd(traffic.Up, traffic.Down), DataLimit: rec.TotalGB, Expire: rec.ExpiryTime}, nil
}
