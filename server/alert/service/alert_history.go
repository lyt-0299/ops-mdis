package service

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// SaveAlertHistory 保存告警历史记录。
// 位于 service 包以避免 model ↔ database 的循环依赖。
//
// 注意：此函数只负责 INSERT，按 (source_type, source_id) 维度保留最新
// AlertHistoryKeepPerSource 条的裁剪逻辑由独立的 AlertHistoryCleanup 调度器
// 周期性执行（详见 alert_history_cleanup.go）。
// 高频写入场景下，这避免了原先的 "INSERT + SELECT 100 ID + DELETE NOT IN"
// 三倍写放大。
func SaveAlertHistory(sourceType string, sourceID string, sourceName, alertState, content, webhookName, webhookType string) {
	record := model.AlertHistory{
		ID:          bson.NewObjectID().Hex(),
		SourceType:  sourceType,
		SourceID:    sourceID,
		SourceName:  sourceName,
		AlertState:  alertState,
		WebhookName: webhookName,
		WebhookType: webhookType,
		Content:     content,
		TriggeredAt: time.Now().UTC(),
	}

	ctx := context.TODO()
	if _, err := database.History().InsertOne(ctx, record); err != nil {
		log.Printf("[AlertHistory] 写入历史记录失败 [%s/%s]: %v", sourceType, sourceID, err)
	}
}

// RestoreLastAlertSentCache 从 alert_history 集合查询指定 sourceType 下，
// 各 sourceID 最后一次 Firing 告警的发送时间，用于服务重启后恢复 lastAlertSentCache，
// 避免间隔尚未到期却因重启导致缓存丢失而重复触发提醒间隔告警。
//
// 返回 map[sourceID]triggeredAt。
func RestoreLastAlertSentCache(sourceType string) map[string]time.Time {
	cache := make(map[string]time.Time)
	ctx := context.TODO()

	// 对每个 sourceID 取 triggeredAt 最大值（即最新一次 Firing 的时间）。
	// MongoDB aggregation: group by sourceId, max triggeredAt where alertState=Firing.
	pipeline := []bson.M{
		{"$match": bson.M{
			"sourceType": sourceType,
			"alertState": model.AlertStateFiring,
		}},
		{"$group": bson.M{
			"_id":         "$sourceId",
			"triggeredAt": bson.M{"$max": "$triggeredAt"},
		}},
	}

	cur, err := database.History().Aggregate(ctx, pipeline, options.Aggregate())
	if err != nil {
		log.Printf("[AlertHistory] 恢复 lastAlertSentCache 失败 [%s]: %v", sourceType, err)
		return cache
	}
	defer cur.Close(ctx)

	type aggResult struct {
		SourceID    string    `bson:"_id"`
		TriggeredAt time.Time `bson:"triggeredAt"`
	}
	var results []aggResult
	if err := cur.All(ctx, &results); err != nil {
		log.Printf("[AlertHistory] 解析 lastAlertSentCache 失败 [%s]: %v", sourceType, err)
		return cache
	}

	for _, r := range results {
		cache[r.SourceID] = r.TriggeredAt
	}
	log.Printf("[AlertHistory] 已从历史记录恢复 %d 条提醒间隔缓存 [%s]", len(cache), sourceType)
	return cache
}
