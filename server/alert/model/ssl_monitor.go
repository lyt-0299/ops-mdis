package model

import "time"

// SslMonitorRule SSL 证书监控规则模型
type SslMonitorRule struct {
	ID              string     `bson:"_id,omitempty"  json:"id"`
	Kind            string     `bson:"kind,omitempty" json:"-"`
	Name            string     `bson:"name"           json:"name"`
	Domain          string     `bson:"domain"         json:"domain"`          // 目标域名
	Port            int        `bson:"port"           json:"port"`            // 端口（默认 443）
	Timeout         int        `bson:"timeout"        json:"timeout"`         // 拨测超时时间（秒）
	WarnDays        int        `bson:"warnDays"       json:"warnDays"`        // 提前告警天数
	Interval        int        `bson:"interval"       json:"interval"`        // 检查间隔（秒，默认每天）
	AlertNote       string     `bson:"alertNote"      json:"alertNote"`       // 告警备注（随告警消息发送）
	Remark          string     `bson:"remark"         json:"remark"`          // 任务备注（仅内部展示，不随告警发送）
	WebhookID       string     `bson:"webhookId"      json:"webhookId"`       // 可选的 Webhook 配置
	Status          string     `bson:"status"         json:"status"`          // active/paused
	PauseMode       string     `bson:"pauseMode"      json:"pauseMode"`       // manual/temporary
	PausedUntil     *time.Time `bson:"pausedUntil"    json:"pausedUntil"`     // 临时暂停自动恢复时间
	PausedAt        *time.Time `bson:"pausedAt"       json:"pausedAt"`        // 暂停开始时间
	PauseReason     string     `bson:"pauseReason"    json:"pauseReason"`     // 暂停原因
	AlertState      string     `bson:"alertState"     json:"alertState"`      // OK/Pending/Firing/Recovered
	IgnoreSslErrors bool       `bson:"ignoreSslErrors" json:"ignoreSslErrors"` // 是否忽略证书验证（对于自签证书）
	RepeatAlert     bool       `bson:"repeatAlert"    json:"repeatAlert"`     // 是否持续告警
	RemindInterval  int        `bson:"remindInterval" json:"remindInterval"`  // 提醒间隔（秒），0 表示不重复提醒
	FiringAt        *time.Time `bson:"firingAt"       json:"firingAt"`        // 开始触发时间（告警计时器）
	RecoveredAt     *time.Time `bson:"recoveredAt"    json:"recoveredAt"`     // 开始恢复时间（恢复计时器）
	CertExpiry      *time.Time `bson:"certExpiry"     json:"certExpiry"`      // 实测证书到期时间
	DaysLeft        *int       `bson:"daysLeft"       json:"daysLeft"`        // 证书剩余有效天数（缓存值）
	LastCheckAt     *time.Time `bson:"lastCheckAt"    json:"lastCheckAt"`     // 上次检查时间
	CreatedAt       time.Time  `bson:"createdAt"      json:"createdAt"`
	UpdatedAt       time.Time  `bson:"updatedAt"      json:"updatedAt"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	Webhook *Webhook `bson:"-" json:"webhook,omitempty"`
}
