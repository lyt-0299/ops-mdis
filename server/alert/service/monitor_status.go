package service

import (
	"context"
	"log"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// sourceKindMap 将 sourceType 映射到 alert_config 集合中对应的 kind 值。
var sourceKindMap = map[string]string{
	model.SourceTypeKafka:                 "task",
	model.SourceTypePrometheus:            "alertRule",
	model.SourceTypePortMonitor:           "portRule",
	model.SourceTypeSslMonitor:            "sslRule",
	model.SourceTypeMongoMonitor:          "mongoRule",
	model.SourceTypeMySQLMonitor:          "mysqlRule",
	model.SourceTypeKafkaRebalanceMonitor: "kafkaRebalanceRule",
}

// isMonitorActive 检查指定监控对象当前状态是否为 active。
// 对应上游按 sourceType 选不同 GORM model 查询 status 字段的逻辑。
func isMonitorActive(sourceType string, id string) bool {
	kind, ok := sourceKindMap[sourceType]
	if !ok {
		return false
	}

	ctx := context.TODO()
	result := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kind})
	if result.Err() != nil {
		log.Printf("[MonitorStatus] 查询 %s %s 状态失败: %v", sourceType, id, result.Err())
		return false
	}

	var doc struct {
		Status string `bson:"status"`
	}
	if err := result.Decode(&doc); err != nil {
		log.Printf("[MonitorStatus] 解码 %s %s 状态失败: %v", sourceType, id, err)
		return false
	}

	return doc.Status == model.TaskStatusActive
}

func logSkipPaused(sourceType string, id string, name string) {
	log.Printf("[MonitorStatus] %s %s (%s) 当前不是 active，跳过检查、状态更新和通知", sourceType, id, name)
}
