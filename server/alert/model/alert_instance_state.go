package model

import "time"

// AlertInstanceState stores the latest object-level state for a monitor rule.
// For Prometheus this maps one vector series to one instance row.
type AlertInstanceState struct {
	ID               string     `bson:"_id,omitempty"      json:"id"`
	Kind             string     `bson:"kind,omitempty"     json:"-"`
	SourceType       string     `bson:"sourceType"         json:"sourceType"`
	SourceID         string     `bson:"sourceId"           json:"sourceId"`
	ScopeType        string     `bson:"scopeType"          json:"scopeType"`
	ScopeKey         string     `bson:"scopeKey"           json:"scopeKey"`
	DisplayName      string     `bson:"displayName"        json:"displayName"`
	AlertState       string     `bson:"alertState"         json:"alertState"`
	LastValue        float64    `bson:"lastValue"          json:"lastValue"`
	LastDetail       string     `bson:"lastDetail"         json:"lastDetail"`
	FiringAt         *time.Time `bson:"firingAt"           json:"firingAt"`
	RecoveredAt      *time.Time `bson:"recoveredAt"        json:"recoveredAt"`
	LastSuppressedAt *time.Time `bson:"lastSuppressedAt"   json:"lastSuppressedAt"`
	CreatedAt        time.Time  `bson:"createdAt"          json:"createdAt"`
	UpdatedAt        time.Time  `bson:"updatedAt"          json:"updatedAt"`

	// 运行时计算字段（不持久化）
	Muted      bool       `bson:"-" json:"muted"`
	MutedUntil *time.Time `bson:"-" json:"mutedUntil"`
	MuteReason string     `bson:"-" json:"muteReason"`
	SilenceID  string     `bson:"-" json:"silenceId"`
}
