package model

import "time"

// AlertHistory 告警历史记录模型
type AlertHistory struct {
	ID          string    `bson:"_id,omitempty"  json:"id"`
	Kind        string    `bson:"kind,omitempty" json:"-"`
	SourceType  string    `bson:"sourceType"     json:"sourceType"`  // "kafka" | "prometheus" | 可扩展
	SourceID    string    `bson:"sourceId"       json:"sourceId"`
	SourceName  string    `bson:"sourceName"     json:"sourceName"`  // 名称快照，防止任务/规则改名后丢失关联
	AlertState  string    `bson:"alertState"     json:"alertState"`  // "Firing" | "Recovered"
	WebhookName string    `bson:"webhookName"    json:"webhookName"` // 通知渠道名称
	WebhookType string    `bson:"webhookType"    json:"webhookType"` // 通知渠道类型 (dingtalk, wecom, feishu, email)
	Content     string    `bson:"content"        json:"content"`     // 告警正文
	TriggeredAt time.Time `bson:"triggeredAt"    json:"triggeredAt"`
}
