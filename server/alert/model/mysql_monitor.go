package model

import "time"

const (
	MySQLMonitorTypeHealth            = "health"             // 兼容旧值，运行时会按架构映射
	MySQLMonitorTypeMGRStatus         = "mgr_status"         // 兼容旧值，运行时会按架构映射
	MySQLMonitorTypeConnectivity      = "connectivity"
	MySQLMonitorTypeReplicationLag    = "replication_lag"
	MySQLMonitorTypeReplicationStatus = "replication_status"
	MySQLMonitorTypeMGRNodeStatus     = "mgr_node_status"
	MySQLMonitorTypeMGRSyncStatus     = "mgr_sync_status"
)

// MySQLMonitorRule MySQL 监控规则模型。
type MySQLMonitorRule struct {
	ID                      string     `bson:"_id,omitempty"            json:"id"`
	Kind                    string     `bson:"kind,omitempty"           json:"-"`
	Name                    string     `bson:"name"                     json:"name"`
	MySQLInstanceID         string     `bson:"mysqlInstanceId"          json:"mysqlInstanceId"`
	MonitorType             string     `bson:"monitorType"              json:"monitorType"`
	ReplicationLagThreshold int        `bson:"replicationLagThreshold"  json:"replicationLagThreshold"` // 主从延迟阈值，单位秒
	MGRQueueThreshold       int        `bson:"mgrQueueThreshold"        json:"mgrQueueThreshold"`       // MGR 队列阈值
	Duration                int        `bson:"duration"                 json:"duration"`
	RecoveryDuration        int        `bson:"recoveryDuration"         json:"recoveryDuration"`
	Interval                int        `bson:"interval"                 json:"interval"`
	AlertNote               string     `bson:"alertNote"                json:"alertNote"`
	Remark                  string     `bson:"remark"                   json:"remark"`
	WebhookID               string     `bson:"webhookId"                json:"webhookId"`
	Status                  string     `bson:"status"                   json:"status"`
	PauseMode               string     `bson:"pauseMode"                json:"pauseMode"`
	PausedUntil             *time.Time `bson:"pausedUntil"              json:"pausedUntil"`
	PausedAt                *time.Time `bson:"pausedAt"                 json:"pausedAt"`
	PauseReason             string     `bson:"pauseReason"              json:"pauseReason"`
	AlertState              string     `bson:"alertState"               json:"alertState"`
	RepeatAlert             bool       `bson:"repeatAlert"              json:"repeatAlert"`
	RemindInterval          int        `bson:"remindInterval"           json:"remindInterval"`
	FiringAt                *time.Time `bson:"firingAt"                 json:"firingAt"`
	RecoveredAt             *time.Time `bson:"recoveredAt"              json:"recoveredAt"`
	LastValue               float64    `bson:"lastValue"                json:"lastValue"`
	LastDetail              string     `bson:"lastDetail"               json:"lastDetail"`
	CreatedAt               time.Time  `bson:"createdAt"                json:"createdAt"`
	UpdatedAt               time.Time  `bson:"updatedAt"                json:"updatedAt"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	MySQLInstance MySQLInstance `bson:"-" json:"mysqlInstance,omitempty"`
	Webhook       *Webhook      `bson:"-" json:"webhook,omitempty"`
}
