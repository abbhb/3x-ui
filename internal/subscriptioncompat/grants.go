package subscriptioncompat

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type HostChoice struct {
	HostID    int    `json:"host_id"`
	InboundID int    `json:"inbound_id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Disabled  bool   `json:"disabled"`
	Signature string `json:"signature"`
}

func HostSignatures(tx *gorm.DB, ids []int) (map[string]string, error) {
	signatures := map[string]string{}
	for start := 0; start < len(ids); start += 400 {
		part := ids[start:min(start+400, len(ids))]
		var hosts []model.Host
		if err := tx.Where("id IN ?", part).Find(&hosts).Error; err != nil {
			return nil, err
		}
		// Preserve stored plan identities across removal of commerce route overrides.
		// The archived client stream contributes only to this historical signature.
		var routes []model.SubscriptionHost
		if err := tx.Where("host_id IN ?", part).Find(&routes).Error; err != nil {
			return nil, err
		}
		routeByID := map[int]string{}
		for _, r := range routes {
			routeByID[r.HostID] = r.ClientStream
		}
		inboundByID := map[int]string{}
		for _, h := range hosts {
			if _, ok := inboundByID[h.InboundId]; !ok {
				var ib model.Inbound
				if err := tx.Select("id", "stream_settings").First(&ib, h.InboundId).Error; err != nil {
					return nil, err
				}
				inboundByID[h.InboundId] = ib.StreamSettings
			}
			id := h.Id
			h.Id = 0
			h.GroupId = ""
			h.Remark = ""
			h.SortOrder = 0
			h.ServerDescription = ""
			h.IsDisabled = false
			h.IsHidden = false
			h.Tags = nil
			h.CreatedAt = 0
			h.UpdatedAt = 0
			var stream, client any
			if strings.TrimSpace(inboundByID[h.InboundId]) != "" && json.Unmarshal([]byte(inboundByID[h.InboundId]), &stream) != nil {
				return nil, ErrInvalid
			}
			if raw := routeByID[id]; raw != "" {
				if json.Unmarshal([]byte(raw), &client) != nil {
					return nil, ErrInvalid
				}
			}
			raw, err := canonicalJSON([]any{h, stream, client})
			if err != nil {
				return nil, err
			}
			signatures[strconv.Itoa(id)] = hashText(string(raw))
		}
	}
	return signatures, nil
}

func CaptureHostSignatures(tx *gorm.DB, p *model.PortalPlan) error {
	signatures, err := HostSignatures(tx, p.HostIDs)
	if err != nil {
		return err
	}
	if len(signatures) != len(p.HostIDs) {
		return ErrInvalid
	}
	for key, expected := range p.HostSignatures {
		if current, exists := signatures[key]; exists && expected != current {
			return ErrConflict
		}
	}
	p.HostSignatures = signatures
	return nil
}

func ValidateHostSignatures(tx *gorm.DB, p model.PortalPlan) error {
	if len(p.HostSignatures) == 0 {
		return nil
	}
	signatures, err := HostSignatures(tx, p.HostIDs)
	if err != nil {
		return err
	}
	for _, id := range p.HostIDs {
		key := strconv.Itoa(id)
		if p.HostSignatures[key] != "" && signatures[key] != p.HostSignatures[key] {
			return ErrConflict
		}
	}
	return nil
}

func EffectiveHostGrants(tx *gorm.DB, email string) ([]int, bool, error) {
	var a model.PortalAccount
	err := tx.Where("client_id IN (?)", tx.Model(&model.ClientRecord{}).Select("id").Where("email = ?", email)).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var sub model.PortalSubscription
	err = tx.First(&sub, a.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !a.IsActive || sub.DisabledAt != 0 {
		return []int{}, true, nil
	}
	var p model.PortalPlan
	if err = tx.First(&p, sub.PlanID).Error; err != nil {
		return nil, true, err
	}
	signatures, err := HostSignatures(tx, p.HostIDs)
	if err != nil {
		return nil, true, err
	}
	ids := []int{}
	for _, id := range p.HostIDs {
		key := strconv.Itoa(id)
		if current, exists := signatures[key]; exists && (p.HostSignatures[key] == "" || p.HostSignatures[key] == current) {
			ids = append(ids, id)
		}
	}
	var auto []int
	if len(p.AutoHostInboundIDs) > 0 {
		if err := tx.Model(&model.Host{}).Where("inbound_id IN ?", p.AutoHostInboundIDs).Order("id").Pluck("id", &auto).Error; err != nil {
			return nil, true, err
		}
		for _, id := range auto {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, true, nil
}

func (s *Service) HostCatalog() ([]HostChoice, error) {
	result := []HostChoice{}
	last := 0
	for {
		var hosts []model.Host
		if err := s.DB.Where("id > ?", last).Order("id").Limit(100).Find(&hosts).Error; err != nil {
			return nil, err
		}
		if len(hosts) == 0 {
			return result, nil
		}
		ids := []int{}
		for _, h := range hosts {
			ids = append(ids, h.Id)
		}
		signatures, err := HostSignatures(s.DB, ids)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			last = h.Id
			result = append(result, HostChoice{HostID: h.Id, InboundID: h.InboundId, Name: h.Remark, Address: h.Address, Disabled: h.IsDisabled, Signature: signatures[strconv.Itoa(h.Id)]})
		}
		if len(result) > 32768 {
			return nil, ErrBudget
		}
	}
}
