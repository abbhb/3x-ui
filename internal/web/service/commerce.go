package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const (
	portalMaxBalance int64 = 9_000_000_000_000_000
	portalMaxAmount  int64 = 1_000_000_000_000
)

var (
	ErrPortalInvalid     = errors.New("invalid portal request")
	ErrPortalUnavailable = errors.New("portal resource unavailable")
	ErrPortalConflict    = errors.New("idempotency key conflicts with the original request")
	ErrPortalBalance     = errors.New("insufficient wallet balance")
	ErrPortalAuth        = errors.New("invalid credentials or account unavailable")
	portalUsername       = regexp.MustCompile(`^[a-zA-Z0-9_@.-]{3,32}$`)
)

type CommerceService struct{ Now func() time.Time }

func (s *CommerceService) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func portalRandom() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func portalHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

type PortalUsage struct {
	Status              string `json:"status"`
	UsedTraffic         int64  `json:"used_traffic"`
	LifetimeUsedTraffic int64  `json:"lifetime_used_traffic"`
	DataLimit           int64  `json:"data_limit"`
	Expire              int64  `json:"expire"`
}
type PortalAccountView struct {
	ID                 int                       `json:"id"`
	Username           string                    `json:"username"`
	WalletBalanceMinor int64                     `json:"wallet_balance_minor"`
	IsActive           bool                      `json:"is_active"`
	ClientID           *int                      `json:"client_id"`
	UserID             *int64                    `json:"user_id"`
	CreatedAt          int64                     `json:"created_at"`
	Subscription       *model.PortalSubscription `json:"subscription"`
	Usage              PortalUsage               `json:"usage"`
	RuntimePending     bool                      `json:"runtime_pending"`
}

func (s *CommerceService) accountView(tx *gorm.DB, account model.PortalAccount) (PortalAccountView, error) {
	view := PortalAccountView{ID: account.ID, Username: account.Username, WalletBalanceMinor: account.WalletBalanceMinor, IsActive: account.IsActive, ClientID: account.ClientID, CreatedAt: account.CreatedAt, Usage: PortalUsage{Status: "not_activated"}}
	var sub model.PortalSubscription
	if err := tx.First(&sub, "account_id = ?", account.ID).Error; err == nil {
		var plan model.PortalPlan
		if tx.Select("name").First(&plan, sub.PlanID).Error == nil {
			sub.PlanName = plan.Name
		}
		view.Subscription = &sub
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return view, err
	}
	if account.ClientID != nil {
		var rec model.ClientRecord
		if err := tx.First(&rec, *account.ClientID).Error; err == nil {
			var traffic xray.ClientTraffic
			if err := tx.First(&traffic, "email = ?", rec.Email).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return view, err
			}
			counters := []xray.ClientTraffic{traffic}
			overlayGlobalTrafficValues(tx, counters)
			traffic = counters[0]
			var history []model.PortalUsageReset
			if err := tx.Where("client_id = ?", rec.Id).Find(&history).Error; err != nil {
				return view, err
			}
			lifetime := clampedTrafficAdd(traffic.Up, traffic.Down)
			for _, h := range history {
				lifetime = clampedTrafficAdd(lifetime, h.UsedTraffic)
			}
			view.Usage = PortalUsage{LifetimeUsedTraffic: lifetime, Status: "active", UsedTraffic: clampedTrafficAdd(traffic.Up, traffic.Down), DataLimit: rec.TotalGB, Expire: rec.ExpiryTime}
			if !account.IsActive || !rec.Enable {
				view.Usage.Status = "disabled"
			}
			if rec.ExpiryTime > 0 && rec.ExpiryTime <= s.now() {
				view.Usage.Status = "expired"
			}
			if rec.TotalGB > 0 && view.Usage.UsedTraffic >= rec.TotalGB {
				view.Usage.Status = "limited"
			}
			var control model.PortalAccess
			if err := tx.First(&control, "client_id = ?", rec.Id).Error; err == nil && control.ManuallyDisabled {
				view.Usage.Status = "disabled"
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return view, err
			}
			if n, _, ok := strings.Cut(rec.Email, "."); ok {
				if id, err := strconv.ParseInt(n, 10, 64); err == nil && id > 0 {
					view.UserID = &id
				}
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return view, err
		}
	}
	var pending int64
	if err := tx.Model(&model.PortalRuntimeChange{}).Where("account_id = ? AND finished_at = 0", account.ID).Count(&pending).Error; err != nil {
		return view, err
	}
	view.RuntimePending = pending > 0
	return view, nil
}

func (s *CommerceService) Account(id int) (PortalAccountView, error) {
	var a model.PortalAccount
	if err := database.GetDB().First(&a, id).Error; err != nil {
		return PortalAccountView{}, ErrPortalUnavailable
	}
	return s.accountView(database.GetDB(), a)
}

func (s *CommerceService) Accounts(page int, search ...string) ([]PortalAccountView, error) {
	if page < 1 || page > 100000 {
		return nil, ErrPortalInvalid
	}
	var rows []model.PortalAccount
	query := database.GetDB()
	if len(search) > 0 && strings.TrimSpace(search[0]) != "" {
		if len(search[0]) > 64 {
			return nil, ErrPortalInvalid
		}
		value := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(search[0])), "!", "!!"), "%", "!%"), "_", "!_")
		query = query.Where("username LIKE ? ESCAPE '!'", "%"+value+"%")
	}
	if err := query.Order("id").Limit(100).Offset((page - 1) * 100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]PortalAccountView, 0, len(rows))
	for _, row := range rows {
		v, err := s.accountView(database.GetDB(), row)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func (s *CommerceService) Plans(visibleOnly bool) ([]model.PortalPlan, error) {
	rows := []model.PortalPlan{}
	query := database.GetDB().Order("id")
	if visibleOnly {
		query = query.Where("is_visible = ?", true)
	}
	err := query.Find(&rows).Error
	if visibleOnly {
		for i := range rows {
			rows[i].HostSignatures = nil
			rows[i].ExternalSelections = nil
			rows[i].GroupMembers = nil
			rows[i].HostIDs = nil
			rows[i].InboundIDs = nil
			rows[i].AutoHostInboundIDs = nil
		}
	}
	return rows, err
}

func portalValidatePlan(tx *gorm.DB, p *model.PortalPlan) ([]model.Inbound, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	if p.Name == "" || len(p.Name) > 128 || len(p.Description) > 1000 || p.Currency != "CNY" || p.PriceMinor < 0 || p.PriceMinor > portalMaxAmount || p.DurationDays < 1 || p.DurationDays > 3650 || p.DataLimit < 0 || p.DataLimit > 1_000_000_000_000_000_000 || (len(p.InboundIDs) == 0 && len(p.ExternalSelections) == 0) || len(p.InboundIDs) > 128 || (len(p.InboundIDs) > 0 && len(p.HostIDs) == 0 && len(p.AutoHostInboundIDs) == 0) || len(p.HostIDs) > 32768 || p.CompatibilityPending {
		return nil, ErrPortalInvalid
	}
	for _, id := range p.AutoHostInboundIDs {
		if !slices.Contains(p.InboundIDs, id) {
			return nil, ErrPortalInvalid
		}
	}
	if len(uniqueInts(p.AutoHostInboundIDs)) != len(p.AutoHostInboundIDs) {
		return nil, ErrPortalInvalid
	}
	if err := subscriptioncompat.ValidateHostSignatures(tx, *p); err != nil {
		return nil, err
	}
	if err := subscriptioncompat.ValidateSelections(tx, p.ExternalSelections); err != nil {
		return nil, err
	}
	if err := subscriptioncompat.ValidateMembers(tx, *p); err != nil {
		return nil, err
	}
	if len(uniqueInts(p.InboundIDs)) != len(p.InboundIDs) || len(uniqueInts(p.HostIDs)) != len(p.HostIDs) {
		return nil, ErrPortalInvalid
	}
	var inbounds []model.Inbound
	if err := tx.Where("id IN ?", p.InboundIDs).Find(&inbounds).Error; err != nil {
		return nil, err
	}
	if len(inbounds) != len(p.InboundIDs) {
		return nil, ErrPortalUnavailable
	}
	for _, ib := range inbounds {
		if !ib.Enable || (ib.Protocol != model.VLESS && ib.Protocol != model.Hysteria) {
			return nil, ErrPortalInvalid
		}
	}
	if len(p.HostIDs) > 0 {
		var hosts []model.Host
		if err := tx.Where("id IN ?", p.HostIDs).Find(&hosts).Error; err != nil {
			return nil, err
		}
		if len(hosts) != len(p.HostIDs) {
			return nil, ErrPortalUnavailable
		}
		for _, h := range hosts {
			if !slices.Contains(p.InboundIDs, h.InboundId) {
				return nil, ErrPortalInvalid
			}
		}
	}
	return inbounds, nil
}

func (s *CommerceService) SavePlan(id int, p model.PortalPlan) (*model.PortalPlan, error) {
	p.ID = id
	err := submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := subscriptioncompat.Lock(tx); err != nil {
				return err
			}
			var old model.PortalPlan
			if id > 0 {
				if err := tx.First(&old, id).Error; err != nil {
					return ErrPortalUnavailable
				}
				p.CreatedAt = old.CreatedAt
				p.CompatibilityPending = old.CompatibilityPending
				p.LegacyMetadata = old.LegacyMetadata
				if p.HostSignatures == nil {
					p.HostSignatures = old.HostSignatures
				}
			}
			if err := subscriptioncompat.CaptureHostSignatures(tx, &p); err != nil {
				return err
			}
			inbounds, err := portalValidatePlan(tx, &p)
			if err != nil {
				return err
			}
			changed := !slices.Equal(p.InboundIDs, old.InboundIDs) || !slices.Equal(p.HostIDs, old.HostIDs) || !slices.Equal(p.AutoHostInboundIDs, old.AutoHostInboundIDs)
			if err := tx.Save(&p).Error; err != nil {
				return err
			}
			if id == 0 || !changed {
				return nil
			}
			return s.reconcilePortalPlanTx(tx, p, inbounds)
		})
	})
	return &p, err
}

func (s *CommerceService) reconcilePortalPlanTx(tx *gorm.DB, p model.PortalPlan, inbounds []model.Inbound) error {
	last := 0
	for {
		var subscriptions []model.PortalSubscription
		if err := tx.Where("plan_id = ? AND account_id > ?", p.ID, last).Order("account_id").Limit(100).Find(&subscriptions).Error; err != nil {
			return err
		}
		if len(subscriptions) == 0 {
			return nil
		}
		for _, sub := range subscriptions {
			last = sub.AccountID
			if err := tx.Model(&sub).Updates(map[string]any{"inbound_ids": model.EncodeClientHostRuleIds(p.InboundIDs), "host_ids": model.EncodeClientHostRuleIds(p.HostIDs)}).Error; err != nil {
				return err
			}
			var a model.PortalAccount
			if err := tx.First(&a, sub.AccountID).Error; err != nil {
				return err
			}
			if a.ClientID == nil {
				continue
			}
			var rec model.ClientRecord
			if err := tx.First(&rec, *a.ClientID).Error; err != nil {
				return err
			}
			operational := p
			operational.DataLimit = rec.TotalGB
			if _, err := s.applyPortalEntitlement(tx, &a, operational, inbounds, rec.ExpiryTime, false, rec.Enable); err != nil {
				return err
			}
		}
	}
}

func portalSnapshot(p model.PortalPlan) model.PortalPlanSnapshot {
	return model.PortalPlanSnapshot{PlanID: p.ID, PlanName: p.Name, PriceMinor: p.PriceMinor, Currency: p.Currency, DurationDays: p.DurationDays, DataLimit: p.DataLimit, InboundIDs: slices.Clone(p.InboundIDs), HostIDs: slices.Clone(p.HostIDs)}
}

type PortalMutation struct {
	Kind        string `json:"kind"`
	PlanID      int    `json:"plan_id"`
	AmountMinor int64  `json:"amount_minor"`
	Days        int    `json:"days"`
	Note        string `json:"note"`
	Actor       string `json:"actor"`
}
type PortalMutationResult struct {
	PortalAccountView
	PurchaseID      int  `json:"purchase_id"`
	Replayed        bool `json:"replayed"`
	RuntimeChangeID int  `json:"runtime_change_id"`
}

// RunMutation serializes against traffic updates and locks the account before
// reading its balance. The same protocol works across PostgreSQL processes;
// SQLite acquires its write lock before any transaction reads.
func (s *CommerceService) RunMutation(id int, key string, request PortalMutation) (PortalMutationResult, error) {
	var result PortalMutationResult
	err := submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			return s.runPortalMutationTx(tx, id, key, request, &result)
		})
	})
	return result, err
}

func (s *CommerceService) runPortalMutationTx(tx *gorm.DB, id int, key string, request PortalMutation, result *PortalMutationResult) error {
	if id <= 0 || len(key) < 1 || len(key) > 128 || strings.TrimSpace(key) != key || len(request.Note) > 500 {
		return ErrPortalInvalid
	}
	if !slices.Contains([]string{"self_purchase", "admin_grant", "admin_renewal", "admin_credit", "admin_disable"}, request.Kind) {
		return ErrPortalInvalid
	}
	if request.Kind == "self_purchase" {
		request.Actor = ""
	} else if request.Actor == "" {
		return ErrPortalInvalid
	}
	scope := "subscription"
	if request.Kind == "admin_credit" {
		scope = "wallet_credit"
	}
	encoded, _ := json.Marshal(request)
	fingerprint := portalHash(string(encoded))
	locked := tx.Model(&model.PortalAccount{}).Where("id = ?", id).UpdateColumn("wallet_balance_minor", gorm.Expr("wallet_balance_minor"))
	if locked.Error != nil {
		return locked.Error
	}
	if locked.RowsAffected != 1 {
		return ErrPortalUnavailable
	}
	var account model.PortalAccount
	if err := tx.First(&account, id).Error; err != nil {
		return err
	}
	var previous model.PortalAction
	if err := tx.Where("account_id = ? AND scope = ? AND key = ?", id, scope, key).First(&previous).Error; err == nil {
		if previous.RequestHash != fingerprint {
			return ErrPortalConflict
		}
		if json.Unmarshal([]byte(previous.Response), result) != nil {
			return ErrPortalUnavailable
		}
		result.Replayed = true
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if !account.IsActive && request.Kind != "admin_credit" && request.Kind != "admin_disable" {
		return ErrPortalUnavailable
	}
	now := s.now()
	before := account.WalletBalanceMinor
	if request.Kind == "admin_credit" {
		if request.AmountMinor <= 0 || request.AmountMinor > portalMaxAmount || before > portalMaxBalance-request.AmountMinor {
			return ErrPortalInvalid
		}
		account.WalletBalanceMinor += request.AmountMinor
		if err := tx.Model(&account).Update("wallet_balance_minor", account.WalletBalanceMinor).Error; err != nil {
			return err
		}
		ledger := model.PortalWalletTransaction{AccountID: id, Kind: "admin_credit", IdempotencyKey: key, AmountMinor: request.AmountMinor, BalanceAfterMinor: account.WalletBalanceMinor, ActorAdmin: request.Actor, Note: request.Note, CreatedAt: now}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
	} else {
		var sub model.PortalSubscription
		subErr := tx.First(&sub, "account_id = ?", id).Error
		if subErr != nil && !errors.Is(subErr, gorm.ErrRecordNotFound) {
			return subErr
		}
		var purchase model.PortalPurchase
		switch request.Kind {
		case "self_purchase", "admin_grant":
			var plan model.PortalPlan
			if err := tx.First(&plan, request.PlanID).Error; err != nil {
				return ErrPortalUnavailable
			}
			if request.Kind == "self_purchase" && !plan.IsVisible {
				return ErrPortalUnavailable
			}
			inbounds, err := portalValidatePlan(tx, &plan)
			if err != nil {
				return err
			}
			amount := int64(0)
			if request.Kind == "self_purchase" {
				amount = plan.PriceMinor
			}
			debit := tx.Model(&model.PortalAccount{}).Where("id = ? AND wallet_balance_minor >= ?", id, amount).UpdateColumn("wallet_balance_minor", gorm.Expr("wallet_balance_minor - ?", amount))
			if debit.Error != nil {
				return debit.Error
			}
			if debit.RowsAffected != 1 {
				return ErrPortalBalance
			}
			account.WalletBalanceMinor -= amount
			expires := now + int64(plan.DurationDays)*86400000
			change, err := s.applyPortalEntitlement(tx, &account, plan, inbounds, expires, true, true)
			if err != nil {
				return err
			}
			result.RuntimeChangeID = change.ID
			sub = model.PortalSubscription{AccountID: id, PortalPlanSnapshot: portalSnapshot(plan), StartsAt: now, ExpiresAt: expires, PurchasedAt: now, PricePaidMinor: amount}
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			purchase = model.PortalPurchase{AccountID: id, IdempotencyKey: key, Kind: request.Kind, ActorAdmin: request.Actor, PortalPlanSnapshot: portalSnapshot(plan), AmountMinor: amount, BalanceBeforeMinor: before, BalanceAfterMinor: account.WalletBalanceMinor, EffectiveExpiresAt: expires, CreatedAt: now}
		case "admin_renewal":
			if subErr != nil || request.Days < 1 || request.Days > 3650 || account.ClientID == nil {
				return ErrPortalUnavailable
			}
			sub.ExpiresAt = max(now, sub.ExpiresAt) + int64(request.Days)*86400000
			sub.DurationDays += request.Days
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			var rec model.ClientRecord
			if err := tx.First(&rec, *account.ClientID).Error; err != nil {
				return err
			}
			var traffic xray.ClientTraffic
			if err := tx.Where("email = ?", rec.Email).First(&traffic).Error; err != nil {
				return err
			}
			counters := []xray.ClientTraffic{traffic}
			overlayGlobalTrafficValues(tx, counters)
			var access model.PortalAccess
			if err := tx.First(&access, "client_id = ?", rec.Id).Error; err != nil {
				return err
			}
			usable := !access.ManuallyDisabled && sub.DisabledAt == 0 && (rec.Enable || (rec.ExpiryTime > 0 && rec.ExpiryTime <= now)) && (rec.TotalGB == 0 || clampedTrafficAdd(counters[0].Up, counters[0].Down) < rec.TotalGB)
			if err := tx.Model(&model.ClientRecord{}).Where("id = ?", *account.ClientID).Updates(map[string]any{"expiry_time": sub.ExpiresAt, "enable": usable}).Error; err != nil {
				return err
			}
			if err := tx.Model(&xray.ClientTraffic{}).Where("email = ?", rec.Email).Updates(map[string]any{"expiry_time": sub.ExpiresAt, "enable": usable}).Error; err != nil {
				return err
			}
			change, err := s.queuePortalRuntime(tx, id, rec, false)
			if err != nil {
				return err
			}
			result.RuntimeChangeID = change.ID
			purchase = model.PortalPurchase{AccountID: id, IdempotencyKey: key, Kind: request.Kind, ActorAdmin: request.Actor, PortalPlanSnapshot: sub.PortalPlanSnapshot}
			var currentPlan model.PortalPlan
			if err := tx.First(&currentPlan, sub.PlanID).Error; err != nil {
				return err
			}
			purchase.PlanName = currentPlan.Name
			purchase.DurationDays = request.Days
			purchase.AmountMinor = 0
			purchase.BalanceBeforeMinor = before
			purchase.BalanceAfterMinor = before
			purchase.EffectiveExpiresAt = sub.ExpiresAt
			purchase.CreatedAt = now
		case "admin_disable":
			if subErr != nil || account.ClientID == nil {
				return ErrPortalUnavailable
			}
			sub.DisabledAt = now
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			var rec model.ClientRecord
			if err := tx.First(&rec, *account.ClientID).Error; err != nil {
				return err
			}
			if err := portalSetManualEnabled(tx, []string{rec.Email}, false); err != nil {
				return err
			}
			if err := tx.Model(&rec).Update("enable", false).Error; err != nil {
				return err
			}
			if err := tx.Model(&xray.ClientTraffic{}).Where("email = ?", rec.Email).Update("enable", false).Error; err != nil {
				return err
			}
			change, err := s.queuePortalRuntime(tx, id, rec, false)
			if err != nil {
				return err
			}
			result.RuntimeChangeID = change.ID
		}
		if request.Kind != "admin_disable" {
			if err := tx.Create(&purchase).Error; err != nil {
				return err
			}
			result.PurchaseID = purchase.ID
			if purchase.AmountMinor > 0 {
				ledger := model.PortalWalletTransaction{AccountID: id, Kind: "purchase_debit", IdempotencyKey: key, AmountMinor: -purchase.AmountMinor, BalanceAfterMinor: account.WalletBalanceMinor, PurchaseID: &purchase.ID, Note: "Purchase: " + purchase.PlanName, CreatedAt: now}
				if err := tx.Create(&ledger).Error; err != nil {
					return err
				}
			}
		}
	}
	view, err := s.accountView(tx, account)
	if err != nil {
		return err
	}
	result.PortalAccountView = view
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return tx.Create(&model.PortalAction{AccountID: id, Scope: scope, Key: key, RequestHash: fingerprint, Response: string(body), CreatedAt: now}).Error
}

func (s *CommerceService) applyPortalEntitlement(tx *gorm.DB, a *model.PortalAccount, p model.PortalPlan, inbounds []model.Inbound, expires int64, reset, enabled bool) (*model.PortalRuntimeChange, error) {
	var rec model.ClientRecord
	if a.ClientID != nil {
		if err := tx.First(&rec, *a.ClientID).Error; err != nil {
			return nil, err
		}
	}
	if rec.Id == 0 {
		// Reserve before creating the proxy identity, never use the account ID.
		for {
			identity := model.PortalIdentity{}
			if err := tx.Create(&identity).Error; err != nil {
				return nil, err
			}
			if identity.ID <= 0 || identity.ID == math.MaxInt64 {
				return nil, ErrPortalUnavailable
			}
			prefix := strconv.FormatInt(identity.ID, 10) + "."
			var existing int64
			if err := tx.Model(&model.ClientRecord{}).Where("email LIKE ?", prefix+"%").Count(&existing).Error; err != nil {
				return nil, err
			}
			if existing > 0 {
				continue
			}
			stable, err := portalRandom()
			if err != nil {
				return nil, err
			}
			auth, err := portalRandom()
			if err != nil {
				return nil, err
			}
			rec = model.ClientRecord{Email: prefix + a.Username, SubID: stable, UUID: uuid.NewString(), Auth: auth, Enable: true, StrictHostBindings: true, RequireMGMA: true, TrafficReset: "never", TrafficResetDay: 1}
			if err := tx.Create(&rec).Error; err != nil {
				return nil, err
			}
			if err := tx.Model(&identity).Update("client_id", rec.Id).Error; err != nil {
				return nil, err
			}
			a.ClientID = &rec.Id
			if err := tx.Model(a).Update("client_id", rec.Id).Error; err != nil {
				return nil, err
			}
			break
		}
	}
	rec.RequireMGMA = true
	if !rec.StrictHostBindings {
		return nil, errors.New("portal entitlements require a strict migration client")
	}
	for _, inbound := range inbounds {
		if inbound.Protocol == model.VLESS && rec.UUID == "" {
			rec.UUID = uuid.NewString()
		}
		if inbound.Protocol == model.Hysteria && rec.Auth == "" {
			var err error
			rec.Auth, err = portalRandom()
			if err != nil {
				return nil, err
			}
		}
	}
	var oldLinks []model.ClientInbound
	if err := tx.Where("client_id = ?", rec.Id).Find(&oldLinks).Error; err != nil {
		return nil, err
	}
	oldFlows := map[int]string{}
	allIDs := slices.Clone(p.InboundIDs)
	for _, link := range oldLinks {
		oldFlows[link.InboundId] = link.FlowOverride
		allIDs = append(allIDs, link.InboundId)
	}
	if err := tx.Model(&rec).Updates(map[string]any{"uuid": rec.UUID, "auth": rec.Auth, "require_mgma": true, "enable": enabled, "total_gb": p.DataLimit, "expiry_time": expires, "reset": 0, "reset_day": 0, "reset_weekday": 0, "traffic_reset": "never", "client_host_rule_ids": model.EncodeClientHostRuleIds(p.HostIDs), "updated_at": s.now()}).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("client_id = ?", rec.Id).Delete(&model.ClientInbound{}).Error; err != nil {
		return nil, err
	}
	for _, ib := range inbounds {
		flow := ""
		if !ib.DisableFlow && inboundCanEnableTlsFlow(string(ib.Protocol), ib.StreamSettings, ib.Settings) {
			flow = rec.Flow
			if flow == "" {
				flow = "xtls-rprx-vision"
			}
			if old, ok := oldFlows[ib.Id]; ok {
				flow = old
			}
		}
		if err := tx.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: ib.Id, FlowOverride: flow}).Error; err != nil {
			return nil, err
		}
	}
	var traffic xray.ClientTraffic
	if err := tx.Where("email = ?", rec.Email).FirstOrCreate(&traffic, xray.ClientTraffic{Email: rec.Email}).Error; err != nil {
		return nil, err
	}
	updates := map[string]any{"enable": enabled, "total": p.DataLimit, "expiry_time": expires, "reset": 0, "reset_day": 0, "reset_weekday": 0}
	if reset {
		counters := []xray.ClientTraffic{traffic}
		overlayGlobalTrafficValues(tx, counters)
		used := clampedTrafficAdd(counters[0].Up, counters[0].Down)
		if used > 0 {
			if err := tx.Create(&model.PortalUsageReset{ClientID: rec.Id, UsedTraffic: used, ResetAt: s.now()}).Error; err != nil {
				return nil, err
			}
		}
		updates["up"] = 0
		updates["down"] = 0
	}
	if err := tx.Model(&traffic).Updates(updates).Error; err != nil {
		return nil, err
	}
	if reset {
		if err := tx.Where("email = ?", rec.Email).Delete(&model.NodeClientTraffic{}).Error; err != nil {
			return nil, err
		}
		if err := tx.Where("email = ?", rec.Email).Delete(&model.ClientGlobalTraffic{}).Error; err != nil {
			return nil, err
		}
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PortalAccess{ClientID: rec.Id, Generation: 1}).Error; err != nil {
		return nil, err
	}
	if reset && enabled {
		if err := portalSetManualEnabled(tx, []string{rec.Email}, true); err != nil {
			return nil, err
		}
	}
	return s.queuePortalRuntimeForInbounds(tx, a.ID, rec, allIDs, reset)
}

func (s *CommerceService) queuePortalRuntime(tx *gorm.DB, id int, rec model.ClientRecord, reset bool) (*model.PortalRuntimeChange, error) {
	var ids []int
	if err := tx.Model(&model.ClientInbound{}).Where("client_id = ?", rec.Id).Pluck("inbound_id", &ids).Error; err != nil {
		return nil, err
	}
	return s.queuePortalRuntimeForInbounds(tx, id, rec, ids, reset)
}

func (s *CommerceService) queuePortalRuntimeForInbounds(tx *gorm.DB, id int, rec model.ClientRecord, ids []int, reset bool) (*model.PortalRuntimeChange, error) {
	var inbounds []model.Inbound
	if len(ids) > 0 {
		if err := tx.Where("id IN ?", uniqueInts(ids)).Find(&inbounds).Error; err != nil {
			return nil, err
		}
	}
	nodes := []int{}
	for _, ib := range inbounds {
		node := 0
		if ib.NodeID != nil {
			node = *ib.NodeID
		}
		if !slices.Contains(nodes, node) {
			nodes = append(nodes, node)
		}
	}
	for _, node := range nodes {
		if node > 0 {
			if err := (&NodeService{}).MarkNodeDirtyTx(tx, node); err != nil {
				return nil, err
			}
			if reset {
				row := model.NodePendingReset{NodeId: node, Email: rec.Email, QueuedAt: time.Now().UnixNano()}
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}, {Name: "email"}}, DoUpdates: clause.AssignmentColumns([]string{"queued_at"})}).Create(&row).Error; err != nil {
					return nil, err
				}
			}
		}
	}
	change := &model.PortalRuntimeChange{AccountID: id, ClientID: rec.Id, NodeIDs: nodes, ResetTraffic: reset, LocalResetPending: reset && slices.Contains(nodes, 0)}
	return change, tx.Create(change).Error
}

func (s *CommerceService) Wallet(id, page int) ([]model.PortalWalletTransaction, error) {
	rows := []model.PortalWalletTransaction{}
	if page < 1 || page > 100000 {
		return nil, ErrPortalInvalid
	}
	err := database.GetDB().Where("account_id = ?", id).Order("id DESC").Limit(100).Offset((page - 1) * 100).Find(&rows).Error
	return rows, err
}

func (s *CommerceService) SetAccountEnabled(id int, enabled bool) error {
	return submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var a model.PortalAccount
			if err := tx.First(&a, id).Error; err != nil {
				return ErrPortalUnavailable
			}
			if err := tx.Model(&a).Updates(map[string]any{"is_active": enabled, "auth_version": gorm.Expr("auth_version + 1")}).Error; err != nil {
				return err
			}
			if !enabled && a.ClientID != nil {
				var rec model.ClientRecord
				if err := tx.First(&rec, *a.ClientID).Error; err != nil {
					return err
				}
				if err := portalSetManualEnabled(tx, []string{rec.Email}, false); err != nil {
					return err
				}
				if err := tx.Model(&rec).Update("enable", false).Error; err != nil {
					return err
				}
				if err := tx.Model(&xray.ClientTraffic{}).Where("email = ?", rec.Email).Update("enable", false).Error; err != nil {
					return err
				}
				_, err := s.queuePortalRuntime(tx, id, rec, false)
				return err
			}
			return nil
		})
	})
}

// Called by the periodic outbox worker, not inside a money transaction.
func (s *CommerceService) SyncPortalRuntime() error {
	var changes []model.PortalRuntimeChange
	if err := database.GetDB().Where("finished_at = 0").Order("last_attempt_at,id").Limit(100).Find(&changes).Error; err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	hasLocal, resetLocal, lastLocal := portalHasLocalChange(changes)
	localReady := !hasLocal
	if hasLocal && (&XrayService{}).IsXrayRunning() {
		if err := (&XrayService{}).RestartXray(resetLocal); err == nil {
			if err := database.GetDB().Model(&model.PortalRuntimeChange{}).Where("id <= ? AND local_reset_pending = ?", lastLocal, true).Update("local_reset_pending", false).Error; err != nil {
				return err
			}
			localReady = true
		}
	}
	for _, change := range changes {
		pending := false
		for _, node := range change.NodeIDs {
			if node == 0 {
				if !localReady {
					pending = true
				}
			} else {
				var row model.Node
				if err := database.GetDB().First(&row, node).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				} else if err != nil {
					return err
				}
				if row.ConfigDirty {
					pending = true
				}
				var resets int64
				if err := database.GetDB().Model(&model.NodePendingReset{}).Where("node_id = ?", node).Count(&resets).Error; err != nil {
					return err
				}
				if resets > 0 {
					pending = true
				}
			}
		}
		values := map[string]any{"last_error": "waiting for service reconciliation", "last_attempt_at": s.now()}
		if !pending {
			values = map[string]any{"last_error": "", "finished_at": s.now(), "last_attempt_at": s.now()}
		}
		if err := database.GetDB().Model(&change).Updates(values).Error; err != nil {
			return err
		}
	}
	return nil
}
