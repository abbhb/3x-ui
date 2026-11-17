package model

type ExternalSelection struct {
	SourceID int    `json:"source_id"`
	Mode     string `json:"mode"`
	NodeIDs  []int  `json:"node_ids"`
}

type SubscriptionSource struct {
	ID                   int            `json:"id" gorm:"primaryKey"`
	OwnerAccountID       *int           `json:"owner_account_id" gorm:"index"`
	Owner                *PortalAccount `json:"-" gorm:"foreignKey:OwnerAccountID;constraint:OnDelete:RESTRICT"`
	ScopeKey             int            `json:"-" gorm:"uniqueIndex:subscription_source_name,priority:1;uniqueIndex:subscription_source_group,priority:1"`
	Name                 string         `json:"name" gorm:"uniqueIndex:subscription_source_name,priority:2"`
	GroupName            string         `json:"group_name" gorm:"uniqueIndex:subscription_source_group,priority:2"`
	URLCiphertext        string         `json:"-"`
	CredentialConfigured bool           `json:"credential_configured" gorm:"-"`
	OwnerUsername        string         `json:"owner_username" gorm:"-"`
	Enabled              bool           `json:"enabled"`
	RefreshMinutes       int            `json:"refresh_interval_minutes"`
	URLRevision          int64          `json:"-"`
	NextRefreshAt        int64          `json:"next_refresh_at" gorm:"index"`
	LeaseUntil           int64          `json:"-"`
	LeaseToken           string         `json:"-"`
	Status               string         `json:"status"`
	LastErrorCode        string         `json:"last_error_code"`
	LastAttemptAt        int64          `json:"last_attempt_at"`
	LastSuccessAt        int64          `json:"last_success_at"`
	NodeCount            int            `json:"node_count"`
	SkippedNodeCount     int            `json:"skipped_node_count"`
	CreatedAt            int64          `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt            int64          `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

type SubscriptionNode struct {
	ID                int                 `json:"id" gorm:"primaryKey"`
	SourceID          int                 `json:"source_id" gorm:"index;uniqueIndex:subscription_node_key,priority:1"`
	Source            *SubscriptionSource `json:"-" gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE"`
	ExternalKey       string              `json:"-" gorm:"uniqueIndex:subscription_node_key,priority:2"`
	Name              string              `json:"name"`
	ProxyType         string              `json:"proxy_type"`
	PayloadCiphertext string              `json:"-"`
	PlainBytes        int                 `json:"-"`
	Position          int                 `json:"position"`
	IsActive          bool                `json:"is_active"`
}

// SubscriptionHost is retired commerce route metadata, retained only for migration
// readback and existing plan signatures. Subscription rendering must not use it.
type SubscriptionHost struct {
	MihomoOnly      bool              `json:"mihomo_only"`
	HostID          int               `json:"host_id" gorm:"primaryKey;autoIncrement:false"`
	Host            *Host             `json:"-" gorm:"foreignKey:HostID;constraint:OnDelete:CASCADE"`
	Revision        int64             `json:"revision"`
	CascadeMode     string            `json:"cascade_mode"`
	EntryNodeID     *int              `json:"entry_node_id" gorm:"index"`
	EntryNode       *SubscriptionNode `json:"-" gorm:"foreignKey:EntryNodeID;constraint:OnDelete:RESTRICT"`
	EntryCiphertext string            `json:"-"`
	EntryConfigured bool              `json:"entry_configured" gorm:"-"`
	ClientStream    string            `json:"client_stream"`
}

type SubscriptionPolicy struct {
	ID           int      `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Revision     int64    `json:"revision"`
	Enabled      bool     `json:"enabled"`
	PrimaryGroup string   `json:"primary_group"`
	SecureDNS    bool     `json:"secure_dns"`
	Document     string   `json:"document"`
	GroupOrder   []string `json:"group_order" gorm:"serializer:json"`
}

type SubscriptionBudgetLock struct {
	PlanCursor    int
	LastCleanupAt int64
	ID            int `gorm:"primaryKey;autoIncrement:false"`
}
