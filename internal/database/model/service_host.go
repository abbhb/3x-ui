package model

// ServiceHost is a physical machine. Host remains a subscription endpoint rule.
type ServiceHost struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name      string `json:"name" gorm:"uniqueIndex;not null"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
}

// NodeId=0 represents this panel's local core. A service belongs to one machine.
type ServiceHostMember struct {
	NodeId int `json:"nodeId" gorm:"primaryKey;autoIncrement:false"`
	HostId int `json:"hostId" gorm:"index;not null"`
}

// ServiceLine preserves an imported exit annotation for read-only migration
// reference. It never configures forwarding: native Xray outbounds/routing and
// a configured address allocator own effective egress. Keep existing rows when
// upgrading; they are not an alternative editable policy model.
type ServiceLine struct {
	InboundId  int    `json:"inboundId" gorm:"primaryKey;autoIncrement:false"`
	EgressMode string `json:"egressMode"`
	Note       string `json:"note"`
}
