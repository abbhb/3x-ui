package model

import (
	"errors"

	"gorm.io/gorm"
)

// PortalAccount is a customer login. It never confers panel administrator access.
type PortalAccount struct {
	ID                 int           `json:"id" gorm:"primaryKey"`
	Username           string        `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash       string        `json:"-"`
	WalletBalanceMinor int64         `json:"wallet_balance_minor" gorm:"not null;default:0;check:portal_balance_nonnegative,wallet_balance_minor >= 0"`
	IsActive           bool          `json:"is_active" gorm:"not null"`
	ClientID           *int          `json:"client_id" gorm:"uniqueIndex"`
	ProxyClient        *ClientRecord `json:"-" gorm:"foreignKey:ClientID;constraint:OnDelete:SET NULL"`
	AuthVersion        int64         `json:"-" gorm:"not null;default:1"`
	CreatedAt          int64         `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt          int64         `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

// PortalIdentity reserves a permanent egress identity, including deleted users.
type PortalIdentity struct {
	ID          int64         `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientID    *int          `json:"client_id" gorm:"uniqueIndex"`
	ProxyClient *ClientRecord `json:"-" gorm:"foreignKey:ClientID;constraint:OnDelete:SET NULL"`
}

type PortalPlan struct {
	CompatibilityPending bool   `json:"compatibility_pending"`
	LegacyMetadata       string `json:"-"`
	ID                   int    `json:"id" gorm:"primaryKey"`
	Name                 string `json:"name" gorm:"uniqueIndex;not null"`
	Description          string `json:"description"`
	PriceMinor           int64  `json:"price_minor" gorm:"not null;check:portal_price_nonnegative,price_minor >= 0"`
	Currency             string `json:"currency"`
	DurationDays         int    `json:"duration_days"`
	DataLimit            int64  `json:"data_limit"`
	InboundIDs           []int  `json:"inbound_ids" gorm:"serializer:json"`
	HostIDs              []int  `json:"host_ids" gorm:"serializer:json"`
	IsVisible            bool   `json:"is_visible"`
	CreatedAt            int64  `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt            int64  `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

// Financial terms are historical; current route grants can follow plan edits.
type PortalPlanSnapshot struct {
	PlanID       int    `json:"plan_id"`
	PlanName     string `json:"plan_name"`
	PriceMinor   int64  `json:"price_minor"`
	Currency     string `json:"currency"`
	DurationDays int    `json:"duration_days"`
	DataLimit    int64  `json:"data_limit"`
	InboundIDs   []int  `json:"inbound_ids" gorm:"serializer:json"`
	HostIDs      []int  `json:"host_ids" gorm:"serializer:json"`
}

type PortalSubscription struct {
	AccountID          int `json:"account_id" gorm:"primaryKey;autoIncrement:false"`
	PortalPlanSnapshot `gorm:"embedded"`
	StartsAt           int64 `json:"starts_at"`
	PricePaidMinor     int64 `json:"price_paid_minor"`
	ExpiresAt          int64 `json:"expires_at"`
	PurchasedAt        int64 `json:"purchased_at"`
	DisabledAt         int64 `json:"disabled_at"`
}

type PortalPurchase struct {
	ID                 int    `json:"id" gorm:"primaryKey"`
	AccountID          int    `json:"account_id" gorm:"index;uniqueIndex:portal_purchase_key,priority:1"`
	IdempotencyKey     string `json:"-" gorm:"uniqueIndex:portal_purchase_key,priority:2"`
	Kind               string `json:"kind"`
	ActorAdmin         string `json:"actor_admin"`
	PortalPlanSnapshot `gorm:"embedded"`
	AmountMinor        int64 `json:"amount_minor"`
	BalanceBeforeMinor int64 `json:"balance_before_minor"`
	BalanceAfterMinor  int64 `json:"balance_after_minor"`
	EffectiveExpiresAt int64 `json:"effective_expires_at"`
	CreatedAt          int64 `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalWalletTransaction struct {
	ID                int    `json:"id" gorm:"primaryKey"`
	AccountID         int    `json:"account_id" gorm:"index;uniqueIndex:portal_wallet_key,priority:1"`
	Kind              string `json:"kind" gorm:"uniqueIndex:portal_wallet_key,priority:2"`
	IdempotencyKey    string `json:"-" gorm:"uniqueIndex:portal_wallet_key,priority:3"`
	AmountMinor       int64  `json:"amount_minor"`
	BalanceAfterMinor int64  `json:"balance_after_minor"`
	ActorAdmin        string `json:"actor_admin"`
	PurchaseID        *int   `json:"purchase_id"`
	Note              string `json:"note"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalAction struct {
	ID          int    `json:"-" gorm:"primaryKey"`
	AccountID   int    `json:"-" gorm:"uniqueIndex:portal_action_key,priority:1"`
	Scope       string `json:"-" gorm:"uniqueIndex:portal_action_key,priority:2"`
	Key         string `json:"-" gorm:"uniqueIndex:portal_action_key,priority:3"`
	RequestHash string `json:"-"`
	Response    string `json:"-"`
	CreatedAt   int64  `json:"-" gorm:"autoCreateTime:milli"`
}

type PortalInvitation struct {
	ID         int    `json:"id" gorm:"primaryKey"`
	Digest     string `json:"-" gorm:"uniqueIndex"`
	Prefix     string `json:"prefix"`
	Note       string `json:"note"`
	StartsAt   int64  `json:"starts_at"`
	ExpiresAt  int64  `json:"expires_at"`
	MaxUses    int64  `json:"max_uses"`
	UseCount   int64  `json:"use_count"`
	DisabledAt int64  `json:"disabled_at"`
	ActorAdmin string `json:"actor_admin"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalInvitationUse struct {
	ID           int    `json:"id" gorm:"primaryKey"`
	InvitationID int    `json:"invitation_id" gorm:"index"`
	AccountID    int    `json:"account_id" gorm:"uniqueIndex"`
	SourceIP     string `json:"source_ip"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalIPBlock struct {
	ID         int    `json:"id" gorm:"primaryKey"`
	Network    string `json:"network" gorm:"uniqueIndex"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	ActorAdmin string `json:"actor_admin"`
	ExpiresAt  int64  `json:"expires_at"`
	RevokedAt  int64  `json:"revoked_at"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalSecurityAttempt struct {
	ID           int    `json:"-" gorm:"primaryKey"`
	SourceIP     string `json:"-" gorm:"uniqueIndex:portal_attempt_key,priority:1"`
	Kind         string `json:"-" gorm:"uniqueIndex:portal_attempt_key,priority:2"`
	WindowStart  int64  `json:"-"`
	FailureCount int    `json:"-"`
	UpdatedAt    int64  `json:"-" gorm:"autoUpdateTime:milli"`
}

type PortalSettings struct {
	ID                        int      `json:"-" gorm:"primaryKey;autoIncrement:false"`
	AutoBlockEnabled          bool     `json:"auto_block_enabled"`
	Enabled                   bool     `json:"enabled"`
	PublicURL                 string   `json:"public_url"`
	Mode                      string   `json:"mode"`
	TTLSeconds                int64    `json:"ttl_seconds"`
	SingleUse                 bool     `json:"single_use"`
	SourceMode                string   `json:"source_mode"`
	CustomCIDRs               []string `json:"custom_cidrs" gorm:"serializer:json"`
	LoginFailureLimit         int      `json:"login_failure_limit"`
	RegistrationFailureLimit  int      `json:"registration_failure_limit"`
	LoginWindowSeconds        int64    `json:"login_window_seconds"`
	RegistrationWindowSeconds int64    `json:"registration_window_seconds"`
	AutoBlockSeconds          int64    `json:"auto_block_seconds"`
}

// MGMA state stores a keyed digest, never the bearer or public URL.
type PortalAccess struct {
	ManuallyDisabled bool   `json:"-"`
	ClientID         int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Digest           string `json:"-" gorm:"index"`
	IssuedAt         int64  `json:"-"`
	ExpiresAt        int64  `json:"-"`
	ConsumedAt       int64  `json:"-"`
	Generation       int64  `json:"-"`
}

// Only imported pre-MGMA users may resolve a source-signed legacy path.
type PortalLegacyUser struct {
	ClientID  int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Username  string `json:"-" gorm:"uniqueIndex"`
	CreatedAt int64  `json:"-"`
	RevokedAt int64  `json:"-"`
}

type PortalNextPlan struct {
	LastError           string `json:"last_error"`
	ClientID            int    `json:"client_id" gorm:"primaryKey;autoIncrement:false"`
	DataLimit           int64  `json:"data_limit"`
	Expire              int64  `json:"expire"`
	AddRemainingTraffic bool   `json:"add_remaining_traffic"`
	FireOnEither        bool   `json:"fire_on_either"`
	Paused              bool   `json:"paused"`
}

// Runtime work is committed with the money/entitlement change, then retried.
type PortalRuntimeChange struct {
	LastAttemptAt     int64  `json:"last_attempt_at"`
	ID                int    `json:"id" gorm:"primaryKey"`
	AccountID         int    `json:"account_id" gorm:"index"`
	ClientID          int    `json:"client_id"`
	NodeIDs           []int  `json:"-" gorm:"serializer:json"`
	ResetTraffic      bool   `json:"-"`
	LocalResetPending bool   `json:"-"`
	FinishedAt        int64  `json:"finished_at"`
	LastError         string `json:"last_error"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

type PortalUsageReset struct {
	ID          int   `json:"id" gorm:"primaryKey"`
	ClientID    int   `json:"client_id" gorm:"index"`
	UsedTraffic int64 `json:"used_traffic"`
	ResetAt     int64 `json:"reset_at"`
}

var errPortalHistoryImmutable = errors.New("portal history is append-only")

func (*PortalPurchase) BeforeUpdate(*gorm.DB) error          { return errPortalHistoryImmutable }
func (*PortalPurchase) BeforeDelete(*gorm.DB) error          { return errPortalHistoryImmutable }
func (*PortalWalletTransaction) BeforeUpdate(*gorm.DB) error { return errPortalHistoryImmutable }
func (*PortalWalletTransaction) BeforeDelete(*gorm.DB) error { return errPortalHistoryImmutable }
func (*PortalInvitationUse) BeforeUpdate(*gorm.DB) error     { return errPortalHistoryImmutable }
func (*PortalInvitationUse) BeforeDelete(*gorm.DB) error     { return errPortalHistoryImmutable }
func (*PortalAction) BeforeUpdate(*gorm.DB) error            { return errPortalHistoryImmutable }
func (*PortalAction) BeforeDelete(*gorm.DB) error            { return errPortalHistoryImmutable }
func (*PortalUsageReset) BeforeUpdate(*gorm.DB) error        { return errPortalHistoryImmutable }
func (*PortalUsageReset) BeforeDelete(*gorm.DB) error        { return errPortalHistoryImmutable }

// Shared registration keeps backups, cross-DB conversion and live schemas aligned.
func PortalModels() []any {
	return []any{&PortalAccount{}, &PortalIdentity{}, &PortalPlan{}, &PortalSubscription{}, &PortalPurchase{}, &PortalWalletTransaction{}, &PortalAction{}, &PortalInvitation{}, &PortalInvitationUse{}, &PortalIPBlock{}, &PortalSecurityAttempt{}, &PortalSettings{}, &PortalAccess{}, &PortalLegacyUser{}, &PortalNextPlan{}, &PortalRuntimeChange{}, &PortalUsageReset{}}
}
