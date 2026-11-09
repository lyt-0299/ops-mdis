package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

var ErrKafkaRebalanceMonitorRuleRunning = fmt.Errorf("kafka rebalance monitor rule is already running")

type KafkaRebalanceMonitorScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time
	lastAlertSentCache map[string]time.Time
	runningRules       map[string]bool
}

var (
	globalKafkaRebalanceMonitorScheduler *KafkaRebalanceMonitorScheduler
	kafkaRebalanceMonitorSchedulerOnce   sync.Once
)

func NewKafkaRebalanceMonitorScheduler() *KafkaRebalanceMonitorScheduler {
	return &KafkaRebalanceMonitorScheduler{
		stopChan:           make(chan struct{}),
		lastRunCache:       make(map[string]time.Time),
		lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypeKafkaRebalanceMonitor),
		runningRules:       make(map[string]bool),
	}
}

func GetKafkaRebalanceMonitorScheduler() *KafkaRebalanceMonitorScheduler {
	kafkaRebalanceMonitorSchedulerOnce.Do(func() {
		globalKafkaRebalanceMonitorScheduler = NewKafkaRebalanceMonitorScheduler()
	})
	return globalKafkaRebalanceMonitorScheduler
}

func StartKafkaRebalanceMonitorScheduler() {
	GetKafkaRebalanceMonitorScheduler().Start()
}

func StopKafkaRebalanceMonitorScheduler() {
	if globalKafkaRebalanceMonitorScheduler != nil {
		globalKafkaRebalanceMonitorScheduler.Stop()
	}
}

func (s *KafkaRebalanceMonitorScheduler) Start() {
	log.Println("启动 Kafka 重平衡监控调度器...")
	s.wg.Add(1)
	go s.dispatcher()
}

func (s *KafkaRebalanceMonitorScheduler) Stop() {
	log.Println("停止 Kafka 重平衡监控调度器...")
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

func (s *KafkaRebalanceMonitorScheduler) dispatcher() {
	defer s.wg.Done()
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

func (s *KafkaRebalanceMonitorScheduler) dispatchRules() {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "kafkaRebalanceRule", "status": model.TaskStatusActive})
	if err != nil {
		log.Printf("[KafkaRebalanceMonitorScheduler] 查询规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.KafkaRebalanceMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[KafkaRebalanceMonitorScheduler] 解析规则失败: %v", err)
		return
	}

	// 批量加载 KafkaInstance 和 Webhook，注入到规则中
	s.injectAssociations(ctx, rules)

	now := time.Now()
	for _, rule := range rules {
		s.mu.Lock()
		lastRun, exists := s.lastRunCache[rule.ID]
		shouldRun := !exists || now.Sub(lastRun).Seconds() >= float64(rule.Interval)
		if shouldRun {
			if s.runningRules[rule.ID] {
				s.mu.Unlock()
				continue
			}
			s.runningRules[rule.ID] = true
			s.lastRunCache[rule.ID] = now
		}
		s.mu.Unlock()

		if !shouldRun {
			continue
		}

		ruleCopy := rule
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[KafkaRebalanceMonitorScheduler] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
				}
				s.mu.Lock()
				delete(s.runningRules, ruleCopy.ID)
				s.mu.Unlock()
			}()
			if err := s.CheckRule(&ruleCopy); err != nil {
				log.Printf("[KafkaRebalanceMonitorScheduler] 规则 %s 检查失败: %v", ruleCopy.Name, err)
			}
		}()
	}
}

// injectAssociations 批量查询并注入 KafkaInstance 和 Webhook 关联数据
func (s *KafkaRebalanceMonitorScheduler) injectAssociations(ctx context.Context, rules []model.KafkaRebalanceMonitorRule) {
	instIDs := make(map[string]struct{})
	wbIDs := make(map[string]struct{})
	for _, r := range rules {
		if r.KafkaInstanceID != "" {
			instIDs[r.KafkaInstanceID] = struct{}{}
		}
		if r.WebhookID != "" {
			wbIDs[r.WebhookID] = struct{}{}
		}
	}

	instMap := make(map[string]model.KafkaInstance)
	if len(instIDs) > 0 {
		ids := make([]string, 0, len(instIDs))
		for id := range instIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "kafkaInstance"})
		if err == nil {
			defer cur.Close(ctx)
			var insts []model.KafkaInstance
			if cur.All(ctx, &insts) == nil {
				for _, inst := range insts {
					instMap[inst.ID] = inst
				}
			}
		}
	}

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

	for i := range rules {
		if inst, ok := instMap[rules[i].KafkaInstanceID]; ok {
			rules[i].KafkaInstance = inst
		}
		if rules[i].WebhookID != "" {
			if wb, ok := wbMap[rules[i].WebhookID]; ok {
				rules[i].Webhook = &wb
			}
		}
	}
}

func (s *KafkaRebalanceMonitorScheduler) TryCheckRule(rule *model.KafkaRebalanceMonitorRule) error {
	// 先注入关联再检查：定时路径统一注入过，而手动触发（UI「立即检查」）是 api 层
	// 把库里读出的规则直接递进来，Kafka 实例与通知渠道这类 `bson:"-"` 字段全是零值——
	// 检查即使跑通也发不出通知。放在入口，调用方不必记得注入。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rules := []model.KafkaRebalanceMonitorRule{*rule}
	s.injectAssociations(ctx, rules)
	cancel()
	*rule = rules[0]

	s.mu.Lock()
	if s.runningRules[rule.ID] {
		s.mu.Unlock()
		return ErrKafkaRebalanceMonitorRuleRunning
	}
	s.runningRules[rule.ID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.runningRules, rule.ID)
		s.mu.Unlock()
	}()
	return s.checkRule(rule, true)
}

func (s *KafkaRebalanceMonitorScheduler) CheckRule(rule *model.KafkaRebalanceMonitorRule) error {
	return s.checkRule(rule, false)
}

func (s *KafkaRebalanceMonitorScheduler) checkRule(rule *model.KafkaRebalanceMonitorRule, manualRefresh bool) error {
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeKafkaRebalanceMonitor, rule.ID)) {
		logSkipPaused(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name)
		return nil
	}

	// 如果 KafkaInstance/Webhook 未注入（手动触发路径），按需查询
	if rule.KafkaInstanceID != "" && rule.KafkaInstance.ID == "" {
		ctx := context.TODO()
		result := database.Config().FindOne(ctx, bson.M{"_id": rule.KafkaInstanceID, "kind": "kafkaInstance"})
		if result.Err() == nil {
			var inst model.KafkaInstance
			if result.Decode(&inst) == nil {
				rule.KafkaInstance = inst
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

	ctx := context.Background()
	checker := NewKafkaRebalanceChecker(rule.KafkaInstance, rule.ConsumerGroup, rule.Topic, rule.KafkaVersion, rule.CommandTimeoutSeconds)
	initial := checker.Check(ctx)
	if err := s.updateCheckSnapshot(rule, initial); err != nil {
		return err
	}

	if manualRefresh {
		return s.updateManualSnapshotState(rule, initial)
	}
	if !isMonitorActive(model.SourceTypeKafkaRebalanceMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name)
		return nil
	}

	switch {
	case initial.State == model.KafkaRebalanceStateError:
		return s.handleDetectionError(rule, checker, initial)
	case IsKafkaRebalanceAbnormalState(initial.State):
		return s.handleAbnormal(rule, checker, initial)
	default:
		return s.markNormal(rule, initial, false)
	}
}

func (s *KafkaRebalanceMonitorScheduler) handleDetectionError(rule *model.KafkaRebalanceMonitorRule, checker *KafkaRebalanceChecker, initial KafkaRebalanceCheckResult) error {
	if !s.waitSeconds(rule.ErrorRecheckWaitSeconds) {
		return nil
	}
	if !isMonitorActive(model.SourceTypeKafkaRebalanceMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name)
		return nil
	}

	rechecked := checker.Check(context.Background())
	if err := s.updateCheckSnapshot(rule, rechecked); err != nil {
		return err
	}
	if rechecked.State == model.KafkaRebalanceStateError {
		return s.markFiring(rule, "检测失败", rechecked, rule.NotifyOnDetectionError, false)
	}
	if IsKafkaRebalanceAbnormalState(rechecked.State) {
		return s.markFiring(rule, "检测恢复但消费组仍异常", rechecked, rule.NotifyOnRestartStillAbnormal, false)
	}
	return s.markNormal(rule, rechecked, true)
}

func (s *KafkaRebalanceMonitorScheduler) handleAbnormal(rule *model.KafkaRebalanceMonitorRule, checker *KafkaRebalanceChecker, initial KafkaRebalanceCheckResult) error {
	if !s.waitSeconds(rule.RebalanceInitialWaitSeconds) {
		return nil
	}
	if !isMonitorActive(model.SourceTypeKafkaRebalanceMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name)
		return nil
	}

	confirmed := checker.Check(context.Background())
	if err := s.updateCheckSnapshot(rule, confirmed); err != nil {
		return err
	}

	if confirmed.State == model.KafkaRebalanceStateNormal {
		return s.markRecoveredEvent(rule, "异常观察期内自动恢复", confirmed, rule.NotifyOnRebalanceAutoFixed)
	}
	if confirmed.State == model.KafkaRebalanceStateError {
		return s.markFiring(rule, "等待自动恢复期间检测失败", confirmed, rule.NotifyOnDetectionError, false)
	}

	// AutoRestart 在 MDIS 中无 K8s 支持，若配置了该字段则记录日志后跳过重启
	if !rule.AutoRestart {
		return s.markFiring(rule, "持续异常，未启用自动重启", confirmed, rule.NotifyOnRestartStillAbnormal, false)
	}

	// AutoRestart=true 但 MDIS 无 K8s 实例支持：记录为 Firing 并告知不支持
	log.Printf("[KafkaRebalanceMonitorScheduler] 规则 %s 配置了 AutoRestart=true，但 MDIS 暂不支持 K8s 自动重启，跳过重启步骤", rule.Name)
	result := KafkaRebalanceCheckResult{
		State:   confirmed.State,
		Message: "AutoRestart 已配置但 MDIS 当前版本不支持 K8s 自动重启",
	}
	return s.markFiring(rule, "持续异常（自动重启不支持）", result, rule.NotifyOnRestartStillAbnormal, false)
}

func (s *KafkaRebalanceMonitorScheduler) updateCheckSnapshot(rule *model.KafkaRebalanceMonitorRule, result KafkaRebalanceCheckResult) error {
	now := time.Now()
	rule.LastState = result.State
	rule.LastMessage = result.Message
	rule.LastCheckedAt = &now

	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "kafkaRebalanceRule"},
		bson.M{"$set": bson.M{
			"lastState":     result.State,
			"lastMessage":   result.Message,
			"lastCheckedAt": now,
		}},
	); err != nil {
		return fmt.Errorf("更新检查结果失败: %w", err)
	}
	return nil
}

func (s *KafkaRebalanceMonitorScheduler) updateManualSnapshotState(rule *model.KafkaRebalanceMonitorRule, result KafkaRebalanceCheckResult) error {
	alertState := model.AlertStateOK
	if IsKafkaRebalanceAbnormalState(result.State) || result.State == model.KafkaRebalanceStateError {
		alertState = model.AlertStateFiring
	}
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "kafkaRebalanceRule"},
		bson.M{"$set": bson.M{"alertState": alertState}},
	); err != nil {
		return fmt.Errorf("更新手动刷新状态失败: %w", err)
	}
	rule.AlertState = alertState
	return nil
}

func (s *KafkaRebalanceMonitorScheduler) markNormal(rule *model.KafkaRebalanceMonitorRule, result KafkaRebalanceCheckResult, notifyRecovered bool) error {
	previousState := rule.AlertState
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "kafkaRebalanceRule"},
		bson.M{"$set": bson.M{"alertState": model.AlertStateOK}},
	); err != nil {
		return fmt.Errorf("更新正常状态失败: %w", err)
	}
	rule.AlertState = model.AlertStateOK
	if notifyRecovered && previousState == model.AlertStateFiring {
		s.sendKafkaRebalanceEvent(rule, "检测恢复正常", model.AlertStateRecovered, result, "", false)
	}
	// 无论是否发送了恢复通知，只要从 Firing 回到 OK，都需要清理提醒间隔缓存，
	// 避免残留的缓存时间戳影响下次告警的 shouldSendFiring 判断。
	if previousState == model.AlertStateFiring {
		s.mu.Lock()
		delete(s.lastAlertSentCache, rule.ID)
		s.mu.Unlock()
	}
	return nil
}

func (s *KafkaRebalanceMonitorScheduler) markRecoveredEvent(rule *model.KafkaRebalanceMonitorRule, eventName string, result KafkaRebalanceCheckResult, eventEnabled bool) error {
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "kafkaRebalanceRule"},
		bson.M{"$set": bson.M{"alertState": model.AlertStateRecovered}},
	); err != nil {
		return fmt.Errorf("更新恢复状态失败: %w", err)
	}
	rule.AlertState = model.AlertStateRecovered
	if eventEnabled {
		s.sendKafkaRebalanceEvent(rule, eventName, model.AlertStateRecovered, result, "", false)
	}
	s.mu.Lock()
	delete(s.lastAlertSentCache, rule.ID)
	s.mu.Unlock()
	return nil
}

func (s *KafkaRebalanceMonitorScheduler) markFiring(rule *model.KafkaRebalanceMonitorRule, eventName string, result KafkaRebalanceCheckResult, eventEnabled bool, forceHistory bool) error {
	now := time.Now()
	shouldNotify := eventEnabled && s.shouldSendFiring(rule, now)
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "kafkaRebalanceRule"},
		bson.M{"$set": bson.M{"alertState": model.AlertStateFiring}},
	); err != nil {
		return fmt.Errorf("更新告警状态失败: %w", err)
	}
	rule.AlertState = model.AlertStateFiring

	if shouldNotify || forceHistory {
		s.sendKafkaRebalanceEvent(rule, eventName, model.AlertStateFiring, result, "", !shouldNotify)
		if shouldNotify {
			s.mu.Lock()
			s.lastAlertSentCache[rule.ID] = now
			s.mu.Unlock()
		}
	}
	return nil
}

func (s *KafkaRebalanceMonitorScheduler) shouldSendFiring(rule *model.KafkaRebalanceMonitorRule, now time.Time) bool {
	if rule.AlertState != model.AlertStateFiring {
		return true
	}
	// 如果既未开启重复告警，提醒间隔也无效，则不重复发送
	if !rule.RepeatAlert && rule.RemindInterval <= 0 {
		return false
	}
	// 不管是 RepeatAlert 还是普通的提醒，都受 RemindInterval 控制
	if rule.RemindInterval <= 0 {
		// 防御性：理论上 validate 已拦截，但若在 DB 中强制修改，回退为不发送
		return false
	}
	s.mu.Lock()
	lastSent, exists := s.lastAlertSentCache[rule.ID]
	s.mu.Unlock()
	return !exists || now.Sub(lastSent).Seconds() >= float64(rule.RemindInterval)
}

func (s *KafkaRebalanceMonitorScheduler) sendKafkaRebalanceEvent(rule *model.KafkaRebalanceMonitorRule, eventName, alertState string, result KafkaRebalanceCheckResult, extra string, skipNotification bool) {
	if !isMonitorActive(model.SourceTypeKafkaRebalanceMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name)
		return
	}

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	namespace := rule.Namespace
	if namespace == "" {
		namespace = "default"
	}
	statusText := "告警中"
	if alertState == model.AlertStateRecovered {
		statusText = "已恢复"
	}

	lines := []string{
		fmt.Sprintf("事件类型：%s", eventName),
		fmt.Sprintf("规则名称：%s", rule.Name),
		fmt.Sprintf("Kafka 实例：%s", rule.KafkaInstance.Name),
		fmt.Sprintf("消费组：%s", rule.ConsumerGroup),
	}
	if rule.Topic != "" {
		lines = append(lines, fmt.Sprintf("Topic：%s", rule.Topic))
	}
	lines = append(lines,
		fmt.Sprintf("当前状态：%s", KafkaRebalanceStateText(result.State)),
		fmt.Sprintf("检测详情：%s", result.Message),
		fmt.Sprintf("自动重启：%s", formatBoolText(rule.AutoRestart)),
		fmt.Sprintf("检查时间：%s", nowStr),
		fmt.Sprintf("状态：%s", statusText),
	)
	if extra != "" {
		lines = append(lines, fmt.Sprintf("说明：%s", extra))
	}
	if rule.AlertNote != "" {
		lines = append(lines, fmt.Sprintf("告警备注：%s", rule.AlertNote))
	}
	content := strings.Join(lines, "\n")

	webhookName := ""
	webhookType := ""
	if rule.Webhook != nil {
		webhookName = rule.Webhook.Name
		webhookType = rule.Webhook.Type
		if !skipNotification {
			title := GetAlertEngine().getAlertTitle()
			data := map[string]interface{}{
				"event_type":     "kafka_rebalance_monitor_alert",
				"event_name":     eventName,
				"rule_name":      rule.Name,
				"kafka_instance": rule.KafkaInstance.Name,
				"consumer_group": rule.ConsumerGroup,
				"topic":          rule.Topic,
				"state":          result.State,
				"state_text":     KafkaRebalanceStateText(result.State),
				"message":        result.Message,
				"check_time":     nowStr,
				"alert_status":   alertState,
			}
			if err := SendNotification(rule.Webhook, title, content, data); err != nil {
				log.Printf("[KafkaRebalanceMonitorScheduler] 告警发送失败: %v", err)
			} else {
				log.Printf("[KafkaRebalanceMonitorScheduler] 告警发送成功: %s [%s]", rule.Name, eventName)
			}
		} else {
			log.Printf("[KafkaRebalanceMonitorScheduler] 规则 %s 的事件 %s 未开启通知或被降级，跳过发送，但仍写入历史", rule.Name, eventName)
		}
	} else {
		log.Printf("[KafkaRebalanceMonitorScheduler] 规则 %s 未配置通知渠道，跳过发送，但仍写入历史", rule.Name)
	}

	SaveAlertHistory(model.SourceTypeKafkaRebalanceMonitor, rule.ID, rule.Name, alertState, content, webhookName, webhookType)
}

func (s *KafkaRebalanceMonitorScheduler) waitSeconds(seconds int) bool {
	if seconds <= 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()

	select {
	case <-s.stopChan:
		return false
	case <-timer.C:
		return true
	}
}

func formatBoolText(v bool) string {
	if v {
		return "是"
	}
	return "否"
}
