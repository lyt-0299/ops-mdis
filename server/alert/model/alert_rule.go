package model

import "time"

// AlertRule 告警规则模型
type AlertRule struct {
	ID               string            `bson:"_id,omitempty"    json:"id"`
	Kind             string            `bson:"kind,omitempty"   json:"-"`
	Name             string            `bson:"name"             json:"name"`
	DataSourceID     string            `bson:"dataSourceId"     json:"dataSourceId"`
	Category         string            `bson:"category"         json:"category"`         // server, k8s_pod, custom
	MetricType       string            `bson:"metricType"       json:"metricType"`       // cpu, memory, disk, custom
	PromQL           string            `bson:"promql"           json:"promql"`           // 生成或自定义的 PromQL
	Operator         string            `bson:"operator"         json:"operator"`         // >, <, >=, <=, ==
	Threshold        float64           `bson:"threshold"        json:"threshold"`
	Duration         int               `bson:"duration"         json:"duration"`         // 告警持续时间（秒），超过阈值需持续多久才触发告警
	RecoveryDuration int               `bson:"recoveryDuration" json:"recoveryDuration"` // 恢复持续时间（秒），降至阈值下需持续多久才恢复
	Interval         int               `bson:"interval"         json:"interval"`         // 检查间隔（秒）
	AlertNote        string            `bson:"alertNote"        json:"alertNote"`        // 告警备注（随告警消息发送）
	Remark           string            `bson:"remark"           json:"remark"`           // 任务备注（仅内部展示，不随告警发送）
	WebhookID        string            `bson:"webhookId"        json:"webhookId"`        // 可选的 Webhook 配置
	Status           string            `bson:"status"           json:"status"`           // active/paused
	PauseMode        string            `bson:"pauseMode"        json:"pauseMode"`        // manual/temporary
	PausedUntil      *time.Time        `bson:"pausedUntil"      json:"pausedUntil"`      // 临时暂停自动恢复时间
	PausedAt         *time.Time        `bson:"pausedAt"         json:"pausedAt"`         // 暂停开始时间
	PauseReason      string            `bson:"pauseReason"      json:"pauseReason"`      // 暂停原因
	RepeatAlert      bool              `bson:"repeatAlert"      json:"repeatAlert"`      // 是否持续告警
	RemindInterval   int               `bson:"remindInterval"   json:"remindInterval"`   // 提醒间隔（秒），0 表示不重复提醒
	AlertState       string            `bson:"alertState"       json:"alertState"`       // OK/Pending/Firing/Recovered
	LastValue        float64           `bson:"lastValue"        json:"lastValue"`        // 上次检查的值
	FiringAt         *time.Time        `bson:"firingAt"         json:"firingAt"`         // 开始触发时间（告警计时器）
	RecoveredAt      *time.Time        `bson:"recoveredAt"      json:"recoveredAt"`      // 开始恢复时间（恢复计时器）
	Filters          map[string]string `bson:"filters"          json:"filters"`          // 过滤条件
	CreatedAt        time.Time         `bson:"createdAt"        json:"createdAt"`
	UpdatedAt        time.Time         `bson:"updatedAt"        json:"updatedAt"`

	// 运行时统计字段（不持久化）
	InstanceTotal       int64 `bson:"-" json:"instanceTotal"`
	InstanceFiring      int64 `bson:"-" json:"instanceFiring"`
	InstanceMutedFiring int64 `bson:"-" json:"instanceMutedFiring"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	DataSource DataSource `bson:"-" json:"datasource,omitempty"`
	Webhook    *Webhook   `bson:"-" json:"webhook,omitempty"`
}
