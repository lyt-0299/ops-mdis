package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// MongoMonitorScheduler MongoDB 监控调度器
type MongoMonitorScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time
	lastAlertSentCache map[string]time.Time
	runningRules       map[string]bool // 防止同一规则的 CheckRule 重叠执行
}

var (
	mongoScheduler     *MongoMonitorScheduler
	mongoSchedulerOnce sync.Once
)

// GetMongoMonitorScheduler 获取调度器单例
func GetMongoMonitorScheduler() *MongoMonitorScheduler {
	mongoSchedulerOnce.Do(func() {
		mongoScheduler = &MongoMonitorScheduler{
			stopChan:           make(chan struct{}),
			lastRunCache:       make(map[string]time.Time),
			lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypeMongoMonitor),
			runningRules:       make(map[string]bool),
		}
	})
	return mongoScheduler
}

// StartMongoMonitorScheduler 启动 MongoDB 监控调度器
func StartMongoMonitorScheduler() {
	s := GetMongoMonitorScheduler()
	s.wg.Add(1)
	go s.run()
}

// StopMongoMonitorScheduler 停止 MongoDB 监控调度器（多次调用安全）
func StopMongoMonitorScheduler() {
	s := GetMongoMonitorScheduler()
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
	log.Println("[MongoMonitor] 调度器已停止")
}

// run 调度器主循环
func (s *MongoMonitorScheduler) run() {
	defer s.wg.Done()
	log.Println("[MongoMonitor] 调度器已启动")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.dispatchRules()
		}
	}
}

// dispatchRules 分发规则到各 goroutine 检查
func (s *MongoMonitorScheduler) dispatchRules() {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "mongoRule", "status": model.TaskStatusActive})
	if err != nil {
		log.Printf("[MongoMonitor] 查询规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.MongoMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[MongoMonitor] 解析规则失败: %v", err)
		return
	}

	// 批量加载 MongoInstance 和 Webhook，注入到规则中
	s.injectAssociations(ctx, rules)

	now := time.Now()
	for _, rule := range rules {
		// 按各规则自身 Interval 独立调度
		s.mu.Lock()
		if lastRun, ok := s.lastRunCache[rule.ID]; ok {
			if now.Sub(lastRun) < time.Duration(rule.Interval)*time.Second {
				s.mu.Unlock()
				continue
			}
		}

		// 防止同一规则重叠执行
		if s.runningRules[rule.ID] {
			s.mu.Unlock()
			continue
		}
		s.runningRules[rule.ID] = true
		s.lastRunCache[rule.ID] = now
		s.mu.Unlock()

		// 值拷贝 + 指针深拷贝
		ruleCopy := rule
		if rule.FiringAt != nil {
			t := *rule.FiringAt
			ruleCopy.FiringAt = &t
		}
		if rule.RecoveredAt != nil {
			t := *rule.RecoveredAt
			ruleCopy.RecoveredAt = &t
		}
		if rule.Webhook != nil {
			w := *rule.Webhook
			ruleCopy.Webhook = &w
		}
		// MongoInstance 是值类型，无需额外深拷贝

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[MongoMonitor] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
				}
				s.mu.Lock()
				delete(s.runningRules, ruleCopy.ID)
				s.mu.Unlock()
			}()
			s.CheckRule(&ruleCopy)
		}()
	}
}

// decryptMongoInstance 解密实例密码。
//
// 与 MySQL 同理：密码在 handler 里 crypto.Encrypt 存库，用的时候必须解密。
// 凭据内嵌在 URI 里时不受影响（Password 字段为空），但从 UI 分别填 username/password
// 的实例，不解密就会拿着密文去连库、认证失败并报假告警。
// crypto.Decrypt 对明文/空串安全返回原值。
func decryptMongoInstance(inst *model.MongoInstance) {
	if dec, err := crypto.Decrypt(inst.Password); err == nil {
		inst.Password = dec
	}
}

// injectAssociations 批量查询并注入 MongoInstance 和 Webhook 关联数据
func (s *MongoMonitorScheduler) injectAssociations(ctx context.Context, rules []model.MongoMonitorRule) {
	instIDs := make(map[string]struct{})
	wbIDs := make(map[string]struct{})
	for _, r := range rules {
		if r.MongoInstanceID != "" {
			instIDs[r.MongoInstanceID] = struct{}{}
		}
		if r.WebhookID != "" {
			wbIDs[r.WebhookID] = struct{}{}
		}
	}

	// 查询 MongoInstance
	instMap := make(map[string]model.MongoInstance)
	if len(instIDs) > 0 {
		ids := make([]string, 0, len(instIDs))
		for id := range instIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "mongoInstance"})
		if err == nil {
			defer cur.Close(ctx)
			var insts []model.MongoInstance
			if cur.All(ctx, &insts) == nil {
				for _, inst := range insts {
					decryptMongoInstance(&inst)
					instMap[inst.ID] = inst
				}
			}
		}
	}

	// 查询 Webhook
	wbMap := make(map[string]model.Webhook)
	if len(wbIDs) > 0 {
		ids := make([]string, 0, len(wbIDs))
		for id := range wbIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "webhook"})
		if err == nil {
			defer cur.Close(ctx)
			var wbs []model.Webhook
			if cur.All(ctx, &wbs) == nil {
				for _, wb := range wbs {
					wbMap[wb.ID] = wb
				}
			}
		}
	}

	// 注入到规则
	for i := range rules {
		if inst, ok := instMap[rules[i].MongoInstanceID]; ok {
			rules[i].MongoInstance = inst
		}
		if rules[i].WebhookID != "" {
			if wb, ok := wbMap[rules[i].WebhookID]; ok {
				rules[i].Webhook = &wb
			}
		}
	}
}

// TryCheckRule 手动触发单条规则检查，与调度器共享 runningRules 互斥保护。
// 若该规则已在执行中则返回 false，避免与定时周期并发重叠。
func (s *MongoMonitorScheduler) TryCheckRule(rule *model.MongoMonitorRule) bool {
	s.mu.Lock()
	if s.runningRules[rule.ID] {
		s.mu.Unlock()
		return false
	}
	s.runningRules[rule.ID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.runningRules, rule.ID)
		s.mu.Unlock()
	}()
	s.checkRule(rule, true)
	return true
}

// CheckRule 执行单条规则的检查
func (s *MongoMonitorScheduler) CheckRule(rule *model.MongoMonitorRule) {
	s.checkRule(rule, false)
}

func (s *MongoMonitorScheduler) checkRule(rule *model.MongoMonitorRule, manualRefresh bool) {
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID)) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}

	// 记录本次检查时间：每次检查都写,供"最近检查"准确显示。
	// (updatedAt 只在状态/值变化时才动,稳定 OK 的规则会停在旧时间,看着像没在检查。)
	{
		now := time.Now()
		database.Config().UpdateOne(context.TODO(),
			bson.M{"_id": rule.ID, "kind": "mongoRule"},
			bson.M{"$set": bson.M{"lastCheckAt": now}})
		rule.LastCheckAt = &now
	}

	// 如果 MongoInstance/Webhook 未注入（手动触发路径），按需查询
	if rule.MongoInstanceID != "" && rule.MongoInstance.ID == "" {
		ctx := context.TODO()
		result := database.Config().FindOne(ctx, bson.M{"_id": rule.MongoInstanceID, "kind": "mongoInstance"})
		if result.Err() == nil {
			var inst model.MongoInstance
			if result.Decode(&inst) == nil {
				decryptMongoInstance(&inst)
				rule.MongoInstance = inst
			}
		}
	}
	if rule.WebhookID != "" && rule.Webhook == nil {
		ctx := context.TODO()
		result := database.Config().FindOne(ctx, bson.M{"_id": rule.WebhookID, "kind": "webhook"})
		if result.Err() == nil {
			var wb model.Webhook
			if result.Decode(&wb) == nil {
				rule.Webhook = &wb
			}
		}
	}

	client, err := NewMongoPoolClient(&rule.MongoInstance)
	if err != nil {
		log.Printf("[MongoMonitor] 规则 [%s] 创建客户端失败: %v", rule.Name, err)
		if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
			s.updatePausedMongoSnapshot(rule, true, 0, fmt.Sprintf("连接失败: %v", err))
			return
		}
		s.handleCheckError(rule, fmt.Sprintf("连接失败: %v", err))
		return
	}

	switch rule.MonitorType {
	case "replica_status":
		s.checkReplicaStatus(rule, client, manualRefresh)
	case "oplog_lag":
		s.checkOplogLag(rule, client, manualRefresh)
	case "oplog_window":
		s.checkOplogWindow(rule, client, manualRefresh)
	default:
		log.Printf("[MongoMonitor] 规则 [%s] 未知监控类型: %s", rule.Name, rule.MonitorType)
	}
}

// checkReplicaStatus 检查副本集状态（四态状态机，支持 Duration 与 Recovery）
func (s *MongoMonitorScheduler) checkReplicaStatus(rule *model.MongoMonitorRule, client *MongoPoolClient, manualRefresh bool) {
	ctx, cancel := mongoContextWithTimeout(rule.Interval)
	defer cancel()

	abnormal, detail, errMsg, err := client.CheckReplicaStatus(ctx)

	if !manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}

	// 保存详情
	if detail != nil {
		if detailJSON, e := json.Marshal(detail); e == nil {
			dbCtx := context.TODO()
			database.Config().UpdateOne(dbCtx,
				bson.M{"_id": rule.ID, "kind": "mongoRule"},
				bson.M{"$set": bson.M{"lastDetail": string(detailJSON)}},
			)
		}
	}

	if err != nil {
		log.Printf("[MongoMonitor] 规则 [%s] 检查副本集状态失败: %v", rule.Name, err)
		if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
			s.updatePausedMongoSnapshot(rule, true, 0, fmt.Sprintf("检查失败: %v", err))
			return
		}
		s.handleCheckError(rule, fmt.Sprintf("检查失败: %v", err))
		return
	}
	handled, effectiveAbnormal, err := s.applyMongoInstanceSilences(rule, buildMongoReplicaSnapshots(detail), abnormal, manualRefresh)
	if err != nil {
		log.Printf("[MongoMonitor] 应用规则 %s 实例静默失败: %v", rule.Name, err)
		return
	}
	if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		s.updatePausedMongoSnapshot(rule, effectiveAbnormal, 0, "")
		return
	}
	if handled {
		return
	}

	// 使用状态机处理四态转变
	s.processStateMachine(rule, effectiveAbnormal, 0, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "告警中", errMsg, ""),
			s.buildMongoAlertData(rule, model.AlertStateFiring, errMsg, "")
	}, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "已恢复", "副节点重连或拓扑恢复正常", ""),
			s.buildMongoAlertData(rule, model.AlertStateRecovered, "副节点重连或拓扑恢复正常", "")
	})
}

// checkOplogLag 检查 Oplog 延迟（四态状态机）
func (s *MongoMonitorScheduler) checkOplogLag(rule *model.MongoMonitorRule, client *MongoPoolClient, manualRefresh bool) {
	ctx, cancel := mongoContextWithTimeout(rule.Interval)
	defer cancel()

	lagResults, err := client.GetOplogLag(ctx)

	if !manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}

	// 保存详情
	if lagResults != nil {
		if detailJSON, e := json.Marshal(lagResults); e == nil {
			dbCtx := context.TODO()
			database.Config().UpdateOne(dbCtx,
				bson.M{"_id": rule.ID, "kind": "mongoRule"},
				bson.M{"$set": bson.M{"lastDetail": string(detailJSON)}},
			)
		}
	}

	if err != nil {
		log.Printf("[MongoMonitor] 规则 [%s] 获取 Oplog 延迟失败: %v", rule.Name, err)
		if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
			s.updatePausedMongoSnapshot(rule, true, 0, fmt.Sprintf("获取 Oplog 延迟失败: %v", err))
			return
		}
		s.handleCheckError(rule, fmt.Sprintf("获取 Oplog 延迟失败: %v", err))
		return
	}

	// 取所有 Secondary 的最大延迟
	var maxLag float64
	for _, r := range lagResults {
		if r.LagSeconds > maxLag {
			maxLag = r.LagSeconds
		}
	}

	dbCtx := context.TODO()
	database.Config().UpdateOne(dbCtx,
		bson.M{"_id": rule.ID, "kind": "mongoRule"},
		bson.M{"$set": bson.M{"lastValue": maxLag}},
	)

	triggered := evaluateMongoCondition(maxLag, rule.Operator, rule.Threshold)
	handled, effectiveTriggered, err := s.applyMongoInstanceSilences(rule, buildMongoOplogLagSnapshots(rule, lagResults), triggered, manualRefresh)
	if err != nil {
		log.Printf("[MongoMonitor] 应用规则 %s 实例静默失败: %v", rule.Name, err)
		return
	}
	if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		s.updatePausedMongoSnapshot(rule, effectiveTriggered, maxLag, "")
		return
	}
	if handled {
		return
	}

	s.processStateMachine(rule, effectiveTriggered, maxLag, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "告警中", fmt.Sprintf("%.1f 秒", maxLag), fmt.Sprintf("%s %.1f 秒", rule.Operator, rule.Threshold)),
			s.buildMongoAlertData(rule, model.AlertStateFiring, fmt.Sprintf("%.1f 秒", maxLag), fmt.Sprintf("%s %.1f 秒", rule.Operator, rule.Threshold))
	}, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "已恢复", fmt.Sprintf("%.1f 秒", maxLag), ""),
			s.buildMongoAlertData(rule, model.AlertStateRecovered, fmt.Sprintf("%.1f 秒", maxLag), "")
	})
}

// checkOplogWindow 检查 Oplog 保存时间窗口（四态状态机）
func (s *MongoMonitorScheduler) checkOplogWindow(rule *model.MongoMonitorRule, client *MongoPoolClient, manualRefresh bool) {
	ctx, cancel := mongoContextWithTimeout(rule.Interval)
	defer cancel()

	windowResults, err := client.GetOplogWindows(ctx)

	if !manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}

	// 保存详情
	if windowResults != nil {
		if detailJSON, e := json.Marshal(windowResults); e == nil {
			dbCtx := context.TODO()
			database.Config().UpdateOne(dbCtx,
				bson.M{"_id": rule.ID, "kind": "mongoRule"},
				bson.M{"$set": bson.M{"lastDetail": string(detailJSON)}},
			)
		}
	}

	if err != nil {
		log.Printf("[MongoMonitor] 规则 [%s] 获取 Oplog 窗口失败: %v", rule.Name, err)
		if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
			s.updatePausedMongoSnapshot(rule, true, 0, fmt.Sprintf("获取 Oplog 窗口失败: %v", err))
			return
		}
		s.handleCheckError(rule, fmt.Sprintf("获取 Oplog 窗口失败: %v", err))
		return
	}

	if len(windowResults) == 0 {
		return
	}

	// 找出最短的保存时间
	var minWindow float64 = math.MaxFloat64
	for _, r := range windowResults {
		if r.WindowHours < minWindow {
			minWindow = r.WindowHours
		}
	}

	dbCtx := context.TODO()
	database.Config().UpdateOne(dbCtx,
		bson.M{"_id": rule.ID, "kind": "mongoRule"},
		bson.M{"$set": bson.M{"lastValue": minWindow}},
	)

	triggered := evaluateMongoCondition(minWindow, rule.Operator, rule.Threshold)
	handled, effectiveTriggered, err := s.applyMongoInstanceSilences(rule, buildMongoOplogWindowSnapshots(rule, windowResults), triggered, manualRefresh)
	if err != nil {
		log.Printf("[MongoMonitor] 应用规则 %s 实例静默失败: %v", rule.Name, err)
		return
	}
	if manualRefresh && !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		s.updatePausedMongoSnapshot(rule, effectiveTriggered, minWindow, "")
		return
	}
	if handled {
		return
	}

	s.processStateMachine(rule, effectiveTriggered, minWindow, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "告警中", fmt.Sprintf("%.1f 小时", minWindow), fmt.Sprintf("%s %.1f 小时", rule.Operator, rule.Threshold)),
			s.buildMongoAlertData(rule, model.AlertStateFiring, fmt.Sprintf("%.1f 小时", minWindow), fmt.Sprintf("%s %.1f 小时", rule.Operator, rule.Threshold))
	}, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "已恢复", fmt.Sprintf("%.1f 小时", minWindow), ""),
			s.buildMongoAlertData(rule, model.AlertStateRecovered, fmt.Sprintf("%.1f 小时", minWindow), "")
	})
}

func (s *MongoMonitorScheduler) updatePausedMongoSnapshot(rule *model.MongoMonitorRule, triggered bool, value float64, detail string) {
	snapshotState := model.AlertStateOK
	if triggered {
		snapshotState = model.AlertStateFiring
	}
	updates := bson.M{
		"alertState":  snapshotState,
		"firingAt":    nil,
		"recoveredAt": nil,
	}
	if rule.MonitorType != "replica_status" {
		updates["lastValue"] = value
	}
	if detail != "" {
		updates["lastDetail"] = detail
	}
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "mongoRule"},
		bson.M{"$set": updates},
	); err != nil {
		log.Printf("[MongoMonitor] 更新暂停规则刷新结果失败: %v", err)
	}
}

// processStateMachine 通用四态状态机处理（OK → Pending → Firing → Recovered）
func (s *MongoMonitorScheduler) processStateMachine(rule *model.MongoMonitorRule, triggered bool, value float64, firingMsg func() (string, map[string]interface{}), recoveredMsg func() (string, map[string]interface{})) {
	if !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}
	ctx := context.TODO()
	now := time.Now()

	// Duration 对齐：若 Duration < Interval，自动对齐为 Interval
	duration := rule.Duration
	if duration < rule.Interval {
		duration = rule.Interval
	}

	if triggered {
		// 异常触发：使用单次原子 UpdateOne 更新所有字段
		rule.RecoveredAt = nil

		switch rule.AlertState {
		case model.AlertStateOK, model.AlertStateRecovered:
			// 首次触发，进入 Pending
			database.Config().UpdateOne(ctx,
				bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
				bson.M{"$set": bson.M{
					"alertState":  model.AlertStatePending,
					"firingAt":    now,
					"recoveredAt": nil,
				}},
			)
			rule.AlertState = model.AlertStatePending
			rule.FiringAt = &now
			log.Printf("[MongoMonitor] 规则 [%s] 进入 Pending 状态", rule.Name)

		case model.AlertStatePending:
			// 检查是否满足 Duration
			if rule.FiringAt != nil && now.Sub(*rule.FiringAt) >= time.Duration(duration)*time.Second {
				database.Config().UpdateOne(ctx,
					bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
					bson.M{"$set": bson.M{
						"alertState":  model.AlertStateFiring,
						"recoveredAt": nil,
					}},
				)
				rule.AlertState = model.AlertStateFiring
				log.Printf("[MongoMonitor] 规则 [%s] 进入 Firing 状态", rule.Name)
				content, data := firingMsg()
				s.sendNotification(rule, content, data)
			} else {
				database.Config().UpdateOne(ctx,
					bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
					bson.M{"$set": bson.M{"recoveredAt": nil}},
				)
			}

		case model.AlertStateFiring:
			// 持续 Firing，清除恢复计时
			database.Config().UpdateOne(ctx,
				bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
				bson.M{"$set": bson.M{"recoveredAt": nil}},
			)
			if rule.RepeatAlert {
				content, data := firingMsg()
				s.sendNotification(rule, content, data)
			} else if rule.RemindInterval > 0 {
				content, data := firingMsg()
				s.sendRemindIfNeeded(rule, content, data)
			}
		}
	} else {
		// 正常恢复
		rule.FiringAt = nil

		switch rule.AlertState {
		case model.AlertStatePending:
			// Pending 直接恢复到 OK，不发通知
			database.Config().UpdateOne(ctx,
				bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
				bson.M{"$set": bson.M{
					"alertState":  model.AlertStateOK,
					"firingAt":    nil,
					"recoveredAt": nil,
				}},
			)
			log.Printf("[MongoMonitor] 规则 [%s] 从 Pending 直接恢复为 OK", rule.Name)

		case model.AlertStateFiring:
			recoveryDuration := rule.RecoveryDuration
			if recoveryDuration <= 0 {
				// 立即恢复
				database.Config().UpdateOne(ctx,
					bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
					bson.M{"$set": bson.M{
						"alertState":  model.AlertStateRecovered,
						"firingAt":    nil,
						"recoveredAt": nil,
					}},
				)
				rule.AlertState = model.AlertStateRecovered
				content, data := recoveredMsg()
				s.sendNotification(rule, content, data)
			} else {
				// 开始恢复计时
				if rule.RecoveredAt == nil {
					database.Config().UpdateOne(ctx,
						bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
						bson.M{"$set": bson.M{
							"recoveredAt": now,
							"firingAt":    nil,
						}},
					)
					rule.RecoveredAt = &now
					log.Printf("[MongoMonitor] 规则 [%s] 开始恢复计时", rule.Name)
				} else if now.Sub(*rule.RecoveredAt) >= time.Duration(recoveryDuration)*time.Second {
					// 恢复计时到期
					database.Config().UpdateOne(ctx,
						bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
						bson.M{"$set": bson.M{
							"alertState":  model.AlertStateRecovered,
							"firingAt":    nil,
							"recoveredAt": nil,
						}},
					)
					rule.AlertState = model.AlertStateRecovered
					content, data := recoveredMsg()
					s.sendNotification(rule, content, data)
				}
			}

		case model.AlertStateRecovered:
			// 已发过恢复通知，置回 OK
			database.Config().UpdateOne(ctx,
				bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
				bson.M{"$set": bson.M{
					"alertState":  model.AlertStateOK,
					"firingAt":    nil,
					"recoveredAt": nil,
				}},
			)
		}
	}
}

// handleCheckError 处理检查错误（视为异常触发），全部接入状态机
func (s *MongoMonitorScheduler) handleCheckError(rule *model.MongoMonitorRule, errMsg string) {
	s.processStateMachine(rule, true, 0, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "告警中", errMsg, ""),
			s.buildMongoAlertData(rule, model.AlertStateFiring, errMsg, "")
	}, func() (string, map[string]interface{}) {
		return s.buildMongoAlertContent(rule, "已恢复", "已恢复正常", ""),
			s.buildMongoAlertData(rule, model.AlertStateRecovered, "已恢复正常", "")
	})
}

// sendNotification 发送通知
func (s *MongoMonitorScheduler) sendNotification(rule *model.MongoMonitorRule, content string, data map[string]interface{}) {
	if !isMonitorActive(model.SourceTypeMongoMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMongoMonitor, rule.ID, rule.Name)
		return
	}

	if rule.AlertNote != "" {
		content += "\n备注: " + rule.AlertNote
	}

	webhookName := ""
	webhookType := ""

	if rule.Webhook != nil {
		title := GetAlertEngine().getAlertTitle()
		webhookName = rule.Webhook.Name
		webhookType = rule.Webhook.Type
		if err := SendNotification(rule.Webhook, title, content, data); err != nil {
			log.Printf("[MongoMonitor] 告警发送失败: %v", err)
		} else {
			log.Printf("[MongoMonitor] 告警发送成功: %s", rule.Name)
		}
	}

	// 保存告警历史
	alertState := rule.AlertState
	if alertState == "" || alertState == model.AlertStateOK || alertState == model.AlertStatePending {
		alertState = model.AlertStateFiring
	}
	SaveAlertHistory(model.SourceTypeMongoMonitor, rule.ID, rule.Name, alertState, content, webhookName, webhookType)

	// 更新 lastAlertSentCache
	s.mu.Lock()
	s.lastAlertSentCache[rule.ID] = time.Now()
	s.mu.Unlock()
}

// sendRemindIfNeeded 按 RemindInterval 发送提醒
func (s *MongoMonitorScheduler) sendRemindIfNeeded(rule *model.MongoMonitorRule, content string, data map[string]interface{}) {
	s.mu.Lock()
	lastSent, ok := s.lastAlertSentCache[rule.ID]
	s.mu.Unlock()
	if ok {
		if time.Since(lastSent) < time.Duration(rule.RemindInterval)*time.Second {
			return
		}
	}
	s.sendNotification(rule, content, data)
}

func (s *MongoMonitorScheduler) applyMongoInstanceSilences(rule *model.MongoMonitorRule, snapshots []MonitorInstanceSnapshot, triggered bool, manualRefresh bool) (handled bool, effectiveTriggered bool, err error) {
	silences, err := LoadActiveSilences(model.SourceTypeMongoMonitor, rule.ID)
	if err != nil {
		return false, triggered, fmt.Errorf("查询静默配置失败: %w", err)
	}
	if err := UpdateMonitorInstanceStates(SuppressedHistoryMeta{
		SourceType:     model.SourceTypeMongoMonitor,
		SourceID:       rule.ID,
		SourceName:     rule.Name,
		Duration:       rule.Duration,
		Interval:       rule.Interval,
		RemindInterval: rule.RemindInterval,
	}, snapshots, silences, !manualRefresh); err != nil {
		return false, triggered, fmt.Errorf("更新实例明细失败: %w", err)
	}
	if !triggered {
		return false, false, nil
	}
	firingKeys := snapshotFiringKeys(snapshots)
	if len(firingKeys) == 0 {
		return false, true, nil
	}
	if hasUnmutedFiringKey(firingKeys, silences) {
		return false, true, nil
	}
	s.resetFullySilencedRule(rule)
	return true, false, nil
}

func (s *MongoMonitorScheduler) resetFullySilencedRule(rule *model.MongoMonitorRule) {
	s.mu.Lock()
	delete(s.lastAlertSentCache, rule.ID)
	s.mu.Unlock()
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "mongoRule", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		}},
	); err != nil {
		log.Printf("[MongoMonitor] 重置全静默规则状态失败: %v", err)
	}
	rule.AlertState = model.AlertStateOK
	rule.FiringAt = nil
	rule.RecoveredAt = nil
}

func snapshotFiringKeys(snapshots []MonitorInstanceSnapshot) []string {
	keys := make([]string, 0)
	for _, snapshot := range snapshots {
		if snapshot.AlertState == model.AlertStateFiring {
			keys = append(keys, snapshot.ScopeKey)
		}
	}
	return keys
}

func buildMongoReplicaSnapshots(detail *ReplicaStatusDetail) []MonitorInstanceSnapshot {
	if detail == nil {
		return nil
	}
	normalStates := map[string]bool{"PRIMARY": true, "SECONDARY": true, "ARBITER": true}
	snapshots := make([]MonitorInstanceSnapshot, 0, len(detail.Members))
	for _, member := range detail.Members {
		firing := !normalStates[member.StateStr] || member.Health != 1
		state := model.AlertStateOK
		value := 1.0
		if firing {
			state = model.AlertStateFiring
			value = 0
		}
		memberDetail, _ := json.Marshal(member)
		snapshots = append(snapshots, MonitorInstanceSnapshot{
			ScopeKey:    member.Name,
			DisplayName: member.Name,
			AlertState:  state,
			LastValue:   value,
			LastDetail:  string(memberDetail),
		})
	}
	return snapshots
}

func buildMongoOplogLagSnapshots(rule *model.MongoMonitorRule, results []OplogLagResult) []MonitorInstanceSnapshot {
	snapshots := make([]MonitorInstanceSnapshot, 0, len(results))
	for _, result := range results {
		firing := evaluateMongoCondition(result.LagSeconds, rule.Operator, rule.Threshold)
		state := model.AlertStateOK
		if firing {
			state = model.AlertStateFiring
		}
		detail, _ := json.Marshal(result)
		snapshots = append(snapshots, MonitorInstanceSnapshot{
			ScopeKey:    result.MemberName,
			DisplayName: result.MemberName,
			AlertState:  state,
			LastValue:   result.LagSeconds,
			LastDetail:  string(detail),
		})
	}
	return snapshots
}

func buildMongoOplogWindowSnapshots(rule *model.MongoMonitorRule, results []OplogWindowResult) []MonitorInstanceSnapshot {
	snapshots := make([]MonitorInstanceSnapshot, 0, len(results))
	for _, result := range results {
		firing := evaluateMongoCondition(result.WindowHours, rule.Operator, rule.Threshold)
		state := model.AlertStateOK
		if firing {
			state = model.AlertStateFiring
		}
		detail, _ := json.Marshal(result)
		snapshots = append(snapshots, MonitorInstanceSnapshot{
			ScopeKey:    result.MemberName,
			DisplayName: result.MemberName,
			AlertState:  state,
			LastValue:   result.WindowHours,
			LastDetail:  string(detail),
		})
	}
	return snapshots
}

// getMongoMonitorTypeLabel 将监控类型 key 转换为人类可读名称
func getMongoMonitorTypeLabel(monitorType string) string {
	switch monitorType {
	case "replica_status":
		return "副本集状态"
	case "oplog_lag":
		return "Oplog 延迟"
	case "oplog_window":
		return "Oplog 保存时间"
	default:
		return monitorType
	}
}

// buildMongoAlertContent 构造结构化告警消息
func (s *MongoMonitorScheduler) buildMongoAlertContent(rule *model.MongoMonitorRule, statusText, currentVal, thresholdText string) string {
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	monitorTypeStr := getMongoMonitorTypeLabel(rule.MonitorType)

	content := fmt.Sprintf("任务名称：%s\nMongoDB 实例：%s\n监控类型：%s\n检测数值/详情：%s",
		rule.Name, rule.MongoInstance.Name, monitorTypeStr, currentVal)

	if thresholdText != "" {
		content += fmt.Sprintf("\n告警条件：阈值 %s", thresholdText)
	}

	content += fmt.Sprintf("\n检查时间：%s\n状态：%s", nowStr, statusText)

	return content
}

// buildMongoAlertData 构造供结构化通知渠道(企微/飞书等)使用的 Payload
func (s *MongoMonitorScheduler) buildMongoAlertData(rule *model.MongoMonitorRule, alertStatus string, currentVal, thresholdText string) map[string]interface{} {
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	monitorTypeStr := getMongoMonitorTypeLabel(rule.MonitorType)

	return map[string]interface{}{
		"event_type":     "mongo_monitor_alert",
		"task_name":      rule.Name,
		"mongo_instance": rule.MongoInstance.Name,
		"monitor_type":   monitorTypeStr,
		"current_value":  currentVal,
		"threshold":      thresholdText,
		"check_time":     nowStr,
		"alert_status":   alertStatus,
	}
}

// evaluateMongoCondition 评估条件
func evaluateMongoCondition(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case "==":
		return math.Abs(value-threshold) < 1e-9
	case "!=":
		return math.Abs(value-threshold) >= 1e-9
	default:
		return value > threshold
	}
}

// mongoContextWithTimeout 根据 Interval 计算合理的操作超时上限。
func mongoContextWithTimeout(interval int) (context.Context, context.CancelFunc) {
	timeout := time.Duration(interval/2) * time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}
