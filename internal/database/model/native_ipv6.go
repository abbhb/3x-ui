package model

// NativeIPv6Lease is address ownership, not a forwarding policy. The actual
// sendThrough and user rules remain in the native Xray template. Client IDs
// survive credential rotation; retired leases are deliberately not recycled.
type NativeIPv6Lease struct {
	ClientID int    `json:"clientId" gorm:"primaryKey;autoIncrement:false;column:client_id"`
	Address  string `json:"address" gorm:"uniqueIndex;not null"`
}
