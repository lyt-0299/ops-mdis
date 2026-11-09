package model

import (
	"strings"
	"time"
)

const (
	KafkaRebalanceStateNormal      = "normal"
	KafkaRebalanceStateRebalancing = "rebalancing"
	KafkaRebalanceStateEmpty       = "empty"
	KafkaRebalanceStateOrphaned    = "orphaned"
	KafkaRebalanceStateError       = "error"
)

// KafkaRebalanceMonitorRule Kafka 消费组重平衡/异常状态监控规则。
type KafkaRebalanceMonitorRule struct {
	ID                           string     `bson:"_id,omitempty"                   json:"id"`
	Kind                         string     `bson:"kind,omitempty"                  json:"-"`
	Name                         string     `bson:"name"                            json:"name"`
	KafkaInstanceID              string     `bson:"kafkaInstanceId"                 json:"kafkaInstanceId"`
	ConsumerGroup                string     `bson:"consumerGroup"                   json:"consumerGroup"`
	Topic                        string     `bson:"topic"                           json:"topic"`
	Namespace                    string     `bson:"namespace"                       json:"namespace"`
	Resources                    []string   `bson:"resources"                       json:"resources"`
	Interval                     int        `bson:"interval"                        json:"interval"`
	RebalanceInitialWaitSeconds  int        `bson:"rebalanceInitialWaitSeconds"     json:"rebalanceInitialWaitSeconds"`
	RecheckWaitSeconds           int        `bson:"recheckWaitSeconds"              json:"recheckWaitSeconds"`
	ErrorRecheckWaitSeconds      int        `bson:"errorRecheckWaitSeconds"         json:"errorRecheckWaitSeconds"`
	KafkaVersion                 string     `bson:"kafkaVersion"                    json:"kafkaVersion"`
	CommandTimeoutSeconds        int        `bson:"commandTimeoutSeconds"           json:"commandTimeoutSeconds"`
	AutoRestart                  bool       `bson:"autoRestart"                     json:"autoRestart"`
	NotifyOnDetectionError       bool       `bson:"notifyOnDetectionError"          json:"notifyOnDetectionError"`
	NotifyOnRebalanceAutoFixed   bool       `bson:"notifyOnRebalanceAutoFixed"      json:"notifyOnRebalanceAutoFixed"`
	NotifyOnRestartTriggered     bool       `bson:"notifyOnRestartTriggered"        json:"notifyOnRestartTriggered"`
	NotifyOnRestartFailed        bool       `bson:"notifyOnRestartFailed"           json:"notifyOnRestartFailed"`
	NotifyOnRestartSuccess       bool       `bson:"notifyOnRestartSuccess"          json:"notifyOnRestartSuccess"`
	NotifyOnRestartStillAbnormal bool       `bson:"notifyOnRestartStillAbnormal"    json:"notifyOnRestartStillAbnormal"`
	Status                       string     `bson:"status"                          json:"status"`
	PauseMode                    string     `bson:"pauseMode"                       json:"pauseMode"`
	PausedUntil                  *time.Time `bson:"pausedUntil"                     json:"pausedUntil"`
	PausedAt                     *time.Time `bson:"pausedAt"                        json:"pausedAt"`
	PauseReason                  string     `bson:"pauseReason"                     json:"pauseReason"`
	AlertState                   string     `bson:"alertState"                      json:"alertState"`
	LastState                    string     `bson:"lastState"                       json:"lastState"`
	LastMessage                  string     `bson:"lastMessage"                     json:"lastMessage"`
	LastCheckedAt                *time.Time `bson:"lastCheckedAt"                   json:"lastCheckedAt"`
	LastRestartedAt              *time.Time `bson:"lastRestartedAt"                 json:"lastRestartedAt"`
	RestartCount                 int        `bson:"restartCount"                    json:"restartCount"`
	RepeatAlert                  bool       `bson:"repeatAlert"                     json:"repeatAlert"`
	RemindInterval               int        `bson:"remindInterval"                  json:"remindInterval"`
	AlertNote                    string     `bson:"alertNote"                       json:"alertNote"`
	Remark                       string     `bson:"remark"                          json:"remark"`
	WebhookID                    string     `bson:"webhookId"                       json:"webhookId"`
	CreatedAt                    time.Time  `bson:"createdAt"                       json:"createdAt"`
	UpdatedAt                    time.Time  `bson:"updatedAt"                       json:"updatedAt"`

	// 运行时关联字段（不持久化），由调度器查询后注入
	KafkaInstance KafkaInstance `bson:"-" json:"kafkaInstance,omitempty"`
	Webhook       *Webhook      `bson:"-" json:"webhook,omitempty"`
}

func (r *KafkaRebalanceMonitorRule) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.ConsumerGroup = strings.TrimSpace(r.ConsumerGroup)
	r.Topic = strings.TrimSpace(r.Topic)
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.KafkaVersion = strings.TrimSpace(r.KafkaVersion)
	r.AlertNote = strings.TrimSpace(r.AlertNote)
	r.Remark = strings.TrimSpace(r.Remark)

	resources := make([]string, 0, len(r.Resources))
	seen := make(map[string]bool)
	for _, resource := range r.Resources {
		resource = strings.TrimSpace(resource)
		if resource == "" || seen[resource] {
			continue
		}
		seen[resource] = true
		resources = append(resources, resource)
	}
	r.Resources = resources

	if r.KafkaVersion == "" {
		r.KafkaVersion = "2.0.0"
	}
	if r.Status == "" {
		r.Status = TaskStatusActive
	}
	if r.AlertState == "" {
		r.AlertState = AlertStateOK
	}
	if r.LastState == "" {
		r.LastState = KafkaRebalanceStateNormal
	}
}
