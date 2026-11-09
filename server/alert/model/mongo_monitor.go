package model

import "time"

// MongoMonitorRule MongoDB 监控规则模型
type MongoMonitorRule struct {
	ID               string     `bson:"_id,omitempty"    json:"id"`
	Kind             string     `bson:"kind,omitempty"   json:"-"`
	Name             string     `bson:"name"             json:"name"`
	MongoInstanceID  string     `bson:"mongoInstanceId"  json:"mongoInstanceId"`
	MonitorType      string     `bson:"monitorType"      json:"monitorType"`      // replica_status / oplog_lag / oplog_window
	Threshold        float64    `bson:"threshold"        json:"threshold"`        // oplog_lag: 秒, oplog_window: 小时
	Operator         string     `bson:"operator"         json:"operator"`         // > < >= <=
	Duration         int        `bson:"duration"         json:"duration"`         // 告警持续时间（秒）
	RecoveryDuration int        `bson:"recoveryDuration" json:"recoveryDuration"` // 恢复持续时间（秒）
	Interval         int        `bson:"interval"         json:"interval"`         // 检查间隔（秒）
	AlertNote        string     `bson:"alertNote"        json:"alertNote"`        // 告警备注（随告警消息发送）
	Remark           string     `bson:"remark"           json:"remark"`           // 任务备注（仅内部展示，不随告警发送）
	WebhookID        string     `bson:"webhookId"        json:"webhookId"`        // 可选的 Webhook 配置
	Status           string     `bson:"status"           json:"status"`           // active/paused
	PauseMode        string     `bson:"pauseMode"        json:"pauseMode"`        // manual/temporary
	PausedUntil      *time.Time `bson:"pausedUntil"      json:"pausedUntil"`      // 临时暂停自动恢复时间
	PausedAt         *time.Time `bson:"pausedAt"         json:"pausedAt"`         // 暂停开始时间
	PauseReason      string     `bson:"pauseReason"      json:"pauseReason"`      // 暂停原因
	AlertState       string     `bson:"alertState"       json:"alertState"`       // OK/Pending/Firing/Recovered
	RepeatAlert      bool       `bson:"repeatAlert"      json:"repeatAlert"`      // 是否持续告警
	RemindInterval   int        `bson:"remindInterval"   json:"remindInterval"`   // 提醒间隔（秒），0 表示不重复提醒
	FiringAt         *time.Time `bson:"firingAt"         json:"firingAt"`         // 开始触发时间（告警计时器）
	RecoveredAt      *time.Time `bson:"recoveredAt"      json:"recoveredAt"`      // 开始恢复时间（恢复计时器）
	LastValue        float64    `bson:"lastValue"        json:"lastValue"`        // 上次检测值
	LastDetail       string     `bson:"lastDetail"       json:"lastDetail"`       // 上次检测详情(JSON)
	LastCheckAt      *time.Time `bson:"lastCheckAt"      json:"lastCheckAt"`      // 上次检查时间(每次检查都更新,供"最近检查"准确显示)
	CreatedAt        time.Time  `bson:"createdAt"        json:"createdAt"`
	UpdatedAt        time.Time  `bson:"updatedAt"        json:"updatedAt"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	MongoInstance MongoInstance `bson:"-" json:"mongoInstance,omitempty"`
	Webhook       *Webhook      `bson:"-" json:"webhook,omitempty"`
}
