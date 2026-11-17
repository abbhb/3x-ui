package subscriptioncompat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type ImportedSource struct {
	model.SubscriptionSource
	Ciphertext string `json:"url_ciphertext"`
	Scope      int    `json:"scope_key"`
	Revision   int64  `json:"url_revision"`
}
type ImportedNode struct {
	model.SubscriptionNode
	Key        string `json:"external_key"`
	Ciphertext string `json:"payload_ciphertext"`
}
type ImportedHost struct {
	model.SubscriptionHost
	Ciphertext string `json:"entry_ciphertext"`
}
type ImportBundle struct {
	Schema    string                   `json:"schema"`
	KeySHA256 string                   `json:"external_key_sha256"`
	Sources   []ImportedSource         `json:"sources"`
	Nodes     []ImportedNode           `json:"nodes"`
	Hosts     []model.Host             `json:"hosts"`
	Routes    []ImportedHost           `json:"routes"`
	Policy    model.SubscriptionPolicy `json:"policy"`
}

func ImportPrepareHosts(tx *gorm.DB, b *ImportBundle, insert func(any, ...any) error) error {
	if b == nil {
		return nil
	}
	if b.Schema != "marzban-subscriptions-to-3x-ui/v1" || len(b.Hosts) > 32768 || len(b.Routes) != len(b.Hosts) {
		return ErrInvalid
	}
	allowed := map[int]bool{}
	inbounds := []int{}
	for _, h := range b.Hosts {
		if allowed[h.Id] {
			return ErrInvalid
		}
		allowed[h.Id] = true
		inbounds = append(inbounds, h.InboundId)
	}
	var existing []model.Host
	if err := tx.Where("inbound_id IN ?", inbounds).Find(&existing).Error; err != nil {
		return err
	}
	for _, h := range existing {
		if !allowed[h.Id] {
			return ErrConflict
		}
	}
	for _, h := range b.Hosts {
		if h.Id < 1 || h.InboundId < 1 || h.Address == "" || h.Port < 0 || h.Port > 65535 || !cleanText(h.Remark, 256) {
			return ErrInvalid
		}
		var inbound model.Inbound
		if tx.First(&inbound, h.InboundId).Error != nil {
			return ErrInvalid
		}
		var old model.Host
		err := tx.First(&old, h.Id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := insert(&h, h.Id); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			old.CreatedAt, old.UpdatedAt = 0, 0
			h.CreatedAt, h.UpdatedAt = 0, 0
			raw1, _ := json.Marshal(old)
			raw2, _ := json.Marshal(h)
			var a, c map[string]any
			_ = json.Unmarshal(raw1, &a)
			_ = json.Unmarshal(raw2, &c)
			if !reflect.DeepEqual(a, c) {
				return ErrConflict
			}
		}
	}
	return nil
}

func ImportRows(tx *gorm.DB, b *ImportBundle, insert func(any, ...any) error) error {
	if b == nil {
		return nil
	}
	hasCipher := len(b.Nodes) > 0
	for _, s := range b.Sources {
		hasCipher = hasCipher || s.Ciphertext != ""
	}
	if hasCipher {
		_, err := externalKey()
		if err != nil {
			return err
		}
		if b.KeySHA256 == "" || KeyFingerprint() != b.KeySHA256 {
			return ErrEncryptedUnavailable
		}
	}
	if b.Policy.Revision < 1 || ValidatePolicy(b.Policy) != nil {
		return ErrInvalid
	}
	p := b.Policy
	p.ID = 1
	if err := insert(&p, 1); err != nil {
		return err
	}
	for _, wire := range b.Sources {
		row := wire.SubscriptionSource
		row.URLCiphertext, row.URLRevision = wire.Ciphertext, wire.Revision
		row.LeaseToken, row.LeaseUntil = "", 0
		input := SourceInput{Name: row.Name, GroupName: row.GroupName, RefreshMinutes: row.RefreshMinutes}
		if validateInput(&input) != nil || row.ID < 1 || row.RefreshMinutes != input.RefreshMinutes {
			return ErrInvalid
		}
		if row.OwnerAccountID != nil {
			if wire.Scope != *row.OwnerAccountID {
				return ErrInvalid
			}
			var a model.PortalAccount
			if tx.First(&a, *row.OwnerAccountID).Error != nil {
				return ErrInvalid
			}
		} else if wire.Scope != 0 {
			return ErrInvalid
		}
		row.ScopeKey = wire.Scope
		if row.URLCiphertext != "" {
			raw, e := Decrypt("url", row.URLCiphertext)
			if e != nil || ValidateURL(string(raw)) != nil {
				return ErrEncryptedUnavailable
			}
		}
		if err := insert(&row, row.ID); err != nil {
			return err
		}
	}
	for _, wire := range b.Nodes {
		row := wire.SubscriptionNode
		row.PayloadCiphertext, row.ExternalKey = wire.Ciphertext, wire.Key
		if row.ID < 1 || row.SourceID < 1 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(row.ExternalKey) {
			return ErrInvalid
		}
		payload, err := decryptedNode(row)
		if err != nil || payload["name"] != row.Name || payload["type"] != row.ProxyType {
			return ErrEncryptedUnavailable
		}
		raw, _ := json.Marshal(payload)
		row.PlainBytes = len(raw)
		if err := insert(&row, row.ID); err != nil {
			return err
		}
	}
	for _, wire := range b.Routes {
		row := wire.SubscriptionHost
		row.EntryCiphertext = wire.Ciphertext
		if row.HostID < 1 || row.Revision < 1 {
			return ErrInvalid
		}
		// Retired route metadata is archived for old plan signatures and readback.
		// It never configures subscription endpoints or client connections.
		if err := insert(&row, "host_id = ?", row.HostID); err != nil {
			return err
		}
	}
	for _, wire := range b.Sources {
		var nodes []model.SubscriptionNode
		if err := tx.Where("source_id = ? AND is_active = ?", wire.ID, true).Find(&nodes).Error; err != nil {
			return err
		}
		if len(nodes) != wire.NodeCount || len(nodes) > MaxSourceNodes {
			return ErrInvalid
		}
		if err := validateAllBudgets(tx, wire.OwnerAccountID); err != nil {
			return err
		}
	}
	return nil
}

func KeyFingerprint() string {
	// Fingerprints identify the secret material, not the derived encryption key.
	raw := []byte(os.Getenv("XUI_EXTERNAL_SUBSCRIPTION_ENCRYPTION_KEY"))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func ValidateImportedPlans(tx *gorm.DB) error { return validateAllBudgets(tx, nil) }
