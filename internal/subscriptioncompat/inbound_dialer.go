package subscriptioncompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Each credential belongs to its native inbound. Generic inbound edits and node
// sync cannot overwrite the read-only model field. Only this service writes it.
const (
	dialerKind  = "mihomo-inbound-dialer"
	dialerLimit = 256 << 10
)

type InboundDialerView struct {
	Config   string `json:"config,omitempty"`
	Revision string `json:"revision"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Server   string `json:"server,omitempty"`
	Port     int    `json:"port,omitempty"`
}

func dialerView(ciphertext string, includeConfig bool) (InboundDialerView, error) {
	digest := sha256.Sum256([]byte(ciphertext))
	view := InboundDialerView{Revision: hex.EncodeToString(digest[:])}
	if ciphertext == "" {
		return view, nil
	}
	raw, err := Decrypt(dialerKind, ciphertext)
	if err != nil {
		return view, err
	}
	node, err := decodeInboundDialer(raw)
	if err != nil {
		return view, err
	}
	if includeConfig {
		// Format the stored JSON directly so credential strings and numeric literals
		// round-trip through the editor. Large entries must still fit the save limit.
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, raw, "", "  "); err != nil {
			return view, ErrInvalid
		}
		if formatted.Len() <= dialerLimit {
			view.Config = formatted.String()
		} else {
			view.Config = string(raw)
		}
	}
	view.Name, _ = node["name"].(string)
	view.Type, _ = node["type"].(string)
	view.Server, _ = node["server"].(string)
	view.Port, _ = node["port"].(int)
	return view, nil
}

func OpenInboundDialer(ciphertext string) (map[string]any, error) {
	raw, err := Decrypt(dialerKind, ciphertext)
	if err != nil {
		return nil, err
	}
	return decodeInboundDialer(raw)
}

func decodeInboundDialer(raw []byte) (map[string]any, error) {
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, ErrInvalid
	}
	return NormalizeStandaloneProxy(node)
}

func sealDialer(v any) (string, error) {
	node, err := NormalizeStandaloneProxy(v)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(node)
	if err != nil {
		return "", ErrInvalid
	}
	return Encrypt(dialerKind, b)
}

func parseDialer(input string) (any, error) {
	if len(input) > dialerLimit {
		return nil, ErrInvalid
	}
	// Wrap the snippet to accept both a standalone mapping and a one-item YAML list.
	doc, err := DecodeDocument([]byte("node:\n  " + strings.ReplaceAll(strings.TrimSpace(input), "\n", "\n  ")))
	if err != nil {
		return nil, err
	}
	item := doc["node"]
	if nodes, ok := item.([]any); ok {
		if len(nodes) != 1 {
			return nil, ErrInvalid
		}
		item = nodes[0]
	}
	return item, nil
}

func (s *Service) ownedDialer(userID, id int) (string, error) {
	var inbound model.Inbound
	err := s.DB.Select("mihomo_dialer").Where("id = ? AND user_id = ?", id, userID).Take(&inbound).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	return inbound.MihomoDialer, err
}

func (s *Service) InboundDialer(userID, id int, includeConfig bool) (InboundDialerView, error) {
	old, err := s.ownedDialer(userID, id)
	if err != nil {
		return InboundDialerView{}, err
	}
	return dialerView(old, includeConfig)
}

func (s *Service) SetInboundDialer(userID, id int, revision string, config *string, remove bool) (InboundDialerView, error) {
	if (config != nil) == remove {
		return InboundDialerView{}, ErrInvalid
	}
	old, err := s.ownedDialer(userID, id)
	if err != nil {
		return InboundDialerView{}, err
	}
	digest := sha256.Sum256([]byte(old))
	if hex.EncodeToString(digest[:]) != revision {
		return InboundDialerView{}, ErrConflict
	}
	next := ""
	if config != nil {
		node, err := parseDialer(*config)
		if err != nil {
			return InboundDialerView{}, err
		}
		next, err = sealDialer(node)
		if err != nil {
			return InboundDialerView{}, err
		}
	}
	result := s.DB.Table("inbounds").Where("id = ? AND user_id = ? AND COALESCE(mihomo_dialer, '') = ?", id, userID, old).UpdateColumn("mihomo_dialer", next)
	if result.Error != nil {
		return InboundDialerView{}, result.Error
	}
	if result.RowsAffected != 1 {
		return InboundDialerView{}, ErrConflict
	}
	return dialerView(next, false)
}

// Import the file used by the first release once, before accepting traffic.
// The transaction marker also prevents a deleted hop from coming back on restart.
func MigrateInboundDialers(db *gorm.DB, path string) error {
	if path == "" {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		const marker = "StandaloneMihomoInboundDialers"
		var count int64
		if err := tx.Model(&model.HistoryOfSeeders{}).Where("seeder_name = ?", marker).Count(&count).Error; err != nil || count != 0 {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, MaxSourceBytes+1))
		if err != nil {
			return err
		}
		rows, err := DecodeDocument(b)
		if err != nil {
			return err
		}
		for tag, node := range rows {
			sealed, err := sealDialer(node)
			if err != nil {
				return err
			}
			r := tx.Table("inbounds").Where("tag = ? AND mihomo_dialer IS NULL", tag).UpdateColumn("mihomo_dialer", sealed)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return ErrConflict
			}
		}
		return tx.Create(&model.HistoryOfSeeders{SeederName: marker}).Error
	})
}
