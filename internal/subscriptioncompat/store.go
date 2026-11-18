package subscriptioncompat

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type Service struct {
	ParseSource       func([]byte) (Snapshot, error)
	SuppressSecureDNS bool
	DB                *gorm.DB
	Fetch             func(context.Context, string) ([]byte, error)
}

type SourceInput struct {
	Name           string `json:"name"`
	GroupName      string `json:"group_name"`
	Enabled        bool   `json:"enabled"`
	RefreshMinutes int    `json:"refresh_interval_minutes"`
	URL            string `json:"url,omitempty"`
}

func Lock(tx *gorm.DB) error {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.SubscriptionBudgetLock{ID: 1}).Error; err != nil {
		return err
	}
	return tx.Model(&model.SubscriptionBudgetLock{}).Where("id = 1").UpdateColumn("id", gorm.Expr("id")).Error
}

func sourceFor(tx *gorm.DB, id int, owner *int) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	q := tx.Where("id = ?", id)
	if owner != nil {
		q = q.Where("owner_account_id = ?", *owner)
	}
	if q.First(&row).Error != nil {
		return row, ErrNotFound
	}
	return row, nil
}

func safeSource(row model.SubscriptionSource) model.SubscriptionSource {
	row.CredentialConfigured = row.URLCiphertext != ""
	return row
}

func validateInput(in *SourceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.GroupName = strings.TrimSpace(in.GroupName)
	if in.GroupName == "" {
		in.GroupName = in.Name
	}
	if in.RefreshMinutes == 0 {
		in.RefreshMinutes = 360
	}
	if !cleanText(in.Name, 128) || !cleanText(in.GroupName, 128) || in.RefreshMinutes < 15 || in.RefreshMinutes > 43200 {
		return ErrInvalid
	}
	if in.URL != "" {
		return ValidateURL(in.URL)
	}
	return nil
}

func (s *Service) CreateSource(owner *int, in SourceInput) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	if err := validateInput(&in); err != nil {
		return row, err
	}
	cipher := ""
	if in.URL != "" {
		var err error
		cipher, err = Encrypt("url", []byte(in.URL))
		if err != nil {
			return row, err
		}
	}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		scope := 0
		if owner != nil {
			scope = *owner
			var n int64
			if scope < 1 || tx.Model(&model.PortalAccount{}).Where("id = ?", scope).Count(&n).Error != nil || n != 1 {
				return ErrNotFound
			}
			if tx.Model(&model.SubscriptionSource{}).Where("owner_account_id = ?", scope).Count(&n).Error != nil {
				return ErrInvalid
			}
			if n >= MaxPrivateSources {
				return ErrBudget
			}
		}
		row = model.SubscriptionSource{OwnerAccountID: owner, ScopeKey: scope, Name: in.Name, GroupName: in.GroupName, URLCiphertext: cipher, Enabled: in.Enabled, RefreshMinutes: in.RefreshMinutes, URLRevision: 1, Status: "empty"}
		if cipher != "" && in.Enabled {
			row.NextRefreshAt = time.Now().UnixMilli()
		}
		if err := tx.Create(&row).Error; err != nil {
			return ErrConflict
		}
		return nil
	})
	return safeSource(row), err
}

func (s *Service) Sources(owner *int, page int, search string) ([]model.SubscriptionSource, error) {
	rows := []model.SubscriptionSource{}
	if page < 1 || page > 100000 || len(search) > 256 {
		return rows, ErrInvalid
	}
	q := s.DB.Model(&model.SubscriptionSource{}).Order("id").Offset((page - 1) * 100).Limit(100)
	if owner != nil {
		q = q.Where("owner_account_id = ?", *owner)
	}
	if search != "" {
		q = q.Where("(name LIKE ? OR owner_account_id IN (SELECT id FROM portal_accounts WHERE username LIKE ?))", "%"+search+"%", "%"+search+"%")
	}
	if err := q.Find(&rows).Error; err != nil {
		return rows, err
	}
	for i := range rows {
		rows[i] = safeSource(rows[i])
		if rows[i].OwnerAccountID != nil {
			var a model.PortalAccount
			if s.DB.Select("id", "username").First(&a, *rows[i].OwnerAccountID).Error == nil {
				rows[i].OwnerUsername = a.Username
			}
		}
	}
	return rows, nil
}

func (s *Service) Nodes(id int, owner *int, page int) ([]model.SubscriptionNode, error) {
	rows := []model.SubscriptionNode{}
	if page < 1 || page > 100000 {
		return rows, ErrInvalid
	}
	if _, err := sourceFor(s.DB, id, owner); err != nil {
		return rows, err
	}
	err := s.DB.Where("source_id = ?", id).Order("position, id").Offset((page - 1) * 100).Limit(100).Find(&rows).Error
	return rows, err
}

func (s *Service) UpdateSource(id int, owner *int, in SourceInput) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var err error
		row, err = sourceFor(tx, id, owner)
		if err != nil {
			return err
		}
		if in.GroupName == "" {
			in.GroupName = row.GroupName
		}
		if err := validateInput(&in); err != nil {
			return err
		}
		if in.GroupName != row.GroupName || in.URL != "" {
			return ErrInvalid
		}
		row.Name, row.Enabled, row.RefreshMinutes = in.Name, in.Enabled, in.RefreshMinutes
		row.NextRefreshAt = 0
		if row.Enabled && row.URLCiphertext != "" {
			row.NextRefreshAt = time.Now().Add(time.Duration(row.RefreshMinutes) * time.Minute).UnixMilli()
		}
		if err := tx.Save(&row).Error; err != nil {
			return ErrConflict
		}
		return nil
	})
	return safeSource(row), err
}

func (s *Service) ReplaceCredential(id int, owner *int, raw string) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	cipher := ""
	if raw != "" {
		if err := ValidateURL(raw); err != nil {
			return row, err
		}
		var err error
		cipher, err = Encrypt("url", []byte(raw))
		if err != nil {
			return row, err
		}
	}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var err error
		row, err = sourceFor(tx, id, owner)
		if err != nil {
			return err
		}
		row.URLCiphertext = cipher
		row.URLRevision++
		row.LeaseUntil = 0
		row.LeaseToken = ""
		row.NextRefreshAt = 0
		if row.Enabled && cipher != "" {
			row.NextRefreshAt = time.Now().UnixMilli()
		}
		return tx.Save(&row).Error
	})
	return safeSource(row), err
}

func eachPlan(tx *gorm.DB, fn func(model.PortalPlan) error) error {
	last := 0
	for {
		var rows []model.PortalPlan
		if err := tx.Select("id", "external_selections").Where("id > ?", last).Order("id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, p := range rows {
			last = p.ID
			if err := fn(p); err != nil {
				return err
			}
		}
	}
}

func (s *Service) DeleteSource(id int, owner *int) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		if _, err := sourceFor(tx, id, owner); err != nil {
			return err
		}
		if err := eachPlan(tx, func(p model.PortalPlan) error {
			if slices.ContainsFunc(p.ExternalSelections, func(e model.ExternalSelection) bool { return e.SourceID == id }) {
				return ErrConflict
			}
			return nil
		}); err != nil {
			return err
		}
		// Archived route entries no longer grant or depend on external sources.
		if err := tx.Model(&model.SubscriptionHost{}).Where("entry_node_id IN (?)", tx.Model(&model.SubscriptionNode{}).Select("id").Where("source_id = ?", id)).Update("entry_node_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", id).Delete(&model.SubscriptionNode{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.SubscriptionSource{}, id).Error
	})
}

func ValidateSelections(tx *gorm.DB, selections []model.ExternalSelection) error {
	if len(selections) > 64 {
		return ErrInvalid
	}
	seen := map[int]bool{}
	count, total := 0, 0
	for _, sel := range selections {
		source, err := sourceFor(tx, sel.SourceID, nil)
		if err != nil || source.OwnerAccountID != nil || seen[sel.SourceID] {
			return ErrInvalid
		}
		seen[sel.SourceID] = true
		q := tx.Where("source_id = ?", source.ID)
		switch sel.Mode {
		case "all":
			if len(sel.NodeIDs) != 0 {
				return ErrInvalid
			}
		case "selected":
			if len(sel.NodeIDs) == 0 || len(sel.NodeIDs) > MaxSourceNodes {
				return ErrInvalid
			}
			ids := map[int]bool{}
			for _, id := range sel.NodeIDs {
				if id < 1 || ids[id] {
					return ErrInvalid
				}
				ids[id] = true
			}
			var n int64
			if q.Model(&model.SubscriptionNode{}).Where("id IN ?", sel.NodeIDs).Count(&n).Error != nil || n != int64(len(sel.NodeIDs)) {
				return ErrInvalid
			}
			q = q.Where("id IN ?", sel.NodeIDs)
		default:
			return ErrInvalid
		}
		var nodes []model.SubscriptionNode
		if err := q.Where("is_active = ?", true).Find(&nodes).Error; err != nil {
			return err
		}
		for _, node := range nodes {
			count++
			total += node.PlainBytes
		}
		if count > MaxPlanNodes || total > MaxPlanBytes {
			return ErrBudget
		}
	}
	return nil
}

func validateAllBudgets(tx *gorm.DB, owner *int) error {
	if owner != nil {
		var sources []model.SubscriptionSource
		if err := tx.Where("owner_account_id = ?", *owner).Find(&sources).Error; err != nil {
			return err
		}
		if len(sources) > MaxPrivateSources {
			return ErrBudget
		}
		ids := []int{}
		for _, s := range sources {
			ids = append(ids, s.ID)
		}
		var nodes []model.SubscriptionNode
		if err := tx.Where("source_id IN ? AND is_active = ?", ids, true).Find(&nodes).Error; err != nil {
			return err
		}
		total := 0
		for _, n := range nodes {
			total += n.PlainBytes
		}
		if len(nodes) > MaxPrivateNodes || total > MaxPrivateBytes {
			return ErrBudget
		}
		return nil
	}
	return eachPlan(tx, func(p model.PortalPlan) error { return ValidateSelections(tx, p.ExternalSelections) })
}

func (s *Service) publish(tx *gorm.DB, row *model.SubscriptionSource, snapshot Snapshot) error {
	// Retired entries must not keep obsolete external nodes alive through foreign keys.
	if err := tx.Model(&model.SubscriptionHost{}).Where("entry_node_id IN (?)", tx.Model(&model.SubscriptionNode{}).Select("id").Where("source_id = ?", row.ID)).Update("entry_node_id", nil).Error; err != nil {
		return err
	}
	referenced := map[int]bool{}
	if err := eachPlan(tx, func(p model.PortalPlan) error {
		for _, sel := range p.ExternalSelections {
			if sel.SourceID == row.ID {
				for _, id := range sel.NodeIDs {
					referenced[id] = true
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var old []model.SubscriptionNode
	if err := tx.Where("source_id = ?", row.ID).Find(&old).Error; err != nil {
		return err
	}
	byKey := map[string]model.SubscriptionNode{}
	for _, n := range old {
		byKey[n.ExternalKey] = n
	}
	for _, candidate := range snapshot.Nodes {
		plain, err := json.Marshal(candidate.Payload)
		if err != nil {
			return ErrInvalidSnapshot
		}
		cipher, err := Encrypt("proxy", plain)
		if err != nil {
			return err
		}
		node := byKey[candidate.Key]
		node.SourceID, node.ExternalKey, node.Name, node.ProxyType = row.ID, candidate.Key, candidate.Name, candidate.Type
		node.ShareLinkCiphertext = ""
		if candidate.ShareLink != "" {
			node.ShareLinkCiphertext, err = Encrypt("share-link", []byte(candidate.ShareLink))
			if err != nil {
				return err
			}
		}
		node.PayloadCiphertext, node.PlainBytes, node.Position, node.IsActive = cipher, len(plain)+len(candidate.ShareLink), candidate.Position, true
		if err := tx.Save(&node).Error; err != nil {
			return err
		}
		delete(byKey, candidate.Key)
	}
	for _, node := range byKey {
		if referenced[node.ID] {
			if err := tx.Model(&node).Update("is_active", false).Error; err != nil {
				return err
			}
		} else if err := tx.Delete(&node).Error; err != nil {
			return err
		}
	}
	if err := validateAllBudgets(tx, row.OwnerAccountID); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	row.NodeCount, row.SkippedNodeCount = len(snapshot.Nodes), snapshot.Skipped
	row.Status, row.LastErrorCode = "ready", ""
	row.LastAttemptAt, row.LastSuccessAt = now, now
	row.LeaseUntil, row.LeaseToken = 0, ""
	row.NextRefreshAt = 0
	if row.Enabled && row.URLCiphertext != "" {
		row.NextRefreshAt = now + int64(row.RefreshMinutes)*60000
	}
	return tx.Save(row).Error
}

func (s *Service) parseSource(raw []byte) (Snapshot, error) {
	if s.ParseSource != nil {
		return s.ParseSource(raw)
	}
	return ParseSnapshot(raw)
}

func (s *Service) ImportSnapshot(id int, owner *int, raw []byte) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	if _, err := sourceFor(s.DB, id, owner); err != nil {
		return row, err
	}
	snapshot, err := s.parseSource(raw)
	if err != nil {
		return row, err
	}
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var err error
		row, err = sourceFor(tx, id, owner)
		if err != nil {
			return err
		}
		return s.publish(tx, &row, snapshot)
	})
	return safeSource(row), err
}

func (s *Service) Refresh(ctx context.Context, id int, owner *int) (model.SubscriptionSource, error) {
	var row model.SubscriptionSource
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return row, ErrFetch
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := Lock(tx); err != nil {
			return err
		}
		var err error
		row, err = sourceFor(tx, id, owner)
		if err != nil {
			return err
		}
		if row.LeaseUntil > time.Now().UnixMilli() {
			return ErrConflict
		}
		if row.URLCiphertext == "" {
			return ErrInvalid
		}
		row.LeaseUntil, row.LeaseToken = time.Now().Add(time.Minute).UnixMilli(), token
		return tx.Save(&row).Error
	})
	if err != nil {
		return safeSource(row), err
	}
	rawURL, fetchErr := Decrypt("url", row.URLCiphertext)
	var raw []byte
	fetch := s.Fetch
	if fetch == nil {
		fetch = FetchPublic
	}
	if fetchErr == nil {
		raw, fetchErr = fetch(ctx, string(rawURL))
	}
	var snapshot Snapshot
	if fetchErr == nil {
		snapshot, fetchErr = s.parseSource(raw)
	}
	if fetchErr == nil {
		err = s.DB.Transaction(func(tx *gorm.DB) error {
			if err := Lock(tx); err != nil {
				return err
			}
			current, e := sourceFor(tx, id, owner)
			if e != nil {
				return e
			}
			if current.LeaseToken != token || current.URLRevision != row.URLRevision {
				return ErrConflict
			}
			if e = s.publish(tx, &current, snapshot); e != nil {
				return e
			}
			row = current
			return nil
		})
		if err == nil {
			return safeSource(row), nil
		}
		fetchErr = err
	}
	code := "upstream_unavailable"
	if errors.Is(fetchErr, ErrInvalidSnapshot) {
		code = "invalid_snapshot"
	}
	if errors.Is(fetchErr, ErrEncryptedUnavailable) {
		code = "encrypted_data_unavailable"
	}
	if errors.Is(fetchErr, ErrBudget) {
		code = "projection_limit_exceeded"
	}
	result := s.DB.Model(&model.SubscriptionSource{}).Where("id = ? AND lease_token = ? AND url_revision = ?", id, token, row.URLRevision).Updates(map[string]any{"lease_until": 0, "lease_token": "", "last_error_code": code, "status": "error", "last_attempt_at": time.Now().UnixMilli(), "next_refresh_at": time.Now().Add(time.Duration(row.RefreshMinutes) * time.Minute).UnixMilli()})
	if result.Error != nil {
		return safeSource(row), result.Error
	}
	current, e := sourceFor(s.DB, id, owner)
	if e == nil {
		row = current
	}
	return safeSource(row), fetchErr
}

func (s *Service) Tick(ctx context.Context) error {
	var settings model.PortalSettings
	err := s.DB.First(&settings, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	var rows []model.SubscriptionSource
	if err := s.DB.Where("enabled = ? AND url_ciphertext <> ? AND next_refresh_at > 0 AND next_refresh_at <= ? AND lease_until <= ?", true, "", time.Now().UnixMilli(), time.Now().UnixMilli()).Order("next_refresh_at,id").Limit(5).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, _ = s.Refresh(ctx, row.ID, nil)
	}
	return nil
}

func (s *Service) Source(id int, owner *int) (model.SubscriptionSource, error) {
	row, err := sourceFor(s.DB, id, owner)
	return safeSource(row), err
}
