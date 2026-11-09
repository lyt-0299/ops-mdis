package service

import (
	"context"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
)

// AlertHistoryKeepPerSource 每个 (sourceType, sourceId) 组合保留的最大记录数。
// 这是软上限——定时任务两次执行间隔内允许短暂超额，等待下一轮清理。
const AlertHistoryKeepPerSource = 100

// alertHistoryCleanupInterval 清理周期；高频告警时缩短可减少超额时长，
// 但也增加扫描开销。5 分钟是经验值：100 条软上限在合理告警率下不会爆表。
var alertHistoryCleanupInterval = 5 * time.Minute

// AlertHistoryCleanup 周期性清理 alert_history 集合，按 (sourceType, sourceId)
// 维度只保留最新 AlertHistoryKeepPerSource 条。
//
// 设计动机：原先 SaveAlertHistory 在 INSERT 之后立即跑一次"SELECT 100 IDs +
// DELETE NOT IN"，告警风暴时写放大 3 倍。把清理移到后台后，写路径只剩 InsertOne，
// 清理的聚合 + DeleteMany 由 HAVING count > N 过滤只处理超额组，进一步降本。
type AlertHistoryCleanup struct {
	stopChan  chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	interval  time.Duration
}

var (
	globalAlertHistoryCleanup     *AlertHistoryCleanup
	globalAlertHistoryCleanupOnce sync.Once
)

// GetAlertHistoryCleanup 返回单例。
func GetAlertHistoryCleanup() *AlertHistoryCleanup {
	globalAlertHistoryCleanupOnce.Do(func() {
		globalAlertHistoryCleanup = &AlertHistoryCleanup{
			stopChan: make(chan struct{}),
			interval: alertHistoryCleanupInterval,
		}
	})
	return globalAlertHistoryCleanup
}

// StartAlertHistoryCleanup 启动后台清理调度器。
func StartAlertHistoryCleanup() {
	s := GetAlertHistoryCleanup()
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.run()
	})
}

// StopAlertHistoryCleanup 优雅停止（多次调用安全）。
func StopAlertHistoryCleanup() {
	if globalAlertHistoryCleanup == nil {
		return
	}
	globalAlertHistoryCleanup.stopOnce.Do(func() { close(globalAlertHistoryCleanup.stopChan) })
	globalAlertHistoryCleanup.wg.Wait()
}

func (s *AlertHistoryCleanup) run() {
	defer s.wg.Done()
	log.Println("[AlertHistoryCleanup] 启动告警历史清理调度器")

	// 启动 30s 后跑首轮（避免与监控启动冷启动期争 DB），之后按 interval 周期
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()

	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		select {
		case <-s.stopChan:
			return
		case <-initial.C:
			s.cleanupAll()
			ticker = time.NewTicker(s.interval)
		case <-tickerC(ticker):
			s.cleanupAll()
		}
	}
}

// tickerC 返回 ticker 的 channel；ticker 为 nil 时返回 nil channel
// （select 在 nil channel 上永远阻塞，等价于"还没启动"）。
func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// cleanupAll 扫描所有超额的 (sourceType, sourceId) 组合并裁剪。
// 上游 GORM 版用 HAVING COUNT(*) > N 的 SQL 聚合；mongo 版用 $group + $match。
func (s *AlertHistoryCleanup) cleanupAll() {
	ctx := context.TODO()

	// 聚合：按 (sourceType, sourceId) 分组，筛出 count > AlertHistoryKeepPerSource 的组
	pipeline := []bson.M{
		{"$group": bson.M{
			"_id": bson.M{
				"sourceType": "$sourceType",
				"sourceId":   "$sourceId",
			},
			"count": bson.M{"$sum": 1},
		}},
		{"$match": bson.M{
			"count": bson.M{"$gt": AlertHistoryKeepPerSource},
		}},
	}

	cur, err := database.History().Aggregate(ctx, pipeline, options.Aggregate())
	if err != nil {
		log.Printf("[AlertHistoryCleanup] 扫描超额分组失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	type groupResult struct {
		ID struct {
			SourceType string `bson:"sourceType"`
			SourceID   string `bson:"sourceId"`
		} `bson:"_id"`
		Count int64 `bson:"count"`
	}

	var groups []groupResult
	if err := cur.All(ctx, &groups); err != nil {
		log.Printf("[AlertHistoryCleanup] 解析超额分组失败: %v", err)
		return
	}

	if len(groups) == 0 {
		return
	}

	cleaned := 0
	for _, g := range groups {
		if err := pruneAlertHistoryForSource(ctx, g.ID.SourceType, g.ID.SourceID); err != nil {
			log.Printf("[AlertHistoryCleanup] 裁剪 [%s/%s] 失败: %v", g.ID.SourceType, g.ID.SourceID, err)
			continue
		}
		cleaned++
	}
	if cleaned > 0 {
		log.Printf("[AlertHistoryCleanup] 完成裁剪：处理 %d 个超额源", cleaned)
	}
}

// pruneAlertHistoryForSource 对单个 (sourceType, sourceId) 组合裁剪：
// 保留最新 AlertHistoryKeepPerSource 条，其余删除。
//
// 上游 GORM 版分两步：
//   1. Order("id DESC").Offset(N-1).Limit(1).First(&cutoff) — 取第 N 条
//   2. Delete where id < cutoff.ID
//
// mongo 版等价逻辑：
//   1. Find + Sort(triggeredAt DESC) + Skip(N-1) + Limit(1) 取 cutoff 的 triggeredAt
//   2. DeleteMany where triggeredAt < cutoff.triggeredAt（同一秒多条时可能多删几条，可接受的软限制）
//
// 注意：上游用自增 uint ID 做截断游标；mongo 用 ObjectID hex string 的 _id 亦可自然排序，
// 但 DeleteMany where _id < cutoff._id 在 string 类型下不可靠（字典序 ≠ 时间序）。
// 改用 triggeredAt 时间戳截断；同一秒内多条时可能略微多删，业务上可接受（软上限设计）。
func pruneAlertHistoryForSource(ctx context.Context, sourceType string, sourceID string) error {
	filter := bson.M{
		"sourceType": sourceType,
		"sourceId":   sourceID,
	}

	// 取第 N 条（按 triggeredAt DESC）的 triggeredAt 作为截断游标
	opts := options.FindOne().
		SetSort(bson.D{{Key: "triggeredAt", Value: -1}}).
		SetSkip(int64(AlertHistoryKeepPerSource - 1))

	result := database.History().FindOne(ctx, filter, opts)
	if result.Err() != nil {
		// 记录数不足 N 条，无需裁剪
		return nil
	}

	var cutoff struct {
		TriggeredAt time.Time `bson:"triggeredAt"`
	}
	if err := result.Decode(&cutoff); err != nil {
		return err
	}

	// 删除比截断游标更早的记录
	_, err := database.History().DeleteMany(ctx, bson.M{
		"sourceType":  sourceType,
		"sourceId":    sourceID,
		"triggeredAt": bson.M{"$lt": cutoff.TriggeredAt},
	})
	return err
}
