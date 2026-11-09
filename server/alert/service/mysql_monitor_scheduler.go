package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// decryptMySQLInstance 解密实例密码。
//
// 密码在 handler 里以 crypto.Encrypt 存库，用的时候必须解密——否则会拿着密文去连
// MySQL，认证必然失败，连通性检查报「0/N 节点可达」的假告警（而 exporter 那条路
// 是通的，因为 sd_handler 解了密，于是 mysql_up=1 与告警 Firing 同时成立，极具迷惑性）。
// crypto.Decrypt 对明文/空串安全返回原值，故重复调用无害。
func decryptMySQLInstance(inst *model.MySQLInstance) {
	if dec, err := crypto.Decrypt(inst.Password); err == nil {
		inst.Password = dec
	}
}

type MySQLMonitorScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time
	lastAlertSentCache map[string]time.Time
	runningRules       map[string]bool
}

var ErrMySQLMonitorRuleRunning = errors.New("mysql monitor rule is already running")

var (
	globalMySQLMonitorScheduler *MySQLMonitorScheduler
	mysqlMonitorSchedulerOnce   sync.Once
)

func NewMySQLMonitorScheduler() *MySQLMonitorScheduler {
	return &MySQLMonitorScheduler{
		stopChan:           make(chan struct{}),
		lastRunCache:       make(map[string]time.Time),
		lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypeMySQLMonitor),
		runningRules:       make(map[string]bool),
	}
}

func GetMySQLMonitorScheduler() *MySQLMonitorScheduler {
	mysqlMonitorSchedulerOnce.Do(func() {
		globalMySQLMonitorScheduler = NewMySQLMonitorScheduler()
	})
	return globalMySQLMonitorScheduler
}

func (s *MySQLMonitorScheduler) Start() {
	log.Println("启动 MySQL 监控调度器...")
	s.wg.Add(1)
	go s.dispatcher()
}

// Stop 停止调度器（多次调用安全）
func (s *MySQLMonitorScheduler) Stop() {
	log.Println("停止 MySQL 监控调度器...")
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

func (s *MySQLMonitorScheduler) dispatcher() {
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

func (s *MySQLMonitorScheduler) dispatchRules() {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "mysqlRule", "status": model.TaskStatusActive})
	if err != nil {
		log.Printf("[MySQLMonitor] 查询规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.MySQLMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[MySQLMonitor] 解析规则失败: %v", err)
		return
	}

	// 批量加载 MySQLInstance 和 Webhook，注入到规则中
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
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[MySQLMonitor] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
				}
				s.mu.Lock()
				delete(s.runningRules, ruleCopy.ID)
				s.mu.Unlock()
			}()
			s.CheckRule(&ruleCopy)
		}()
	}
}

// injectAssociations 批量查询并注入 MySQLInstance 和 Webhook 关联数据
func (s *MySQLMonitorScheduler) injectAssociations(ctx context.Context, rules []model.MySQLMonitorRule) {
	instIDs := make(map[string]struct{})
	wbIDs := make(map[string]struct{})
	for _, r := range rules {
		if r.MySQLInstanceID != "" {
			instIDs[r.MySQLInstanceID] = struct{}{}
		}
		if r.WebhookID != "" {
			wbIDs[r.WebhookID] = struct{}{}
		}
	}

	// 查询 MySQLInstance
	instMap := make(map[string]model.MySQLInstance)
	if len(instIDs) > 0 {
		ids := make([]string, 0, len(instIDs))
		for id := range instIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "mysqlInstance"})
		if err == nil {
			defer cur.Close(ctx)
			var insts []model.MySQLInstance
			if cur.All(ctx, &insts) == nil {
				for _, inst := range insts {
					decryptMySQLInstance(&inst)
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
		if inst, ok := instMap[rules[i].MySQLInstanceID]; ok {
			rules[i].MySQLInstance = inst
		}
		if rules[i].WebhookID != "" {
			if wb, ok := wbMap[rules[i].WebhookID]; ok {
				rules[i].Webhook = &wb
			}
		}
	}
}

func (s *MySQLMonitorScheduler) TryCheckRule(rule *model.MySQLMonitorRule) error {
	s.mu.Lock()
	if s.runningRules[rule.ID] {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrMySQLMonitorRuleRunning, rule.ID)
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

func (s *MySQLMonitorScheduler) CheckRule(rule *model.MySQLMonitorRule) error {
	return s.checkRule(rule, false)
}

func (s *MySQLMonitorScheduler) checkRule(rule *model.MySQLMonitorRule, manualRefresh bool) error {
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeMySQLMonitor, rule.ID)) {
		logSkipPaused(model.SourceTypeMySQLMonitor, rule.ID, rule.Name)
		return nil
	}

	// 如果 MySQLInstance/Webhook 未注入（手动触发路径），按需查询
	if rule.MySQLInstanceID != "" && rule.MySQLInstance.ID == "" {
		ctx := context.TODO()
		result := database.Config().FindOne(ctx, bson.M{"_id": rule.MySQLInstanceID, "kind": "mysqlInstance"})
		if result.Err() == nil {
			var inst model.MySQLInstance
			if result.Decode(&inst) == nil {
				decryptMySQLInstance(&inst)
				rule.MySQLInstance = inst
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

	timeout := time.Duration(rule.Interval/2) * time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := NewMySQLClient(&rule.MySQLInstance)
	monitorType := resolveMySQLMonitorType(rule.MonitorType, rule.MySQLInstance.Architecture)
	triggered, value, result := client.Check(ctx, monitorType, rule.ReplicationLagThreshold, rule.MGRQueueThreshold)
	detailJSON, _ := json.Marshal(result)
	rule.LastValue = value
	rule.LastDetail = string(detailJSON)
	if !manualRefresh && !isMonitorActive(model.SourceTypeMySQLMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMySQLMonitor, rule.ID, rule.Name)
		return nil
	}
	silences, err := LoadActiveSilences(model.SourceTypeMySQLMonitor, rule.ID)
	if err != nil {
		log.Printf("[MySQLMonitor] 查询规则 %s 静默配置失败: %v", rule.Name, err)
		return err
	}
	snapshots, firingKeys := buildMySQLInstanceSnapshots(rule, monitorType, result, triggered)
	if err := UpdateMonitorInstanceStates(SuppressedHistoryMeta{
		SourceType:     model.SourceTypeMySQLMonitor,
		SourceID:       rule.ID,
		SourceName:     rule.Name,
		Duration:       rule.Duration,
		Interval:       rule.Interval,
		RemindInterval: rule.RemindInterval,
	}, snapshots, silences, !manualRefresh); err != nil {
		log.Printf("[MySQLMonitor] 更新规则 %s 节点明细失败: %v", rule.Name, err)
		return err
	}
	effectiveTriggered := triggered
	if len(firingKeys) > 0 {
		effectiveTriggered = hasUnmutedFiringKey(firingKeys, silences)
	}
	if manualRefresh && !isMonitorActive(model.SourceTypeMySQLMonitor, rule.ID) {
		snapshotState := model.AlertStateOK
		if effectiveTriggered {
			snapshotState = model.AlertStateFiring
		}
		dbCtx := context.TODO()
		if _, dbErr := database.Config().UpdateOne(dbCtx,
			bson.M{"_id": rule.ID, "kind": "mysqlRule"},
			bson.M{"$set": bson.M{
				"alertState":  snapshotState,
				"firingAt":    nil,
				"recoveredAt": nil,
				"lastValue":   rule.LastValue,
				"lastDetail":  rule.LastDetail,
			}},
		); dbErr != nil {
			log.Printf("[MySQLMonitor] 更新暂停规则刷新结果失败: %v", dbErr)
		}
		return nil
	}
	if len(firingKeys) > 0 && !effectiveTriggered {
		s.resetFullySilencedRule(rule)
		return nil
	}
	s.processStateMachine(rule, effectiveTriggered, result.Summary)
	return nil
}

func (s *MySQLMonitorScheduler) processStateMachine(rule *model.MySQLMonitorRule, triggered bool, summary string) {
	if !isMonitorActive(model.SourceTypeMySQLMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMySQLMonitor, rule.ID, rule.Name)
		return
	}
	duration := rule.Duration
	if duration < rule.Interval {
		duration = rule.Interval
	}
	recoveryDuration := rule.RecoveryDuration
	if recoveryDuration < rule.Interval {
		recoveryDuration = rule.Interval
	}

	now := time.Now()
	previousState := rule.AlertState

	if triggered {
		rule.RecoveredAt = nil
		if rule.FiringAt == nil {
			rule.FiringAt = &now
		}
		if now.Sub(*rule.FiringAt).Seconds() >= float64(duration) {
			if previousState != model.AlertStateFiring {
				rule.AlertState = model.AlertStateFiring
				s.sendAlert(rule, model.AlertStateFiring, summary)
				s.mu.Lock()
				s.lastAlertSentCache[rule.ID] = now
				s.mu.Unlock()
			} else {
				rule.AlertState = model.AlertStateFiring
				if rule.RepeatAlert {
					s.sendAlert(rule, model.AlertStateFiring, summary)
				} else if rule.RemindInterval > 0 {
					s.mu.Lock()
					lastSent, exists := s.lastAlertSentCache[rule.ID]
					s.mu.Unlock()
					if !exists || now.Sub(lastSent).Seconds() >= float64(rule.RemindInterval) {
						s.sendAlert(rule, model.AlertStateFiring, summary)
						s.mu.Lock()
						s.lastAlertSentCache[rule.ID] = now
						s.mu.Unlock()
					}
				}
			}
		} else if previousState != model.AlertStateFiring {
			rule.AlertState = model.AlertStatePending
		}
	} else {
		rule.FiringAt = nil
		if previousState == model.AlertStateFiring || previousState == model.AlertStatePending {
			if rule.RecoveredAt == nil {
				rule.RecoveredAt = &now
			}
			if now.Sub(*rule.RecoveredAt).Seconds() >= float64(recoveryDuration) {
				if previousState == model.AlertStateFiring {
					rule.AlertState = model.AlertStateRecovered
					s.sendAlert(rule, model.AlertStateRecovered, summary)
				} else {
					rule.AlertState = model.AlertStateOK
				}
				s.mu.Lock()
				delete(s.lastAlertSentCache, rule.ID)
				s.mu.Unlock()
				rule.RecoveredAt = nil
			}
		} else {
			rule.AlertState = model.AlertStateOK
			rule.RecoveredAt = nil
		}
	}

	ctx := context.TODO()
	if _, dbErr := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "mysqlRule", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState":  rule.AlertState,
			"firingAt":    rule.FiringAt,
			"recoveredAt": rule.RecoveredAt,
			"lastValue":   rule.LastValue,
			"lastDetail":  rule.LastDetail,
		}},
	); dbErr != nil {
		log.Printf("[MySQLMonitor] 更新规则状态失败: %v", dbErr)
	}
}

func (s *MySQLMonitorScheduler) sendAlert(rule *model.MySQLMonitorRule, state, summary string) {
	if !isMonitorActive(model.SourceTypeMySQLMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeMySQLMonitor, rule.ID, rule.Name)
		return
	}
	statusText := "告警中"
	if state == model.AlertStateRecovered {
		statusText = "已恢复"
	}
	monitorType := resolveMySQLMonitorType(rule.MonitorType, rule.MySQLInstance.Architecture)
	content := fmt.Sprintf("任务名称：%s\nMySQL 数据源：%s\n架构模式：%s\n检查摘要：%s\n检查时间：%s\n状态：%s",
		rule.Name,
		rule.MySQLInstance.Name,
		mysqlArchitectureLabel(rule.MySQLInstance.Architecture),
		summary,
		time.Now().Format("2006-01-02 15:04:05"),
		statusText,
	)
	if monitorType == model.MySQLMonitorTypeReplicationLag {
		content += fmt.Sprintf("\n主从延迟阈值：%d 秒", rule.ReplicationLagThreshold)
	}
	if monitorType == model.MySQLMonitorTypeMGRSyncStatus {
		content += fmt.Sprintf("\nMGR 队列阈值：%d", rule.MGRQueueThreshold)
	}
	if rule.AlertNote != "" {
		content += fmt.Sprintf("\n告警备注：%s", rule.AlertNote)
	}

	data := map[string]interface{}{
		"event_type":   "mysql_monitor_alert",
		"task_name":    rule.Name,
		"source_name":  rule.MySQLInstance.Name,
		"architecture": rule.MySQLInstance.Architecture,
		"monitor_type": monitorType,
		"summary":      summary,
		"alert_status": state,
	}

	webhookName := ""
	webhookType := ""
	if rule.Webhook != nil {
		webhookName = rule.Webhook.Name
		webhookType = rule.Webhook.Type
		if err := SendNotification(rule.Webhook, GetAlertEngine().getAlertTitle(), content, data); err != nil {
			log.Printf("[MySQLMonitor] 告警发送失败: %v", err)
		}
	}
	SaveAlertHistory(model.SourceTypeMySQLMonitor, rule.ID, rule.Name, state, content, webhookName, webhookType)
}

func (s *MySQLMonitorScheduler) resetFullySilencedRule(rule *model.MySQLMonitorRule) {
	s.mu.Lock()
	delete(s.lastAlertSentCache, rule.ID)
	s.mu.Unlock()
	ctx := context.TODO()
	if _, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "mysqlRule", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
			"lastValue":   rule.LastValue,
			"lastDetail":  rule.LastDetail,
		}},
	); err != nil {
		log.Printf("[MySQLMonitor] 重置全静默规则状态失败: %v", err)
	}
	rule.AlertState = model.AlertStateOK
	rule.FiringAt = nil
	rule.RecoveredAt = nil
}

func buildMySQLInstanceSnapshots(rule *model.MySQLMonitorRule, monitorType string, result MySQLCheckResult, triggered bool) ([]MonitorInstanceSnapshot, []string) {
	snapshots := make([]MonitorInstanceSnapshot, 0, len(result.Nodes))
	firingKeys := make([]string, 0)
	for _, node := range result.Nodes {
		scopeKey := mysqlNodeScopeKey(node)
		if scopeKey == "" {
			continue
		}
		value := mysqlNodeValue(monitorType, node)
		firing := mysqlNodeFiring(rule, monitorType, node)
		state := model.AlertStateOK
		if firing {
			state = model.AlertStateFiring
			firingKeys = append(firingKeys, scopeKey)
		}
		detail, _ := json.Marshal(node)
		displayName := scopeKey
		if node.Hostname != "" && node.Hostname != scopeKey {
			displayName = fmt.Sprintf("%s (%s)", node.Hostname, scopeKey)
		}
		snapshots = append(snapshots, MonitorInstanceSnapshot{
			ScopeKey:    scopeKey,
			DisplayName: displayName,
			AlertState:  state,
			LastValue:   value,
			LastDetail:  string(detail),
		})
	}
	if triggered && len(firingKeys) == 0 {
		scopeKey, displayName := mysqlAggregateScope(monitorType)
		snapshots = append(snapshots, MonitorInstanceSnapshot{
			ScopeKey:    scopeKey,
			DisplayName: displayName,
			AlertState:  model.AlertStateFiring,
			LastValue:   rule.LastValue,
			LastDetail:  result.Summary,
		})
		firingKeys = append(firingKeys, scopeKey)
	}
	return snapshots, firingKeys
}

func mysqlAggregateScope(monitorType string) (string, string) {
	switch monitorType {
	case model.MySQLMonitorTypeReplicationLag, model.MySQLMonitorTypeReplicationStatus:
		return monitorType + ":topology", "主从拓扑检查"
	case model.MySQLMonitorTypeMGRNodeStatus:
		return monitorType + ":topology", "MGR 拓扑检查"
	case model.MySQLMonitorTypeMGRSyncStatus:
		return monitorType + ":queue-data", "MGR 队列数据检查"
	default:
		return monitorType + ":summary", "监控检查汇总"
	}
}

func mysqlNodeScopeKey(node MySQLNodeCheck) string {
	if node.Address != "" {
		return node.Address
	}
	if node.Hostname != "" && node.Port > 0 {
		return fmt.Sprintf("%s:%d", node.Hostname, node.Port)
	}
	return node.Hostname
}

func mysqlNodeValue(monitorType string, node MySQLNodeCheck) float64 {
	switch monitorType {
	case model.MySQLMonitorTypeReplicationLag:
		if node.ReplicationLag != nil {
			return float64(*node.ReplicationLag)
		}
	case model.MySQLMonitorTypeMGRSyncStatus:
		if node.MGRTransactionsQ > node.MGRApplierQueue {
			return node.MGRTransactionsQ
		}
		return node.MGRApplierQueue
	default:
		if node.Error != "" || !node.Reachable {
			return 0
		}
		return 1
	}
	return 0
}

func mysqlNodeFiring(rule *model.MySQLMonitorRule, monitorType string, node MySQLNodeCheck) bool {
	if node.Error != "" || !node.Reachable {
		return true
	}
	switch monitorType {
	case model.MySQLMonitorTypeReplicationLag:
		if node.Role != "REPLICA" {
			return false
		}
		return node.ReplicationLag == nil || *node.ReplicationLag > int64(rule.ReplicationLagThreshold)
	case model.MySQLMonitorTypeReplicationStatus:
		if node.Role != "REPLICA" {
			return false
		}
		return !isReplicationRunning(node.ReplicationIORun) || !isReplicationRunning(node.ReplicationSQLRun)
	case model.MySQLMonitorTypeMGRNodeStatus:
		return node.MGRMemberState != "" && node.MGRMemberState != "ONLINE" ||
			node.MGRMemberRole != "" && node.MGRMemberRole != "PRIMARY" && node.MGRMemberRole != "SECONDARY"
	case model.MySQLMonitorTypeMGRSyncStatus:
		return rule.MGRQueueThreshold > 0 &&
			(node.MGRTransactionsQ > float64(rule.MGRQueueThreshold) || node.MGRApplierQueue > float64(rule.MGRQueueThreshold))
	default:
		return false
	}
}

func hasUnmutedFiringKey(firingKeys []string, silences map[string]model.AlertSilence) bool {
	for _, key := range firingKeys {
		if _, muted := silences[key]; !muted {
			return true
		}
	}
	return false
}

func resolveMySQLMonitorType(monitorType, architecture string) string {
	if monitorType == "" || monitorType == model.MySQLMonitorTypeHealth || monitorType == model.MySQLMonitorTypeMGRStatus {
		switch architecture {
		case model.MySQLArchitectureReplication:
			return model.MySQLMonitorTypeReplicationLag
		case model.MySQLArchitectureMGR:
			return model.MySQLMonitorTypeMGRNodeStatus
		default:
			return model.MySQLMonitorTypeConnectivity
		}
	}
	return monitorType
}

func mysqlArchitectureLabel(value string) string {
	switch value {
	case model.MySQLArchitectureReplication:
		return "主从复制"
	case model.MySQLArchitectureMGR:
		return "MGR"
	default:
		return "单节点"
	}
}

func StartMySQLMonitorScheduler() {
	GetMySQLMonitorScheduler().Start()
}

func StopMySQLMonitorScheduler() {
	if globalMySQLMonitorScheduler != nil {
		globalMySQLMonitorScheduler.Stop()
	}
}
