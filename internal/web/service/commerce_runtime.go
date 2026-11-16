package service

import (
	"errors"
	"math"
	"slices"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// A committed reset waits for a fresh local core generation. Discard its old
// stats during that interval instead of charging the previous period again.
func portalFilterPendingLocalTraffic(tx *gorm.DB, rows []*xray.ClientTraffic) ([]*xray.ClientTraffic, error) {
	if !tx.Migrator().HasTable(&model.PortalRuntimeChange{}) {
		return rows, nil
	}
	var emails []string
	if err := tx.Table("portal_runtime_changes p").Select("DISTINCT c.email").Joins("JOIN clients c ON c.id = p.client_id").Where("p.local_reset_pending = ? AND p.finished_at = 0", true).Pluck("c.email", &emails).Error; err != nil {
		return nil, err
	}
	if len(emails) == 0 {
		return rows, nil
	}
	blocked := make(map[string]bool, len(emails))
	for _, email := range emails {
		blocked[email] = true
	}
	result := make([]*xray.ClientTraffic, 0, len(rows))
	for _, row := range rows {
		if row != nil && !blocked[row.Email] {
			result = append(result, row)
		}
	}
	return result, nil
}

func (s *CommerceService) SaveNextPlan(plan model.PortalNextPlan) error {
	if plan.ClientID <= 0 || plan.DataLimit < 0 || plan.DataLimit > 1_000_000_000_000_000_000 || plan.Expire < 0 || plan.Expire > math.MaxInt64/1000 {
		return ErrPortalInvalid
	}
	var rec model.ClientRecord
	if database.GetDB().First(&rec, plan.ClientID).Error != nil {
		return ErrPortalUnavailable
	}
	return database.GetDB().Save(&plan).Error
}

func (s *CommerceService) ApplyNextPlans() error {
	last := 0
	var batchError error
	for {
		var rows []model.PortalNextPlan
		if err := database.GetDB().Where("client_id > ?", last).Order("client_id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, plan := range rows {
			last = plan.ClientID
			if err := submitTrafficWrite(func() error {
				return database.GetDB().Transaction(func(tx *gorm.DB) error {
					lock := tx.Model(&model.PortalNextPlan{}).Where("client_id = ?", plan.ClientID).UpdateColumn("paused", gorm.Expr("paused"))
					if lock.Error != nil {
						return lock.Error
					}
					if lock.RowsAffected == 0 {
						return nil
					}
					if err := tx.First(&plan, "client_id = ?", plan.ClientID).Error; err != nil {
						return err
					}
					var rec model.ClientRecord
					if err := tx.First(&rec, plan.ClientID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
						return tx.Delete(&plan).Error
					} else if err != nil {
						return err
					}
					var access model.PortalAccess
					if err := tx.First(&access, "client_id = ?", rec.Id).Error; err == nil && access.ManuallyDisabled {
						return nil
					} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					if plan.Paused {
						if !rec.Enable {
							return nil
						}
						plan.Paused = false
						if err := tx.Save(&plan).Error; err != nil {
							return err
						}
					}
					var traffic xray.ClientTraffic
					if err := tx.Where("email = ?", rec.Email).First(&traffic).Error; err != nil {
						return err
					}
					used := clampedTrafficAdd(traffic.Up, traffic.Down)
					limited := rec.TotalGB > 0 && used >= rec.TotalGB
					expired := rec.ExpiryTime > 0 && rec.ExpiryTime <= s.now()
					if (!limited && !expired) || (!plan.FireOnEither && (!limited || !expired)) {
						return nil
					}
					// Preserve the source implementation's flag semantics exactly: false
					// carries the remaining allowance. Invalid negative carry fails closed.
					limit := plan.DataLimit
					if !plan.AddRemainingTraffic {
						if rec.TotalGB < used || limit > math.MaxInt64-(rec.TotalGB-used) {
							return ErrPortalInvalid
						}
						limit += rec.TotalGB - used
					}
					expire := plan.Expire * 1000
					if err := tx.Create(&model.PortalUsageReset{ClientID: rec.Id, UsedTraffic: used, ResetAt: s.now()}).Error; err != nil {
						return err
					}
					if err := tx.Model(&rec).Updates(map[string]any{"enable": true, "total_gb": limit, "expiry_time": expire}).Error; err != nil {
						return err
					}
					if err := tx.Model(&traffic).Updates(map[string]any{"enable": true, "total": limit, "expiry_time": expire, "up": 0, "down": 0}).Error; err != nil {
						return err
					}
					if err := tx.Where("email = ?", rec.Email).Delete(&model.NodeClientTraffic{}).Error; err != nil {
						return err
					}
					if err := tx.Where("email = ?", rec.Email).Delete(&model.ClientGlobalTraffic{}).Error; err != nil {
						return err
					}
					var account model.PortalAccount
					accountErr := tx.Where("client_id = ?", rec.Id).First(&account).Error
					if accountErr != nil && !errors.Is(accountErr, gorm.ErrRecordNotFound) {
						return accountErr
					}
					if account.ID > 0 {
						if err := tx.Model(&model.PortalSubscription{}).Where("account_id = ?", account.ID).Updates(map[string]any{"data_limit": limit, "expires_at": expire}).Error; err != nil {
							return err
						}
					}
					if _, err := s.queuePortalRuntime(tx, account.ID, rec, true); err != nil {
						return err
					}
					return tx.Delete(&plan).Error
				})
			}); err != nil {
				batchError = ErrPortalUnavailable
				if saveErr := database.GetDB().Model(&model.PortalNextPlan{}).Where("client_id = ?", plan.ClientID).Update("last_error", "queued plan could not be applied; check allowance and expiry").Error; saveErr != nil {
					return saveErr
				}
			}
		}
	}
	return batchError
}

func (s *CommerceService) Tick() error {
	p, err := s.Settings()
	if err != nil {
		return err
	}
	if !p.Enabled {
		return nil
	}
	nextErr := s.ApplyNextPlans()
	return errors.Join(nextErr, s.SyncPortalRuntime())
}

func portalHasLocalChange(changes []model.PortalRuntimeChange) (bool, bool, int) {
	has, reset, last := false, false, 0
	for _, change := range changes {
		if slices.Contains(change.NodeIDs, 0) {
			has = true
			reset = reset || change.LocalResetPending
			last = max(last, change.ID)
		}
	}
	return has, reset, last
}

// Only explicit administrator controls write this reason. The quota/expiry
// workers change native enable flags without turning them into manual disables.
func portalSetManualEnabled(tx *gorm.DB, emails []string, enabled bool) error {
	if !tx.Migrator().HasTable(&model.PortalAccess{}) {
		return nil
	}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		clients := tx.Model(&model.ClientRecord{}).Select("id").Where("email IN ?", batch)
		if err := tx.Model(&model.PortalAccess{}).Where("client_id IN (?)", clients).Update("manually_disabled", !enabled).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.PortalNextPlan{}) {
			if err := tx.Model(&model.PortalNextPlan{}).Where("client_id IN (?)", clients).Update("paused", !enabled).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
