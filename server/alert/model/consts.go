package model

// 告警状态常量
const (
	AlertStateOK         = "OK"
	AlertStateFiring     = "Firing"
	AlertStatePending    = "Pending"
	AlertStateRecovered  = "Recovered"
	AlertStateMuted      = "Muted"
	AlertStateUnmuted    = "Unmuted"
	AlertStateSuppressed = "Suppressed"
)

// 任务状态常量
const (
	TaskStatusActive = "active"
	TaskStatusPaused = "paused"
)

// 暂停模式常量
const (
	PauseModeManual    = "manual"
	PauseModeTemporary = "temporary"
)

// 告警静默常量
const (
	SilenceModeTemporary = "temporary"
	SilenceModePermanent = "permanent"

	SilenceStatusActive    = "active"
	SilenceStatusExpired   = "expired"
	SilenceStatusCancelled = "cancelled"

	SilenceScopeInstance = "instance"
)

// 告警历史来源类型常量（可扩展，新模块仅需在此新增常量）
const (
	SourceTypeKafka                 = "kafka"
	SourceTypePrometheus            = "prometheus"
	SourceTypePortMonitor           = "port_monitor"
	SourceTypeSslMonitor            = "ssl_monitor"
	SourceTypeMongoMonitor          = "mongo_monitor"
	SourceTypeMySQLMonitor          = "mysql_monitor"
	SourceTypeKafkaRebalanceMonitor = "kafka_rebalance_monitor"
)

// MaskValue 是敏感字段在前端展示和回传时的脱敏占位符。
// 当 API 收到此值时，表示用户未修改该字段，应保留数据库中原有值。
const MaskValue = "********"
