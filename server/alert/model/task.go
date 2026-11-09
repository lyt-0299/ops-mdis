package model

import "time"

// Task 监控任务模型
type Task struct {
	ID              string     `bson:"_id,omitempty" json:"id"`
	Kind            string     `bson:"kind,omitempty" json:"-"`
	Name            string     `bson:"name"          json:"name"`
	KafkaInstanceID string     `bson:"kafkaInstanceId" json:"kafkaInstanceId"`
	Topic           string     `bson:"topic"         json:"topic"`
	ConsumerGroup   string     `bson:"consumerGroup" json:"consumerGroup"`
	Threshold       int64      `bson:"threshold"     json:"threshold"`      // Lag 阈值
	Interval        int        `bson:"interval"      json:"interval"`       // 采集间隔（秒）
	AlertNote       string     `bson:"alertNote"     json:"alertNote"`      // 告警备注（随告警消息发送）
	Remark          string     `bson:"remark"        json:"remark"`         // 任务备注（仅内部展示，不随告警发送）
	WebhookID       string     `bson:"webhookId"     json:"webhookId"`      // 可选的 Webhook 配置
	Status          string     `bson:"status"        json:"status"`         // active/paused
	PauseMode       string     `bson:"pauseMode"     json:"pauseMode"`      // manual/temporary
	PausedUntil     *time.Time `bson:"pausedUntil"   json:"pausedUntil"`    // 临时暂停自动恢复时间
	PausedAt        *time.Time `bson:"pausedAt"      json:"pausedAt"`       // 暂停开始时间
	PauseReason     string     `bson:"pauseReason"   json:"pauseReason"`    // 暂停原因
	AlertState      string     `bson:"alertState"    json:"alertState"`     // OK/Firing/Recovered
	RepeatAlert     bool       `bson:"repeatAlert"   json:"repeatAlert"`    // 是否持续告警
	RemindInterval  int        `bson:"remindInterval" json:"remindInterval"` // 提醒间隔（秒），0 表示不重复提醒
	LastLag         int64      `bson:"lastLag"       json:"lastLag"`        // 上次 Lag 值
	CreatedAt       time.Time  `bson:"createdAt"     json:"createdAt"`
	UpdatedAt       time.Time  `bson:"updatedAt"     json:"updatedAt"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	KafkaInstance KafkaInstance `bson:"-" json:"kafkaInstance,omitempty"`
	Webhook       *Webhook      `bson:"-" json:"webhook,omitempty"`
}
