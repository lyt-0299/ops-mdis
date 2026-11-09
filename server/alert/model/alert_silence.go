package model

import "time"

// AlertSilence records a notification suppression rule for a single alert object.
// The first implementation uses it for Prometheus rule instances, but the source
// fields keep the shape reusable for MongoDB/MySQL instance-level silencing later.
type AlertSilence struct {
	ID          string     `bson:"_id,omitempty" json:"id"`
	Kind        string     `bson:"kind,omitempty" json:"-"`
	SourceType  string     `bson:"sourceType"    json:"sourceType"`
	SourceID    string     `bson:"sourceId"      json:"sourceId"`
	ScopeType   string     `bson:"scopeType"     json:"scopeType"`
	ScopeKey    string     `bson:"scopeKey"      json:"scopeKey"`
	DisplayName string     `bson:"displayName"   json:"displayName"`
	Mode        string     `bson:"mode"          json:"mode"`
	MutedUntil  *time.Time `bson:"mutedUntil"    json:"mutedUntil"`
	Reason      string     `bson:"reason"        json:"reason"`
	Status      string     `bson:"status"        json:"status"`
	CreatedAt   time.Time  `bson:"createdAt"     json:"createdAt"`
	UpdatedAt   time.Time  `bson:"updatedAt"     json:"updatedAt"`
}
