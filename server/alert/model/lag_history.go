package model

import "time"

// LagHistory Lag 历史记录模型
type LagHistory struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	Kind      string    `bson:"kind,omitempty" json:"-"`
	TaskID    string    `bson:"taskId"        json:"taskId"`
	LagValue  int64     `bson:"lagValue"      json:"lagValue"`
	Timestamp time.Time `bson:"timestamp"     json:"timestamp"`
}
