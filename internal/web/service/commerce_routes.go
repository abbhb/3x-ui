package service

import (
	"slices"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
)

// Cleanup never grants a replacement endpoint or rewrites historical purchases.
func (s *CommerceService) PrunePlanRoutes() error {
	return submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := subscriptioncompat.Lock(tx); err != nil {
				return err
			}
			var state model.SubscriptionBudgetLock
			if err := tx.First(&state, 1).Error; err != nil {
				return err
			}
			var plans []model.PortalPlan
			if err := tx.Where("id > ?", state.PlanCursor).Order("id").Limit(20).Find(&plans).Error; err != nil {
				return err
			}
			for _, p := range plans {
				state.PlanCursor = p.ID
				signatures, err := subscriptioncompat.HostSignatures(tx, p.HostIDs)
				if err != nil {
					return err
				}
				kept := []int{}
				for _, id := range p.HostIDs {
					key := strconv.Itoa(id)
					if current, exists := signatures[key]; exists && (p.HostSignatures[key] == "" || p.HostSignatures[key] == current) {
						kept = append(kept, id)
					}
				}
				var inbounds []model.Inbound
				if err := tx.Where("id IN ?", p.InboundIDs).Find(&inbounds).Error; err != nil {
					return err
				}
				var hosts []model.Host
				if err := tx.Where("id IN ?", kept).Find(&hosts).Error; err != nil {
					return err
				}
				ids, auto := []int{}, []int{}
				keptInbounds := []model.Inbound{}
				for _, ib := range inbounds {
					if slices.Contains(p.AutoHostInboundIDs, ib.Id) || slices.ContainsFunc(hosts, func(h model.Host) bool { return h.InboundId == ib.Id }) {
						ids = append(ids, ib.Id)
						keptInbounds = append(keptInbounds, ib)
						if slices.Contains(p.AutoHostInboundIDs, ib.Id) {
							auto = append(auto, ib.Id)
						}
					}
				}
				if slices.Equal(ids, p.InboundIDs) && slices.Equal(kept, p.HostIDs) && slices.Equal(auto, p.AutoHostInboundIDs) {
					continue
				}
				p.InboundIDs, p.HostIDs, p.AutoHostInboundIDs = ids, kept, auto
				p.HostSignatures = nil
				if err := subscriptioncompat.CaptureHostSignatures(tx, &p); err != nil {
					return err
				}
				for name, members := range p.GroupMembers {
					p.GroupMembers[name] = slices.DeleteFunc(members, func(ref string) bool {
						if ref == "external" {
							return false
						}
						if len(ref) < len("inbound:") {
							return true
						}
						id, e := strconv.Atoi(ref[len("inbound:"):])
						return e != nil || !slices.Contains(ids, id)
					})
				}
				if len(ids) == 0 && len(p.ExternalSelections) == 0 {
					p.IsVisible = false
				}
				if err := tx.Save(&p).Error; err != nil {
					return err
				}
				if err := s.reconcilePortalPlanTx(tx, p, keptInbounds); err != nil {
					return err
				}
			}
			if len(plans) < 20 {
				state.PlanCursor = 0
			}
			state.LastCleanupAt = time.Now().UnixMilli()
			return tx.Save(&state).Error
		})
	})
}
