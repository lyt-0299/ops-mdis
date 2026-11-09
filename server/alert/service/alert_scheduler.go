package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// AlertScheduler 告警规则调度器
type AlertScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time // 记录每条规则的上次执行时间
	lastFiringCache    map[string][]string  // 记录每条规则上次告警时触发的实例标识
	lastAlertSentCache map[string]time.Time // 记录每条规则上次发送告警通知的时间（用于提醒间隔）
	runningRules       map[string]bool      // 防止同一规则的 CheckRule 重叠执行
}

var (
	globalAlertScheduler *AlertScheduler
	alertSchedulerOnce   sync.Once
)

// NewAlertScheduler 创建告警调度器
func NewAlertScheduler() *AlertScheduler {
	return &AlertScheduler{
		stopChan:           make(chan struct{}),
		lastRunCache:       make(map[string]time.Time),
		lastFiringCache:    make(map[string][]string),
		lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypePrometheus),
		runningRules:       make(map[string]bool),
	}
}

// GetAlertScheduler 获取全局告警调度器实例
func GetAlertScheduler() *AlertScheduler {
	alertSchedulerOnce.Do(func() {
		globalAlertScheduler = NewAlertScheduler()
	})
	return globalAlertScheduler
}

// Start 启动告警调度器
func (s *AlertScheduler) Start() {
	log.Println("启动 Prometheus 告警调度器...")

	s.wg.Add(1)
	go s.dispatcher()
}

// Stop 停止告警调度器（多次调用安全）
func (s *AlertScheduler) Stop() {
	log.Println("停止 Prometheus 告警调度器...")
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

// dispatcher 调度器主循环
func (s *AlertScheduler) dispatcher() {
	defer s.wg.Done()

	// 每 5 秒扫描一次规则
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

// dispatchRules 分发需要执行的告警规则
func (s *AlertScheduler) dispatchRules() {
	ctx := context.TODO()

	// 查询所有 active 的 alertRule
	cur, err := database.Config().Find(ctx,
		bson.M{"kind": "alertRule", "status": model.TaskStatusActive},
	)
	if err != nil {
		log.Printf("[AlertScheduler] 查询告警规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.AlertRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[AlertScheduler] 解析告警规则失败: %v", err)
		return
	}

	// 批量加载 DataSource 和 Webhook，按需注入到规则中
	s.injectRuleAssociations(ctx, rules)

	now := time.Now()
	for i := range rules {
		rule := &rules[i]

		// 检查是否到达执行间隔
		s.mu.Lock()
		lastRun, exists := s.lastRunCache[rule.ID]
		shouldRun := !exists || now.Sub(lastRun).Seconds() >= float64(rule.Interval)

		if shouldRun {
			// 防止同一规则重叠执行
			if s.runningRules[rule.ID] {
				s.mu.Unlock()
				continue
			}
			s.runningRules[rule.ID] = true
			s.lastRunCache[rule.ID] = now
		}
		s.mu.Unlock()

		if shouldRun {
			// 异步执行检查，值拷贝避免并发竞争
			ruleCopy := *rule
			if rule.FiringAt != nil {
				t := *rule.FiringAt
				ruleCopy.FiringAt = &t
			}
			if rule.RecoveredAt != nil {
				t := *rule.RecoveredAt
				ruleCopy.RecoveredAt = &t
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[AlertScheduler] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
					}
					s.mu.Lock()
					delete(s.runningRules, ruleCopy.ID)
					s.mu.Unlock()
				}()
				s.CheckRule(&ruleCopy)
			}()
		}
	}
}

// injectRuleAssociations 批量加载并注入 DataSource / Webhook 关联数据。
// 在 mongo 设计中，alertRule 文档只存 dataSourceId / webhookId，
// 调度器负责 JOIN 查询并注入运行时字段。
func (s *AlertScheduler) injectRuleAssociations(ctx context.Context, rules []model.AlertRule) {
	// 收集需要查询的 ID 集合
	dsIDs := make(map[string]struct{})
	wbIDs := make(map[string]struct{})
	for _, r := range rules {
		if r.DataSourceID != "" {
			dsIDs[r.DataSourceID] = struct{}{}
		}
		if r.WebhookID != "" {
			wbIDs[r.WebhookID] = struct{}{}
		}
	}

	// 查询 DataSource
	dsMap := make(map[string]model.DataSource)
	if len(dsIDs) > 0 {
		ids := make([]string, 0, len(dsIDs))
		for id := range dsIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "source"})
		if err == nil {
			defer cur.Close(ctx)
			var dss []model.DataSource
			if cur.All(ctx, &dss) == nil {
				for _, ds := range dss {
					dsMap[ds.ID] = ds
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

	// 注入到规则（rules 是值切片，需要按索引修改）
	for i := range rules {
		if ds, ok := dsMap[rules[i].DataSourceID]; ok {
			rules[i].DataSource = ds
		}
		if rules[i].WebhookID != "" {
			if wb, ok := wbMap[rules[i].WebhookID]; ok {
				rules[i].Webhook = &wb
			}
		}
	}
}

// ErrAlertRuleRunning 该规则的上一次检查还没跑完（与定时周期撞上了），可稍后重试。
var ErrAlertRuleRunning = errors.New("alert rule is already running")

// ErrAlertRuleDataSourceMissing 规则引用的数据源已不存在或地址为空——多见于数据源被删掉、
// 而引用它的规则还留着。与「正在检查中」是两回事，必须分开报，否则用户会一直重试。
var ErrAlertRuleDataSourceMissing = errors.New("alert rule datasource missing or has empty url")

// TryCheckRule 手动触发单条规则检查，与调度器共享 runningRules 互斥保护。
// 若该规则已在执行中则返回 ErrRuleRunning，避免与定时周期并发重叠。
func (s *AlertScheduler) TryCheckRule(rule *model.AlertRule) error {
	// 注入关联后再检查。定时路径在 :107 统一做过 injectRuleAssociations，而手动触发
	// （UI「立即检查」）由 api 层直接把库里读出的规则递进来，DataSource / Webhook 两个
	// `bson:"-"` 字段都是零值：前者让 NewPrometheusClient 拿到空 URL、请求退化成相对
	// 路径，报 `unsupported protocol scheme ""`；后者让查询即使成功也发不出通知。
	// 放在入口而不是让每个调用方自己注入——api 层调不到这个私有方法，且历史上正是
	// 「调用方要记得注入」导致这里漏了。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rules := []model.AlertRule{*rule}
	s.injectRuleAssociations(ctx, rules)
	cancel()
	*rule = rules[0]

	// 注入不到就明确报错，不要带着空 URL 去发请求换一个看不懂的传输层错误
	if rule.DataSourceID != "" && rule.DataSource.URL == "" {
		return ErrAlertRuleDataSourceMissing
	}

	s.mu.Lock()
	if s.runningRules[rule.ID] {
		s.mu.Unlock()
		return ErrAlertRuleRunning
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

// CheckRule 检查单条告警规则
func (s *AlertScheduler) CheckRule(rule *model.AlertRule) error {
	return s.checkRule(rule, false)
}

func (s *AlertScheduler) checkRule(rule *model.AlertRule, manualRefresh bool) error {
	ctx := context.TODO()
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypePrometheus, rule.ID)) {
		logSkipPaused(model.SourceTypePrometheus, rule.ID, rule.Name)
		return nil
	}

	// 创建 Prometheus 客户端
	client := NewPrometheusClient(&rule.DataSource)

	// 执行 PromQL 查询，获取所有序列
	results, err := client.GetVectorValues(rule.PromQL)
	if err != nil {
		log.Printf("[AlertScheduler] 规则 %s 查询失败: %v", rule.Name, err)
		return fmt.Errorf("查询失败: %v", err)
	}

	if len(results) == 0 {
		if err := UpdatePrometheusInstanceStates(rule, nil, nil, nil, false); err != nil {
			log.Printf("[AlertScheduler] 清空规则 %s 实例明细失败: %v", rule.Name, err)
			return fmt.Errorf("清空实例明细失败: %v", err)
		}
		return nil
	}
	if !manualRefresh && !isMonitorActive(model.SourceTypePrometheus, rule.ID) {
		logSkipPaused(model.SourceTypePrometheus, rule.ID, rule.Name)
		return nil
	}

	// 找出所有触发的序列并计算全局最大值
	var firingResults []VectorResult
	var maxValue float64
	firingByScopeKey := make(map[string]bool)
	if len(results) > 0 {
		maxValue = results[0].Value
		for _, res := range results {
			if s.evaluateCondition(res.Value, rule.Operator, rule.Threshold) {
				firingResults = append(firingResults, res)
				firingByScopeKey[prometheusScopeKey(res.Metric)] = true
			}
			if res.Value > maxValue {
				maxValue = res.Value
			}
		}
	}

	silences, err := LoadActiveSilences(model.SourceTypePrometheus, rule.ID)
	if err != nil {
		log.Printf("[AlertScheduler] 查询规则 %s 静默配置失败: %v", rule.Name, err)
		return fmt.Errorf("查询静默配置失败: %v", err)
	}
	if err := UpdatePrometheusInstanceStates(rule, results, firingByScopeKey, silences, !manualRefresh); err != nil {
		log.Printf("[AlertScheduler] 更新规则 %s 实例明细失败: %v", rule.Name, err)
		return fmt.Errorf("更新实例明细失败: %v", err)
	}

	unmutedFiringResults := make([]VectorResult, 0, len(firingResults))
	unmutedFiringScopeKeys := make([]string, 0, len(firingResults))
	for _, res := range firingResults {
		scopeKey := prometheusScopeKey(res.Metric)
		if _, muted := silences[scopeKey]; muted {
			continue
		}
		unmutedFiringResults = append(unmutedFiringResults, res)
		unmutedFiringScopeKeys = append(unmutedFiringScopeKeys, scopeKey)
	}
	firingResults = unmutedFiringResults

	// 语义校验：持续时间不能低于检查步长
	duration := rule.Duration
	if duration < rule.Interval {
		duration = rule.Interval
	}

	triggered := len(firingResults) > 0
	previousState := rule.AlertState
	rule.LastValue = maxValue // 始终显示所有实例中的最大值

	if triggered && rule.FiringAt == nil {
		inheritedFiringAt, err := loadEarliestPrometheusInstanceFiringAt(ctx, rule.ID, unmutedFiringScopeKeys)
		if err != nil {
			log.Printf("[AlertScheduler] 继承规则 %s 实例告警开始时间失败: %v", rule.Name, err)
		} else if inheritedFiringAt != nil {
			rule.FiringAt = inheritedFiringAt
		}
	}

	if manualRefresh && !isMonitorActive(model.SourceTypePrometheus, rule.ID) {
		snapshotState := model.AlertStateOK
		if triggered {
			snapshotState = model.AlertStateFiring
		}
		_, err := database.Config().UpdateOne(ctx,
			bson.M{"_id": rule.ID, "kind": "alertRule"},
			bson.M{"$set": bson.M{
				"alertState":  snapshotState,
				"lastValue":   rule.LastValue,
				"firingAt":    nil,
				"recoveredAt": nil,
			}},
		)
		if err != nil {
			log.Printf("[AlertScheduler] 更新暂停规则刷新结果失败: %v", err)
			return fmt.Errorf("更新刷新结果失败: %v", err)
		}
		return nil
	}

	now := time.Now()

	if triggered {
		// 有实例超过阈值：重置恢复计时器
		rule.RecoveredAt = nil

		if rule.FiringAt == nil {
			rule.FiringAt = &now
		}

		durationMet := now.Sub(*rule.FiringAt).Seconds() >= float64(duration)
		if durationMet {
			if previousState != "Firing" {
				rule.AlertState = "Firing"

				// 缓存当前触发的实例标识，供恢复时使用
				var firingIDs []string
				for _, res := range firingResults {
					firingIDs = append(firingIDs, s.getIdentifier(res.Metric))
				}
				s.mu.Lock()
				s.lastFiringCache[rule.ID] = firingIDs
				s.lastAlertSentCache[rule.ID] = now
				s.mu.Unlock()

				s.sendAlert(rule, results, "Firing", nil)
			} else {
				// 保持 Firing 状态，但更新缓存的触发实例列表
				var firingIDs []string
				for _, res := range firingResults {
					firingIDs = append(firingIDs, s.getIdentifier(res.Metric))
				}
				s.mu.Lock()
				s.lastFiringCache[rule.ID] = firingIDs
				s.mu.Unlock()

				rule.AlertState = "Firing"

				if rule.RepeatAlert {
					// 持续告警：每次都发送（优先级最高）
					log.Printf("[AlertScheduler] 规则 %s 持续告警触发", rule.Name)
					s.sendAlert(rule, results, "Firing", nil)
				} else if rule.RemindInterval > 0 {
					// 提醒间隔：未开启持续告警时，按间隔重复提醒
					s.mu.Lock()
					lastSent, exists := s.lastAlertSentCache[rule.ID]
					s.mu.Unlock()
					if !exists || now.Sub(lastSent).Seconds() >= float64(rule.RemindInterval) {
						log.Printf("[AlertScheduler] 规则 %s 提醒间隔告警触发", rule.Name)
						s.sendAlert(rule, results, "Firing", nil)
						s.mu.Lock()
						s.lastAlertSentCache[rule.ID] = now
						s.mu.Unlock()
					}
				}
			}
		} else {
			// 触发但未满足持续时间，标记为 Pending
			if previousState != "Firing" {
				rule.AlertState = "Pending"
			} else {
				rule.AlertState = "Firing"
			}
		}
	} else {
		// 没有实例超过阈值：重置告警计时器
		rule.FiringAt = nil

		if previousState == "Firing" || previousState == "Pending" {
			// 恢复持续时间判定
			recoveryDuration := rule.RecoveryDuration
			if recoveryDuration <= 0 {
				// 如果没有设置恢复持续时间，立即恢复
				if previousState == "Firing" {
					rule.AlertState = "Recovered"
					rule.RecoveredAt = nil

					s.mu.Lock()
					previousFiringIDs := s.lastFiringCache[rule.ID]
					delete(s.lastFiringCache, rule.ID)
					delete(s.lastAlertSentCache, rule.ID)
					s.mu.Unlock()

					s.sendAlert(rule, results, "Recovered", previousFiringIDs)
				} else {
					// 从 Pending 恢复，直接变 OK，无需通知
					rule.AlertState = "OK"
					rule.RecoveredAt = nil
				}
			} else {
				// 有恢复持续时间要求，启动恢复计时器
				if rule.RecoveredAt == nil {
					rule.RecoveredAt = &now
				}

				recoveryMet := now.Sub(*rule.RecoveredAt).Seconds() >= float64(recoveryDuration)
				if recoveryMet {
					// 恢复持续时间已满足
					if previousState == "Firing" {
						rule.AlertState = "Recovered"
						rule.RecoveredAt = nil

						s.mu.Lock()
						previousFiringIDs := s.lastFiringCache[rule.ID]
						delete(s.lastFiringCache, rule.ID)
						delete(s.lastAlertSentCache, rule.ID)
						s.mu.Unlock()

						s.sendAlert(rule, results, "Recovered", previousFiringIDs)
					} else {
						// 从 Pending 恢复
						rule.AlertState = "OK"
						rule.RecoveredAt = nil
					}
				}
				// else: 恢复中但未满足持续时间，保持原有状态不变
			}
		} else {
			rule.AlertState = "OK"
			rule.RecoveredAt = nil
		}
	}

	// 更新数据库（仅当规则仍为 active 时）
	_, err = database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "alertRule", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState":  rule.AlertState,
			"lastValue":   rule.LastValue,
			"firingAt":    rule.FiringAt,
			"recoveredAt": rule.RecoveredAt,
		}},
	)
	if err != nil {
		log.Printf("[AlertScheduler] 更新规则状态失败: %v", err)
		return fmt.Errorf("更新状态失败: %v", err)
	}

	return nil
}

// getIdentifier 尝试从标签中提取主机/实例标识
func (s *AlertScheduler) getIdentifier(metric map[string]string) string {
	// 1. 提取资产名称标签
	name := ""
	if val, ok := metric["nodename"]; ok {
		name = val
	} else if val, ok := metric["hostname"]; ok {
		name = val
	} else if val, ok := metric["host"]; ok {
		name = val
	}

	// 2. 提取实例连接标签
	instance := metric["instance"]

	// 3. 组合逻辑
	if name != "" && instance != "" {
		return fmt.Sprintf("%s (%s)", name, instance)
	}
	if name != "" {
		return name
	}
	if instance != "" {
		return instance
	}

	// 4. 次选：K8s 容器标识（保留，MDIS 支持 k8s 监控）
	if val, ok := metric["kubernetes_pod_name"]; ok {
		return val
	}
	if val, ok := metric["pod"]; ok {
		return val
	}

	// 5. 最后回退：返回所有标签的精简字符串
	return fmt.Sprintf("%v", metric)
}

// sendAlert 发送告警通知（无论是否配置通知渠道，均写入告警历史）
// previousFiringIDs: 恢复时传入上次告警的实例标识列表，告警时传 nil
func (s *AlertScheduler) sendAlert(rule *model.AlertRule, results []VectorResult, status string, previousFiringIDs []string) {
	if !isMonitorActive(model.SourceTypePrometheus, rule.ID) {
		logSkipPaused(model.SourceTypePrometheus, rule.ID, rule.Name)
		return
	}
	var metricName, unit string
	categories := model.GetMetricCategories()
	for _, cat := range categories {
		if cat.Category == rule.Category {
			for _, tpl := range cat.Templates {
				if tpl.Type == rule.MetricType {
					metricName = tpl.Name
					unit = tpl.Unit
					break
				}
			}
		}
	}
	if metricName == "" {
		metricName = rule.MetricType
	}

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	var statusText string
	if status == "Firing" {
		statusText = "告警中"
	} else {
		statusText = "已恢复"
	}

	// 构建上次触发实例的快速查找集合
	previousFiringSet := make(map[string]bool)
	for _, id := range previousFiringIDs {
		previousFiringSet[id] = true
	}

	// 构造多实例信息
	var instanceInfo string
	var lines []string

	// 把当前的查询结果转为 Map 方便查找
	currentResultsMap := make(map[string]float64)
	for _, res := range results {
		id := s.getIdentifier(res.Metric)
		currentResultsMap[id] = res.Value
	}

	// 布尔型指标（如 up）显示为 在线/离线，而不是小数值
	isBoolMetric := rule.Category == "server" && rule.MetricType == "online"
	formatValue := func(v float64) string {
		if isBoolMetric {
			if v >= 1 {
				return "在线"
			}
			return "离线"
		}
		return fmt.Sprintf("%.2f%s", v, unit)
	}

	if status == "Firing" {
		silences, err := LoadActiveSilences(model.SourceTypePrometheus, rule.ID)
		if err != nil {
			log.Printf("[AlertScheduler] 查询规则 %s 静默配置失败: %v", rule.Name, err)
			silences = map[string]model.AlertSilence{}
		}
		// 告警时：遍历当前结果，显示所有触发项
		for _, res := range results {
			if s.evaluateCondition(res.Value, rule.Operator, rule.Threshold) {
				if _, muted := silences[prometheusScopeKey(res.Metric)]; muted {
					continue
				}
				id := s.getIdentifier(res.Metric)
				lines = append(lines, fmt.Sprintf("- %s: %s", id, formatValue(res.Value)))
			}
		}
	} else {
		// 恢复时：遍历【上次曾触发过告警】的名单，逐一交代现状
		for _, id := range previousFiringIDs {
			if val, ok := currentResultsMap[id]; ok {
				lines = append(lines, fmt.Sprintf("- %s: %s", id, formatValue(val)))
			} else {
				lines = append(lines, fmt.Sprintf("- %s: [已离线/已删除]", id))
			}
		}

		if len(previousFiringIDs) == 0 {
			lines = append(lines, "（提示：因服务重启或缓存失效，无法显示具体的恢复实例明细）")
		}
	}

	if len(lines) > 0 {
		instanceInfo = "\n告警实例明细："
		for i, line := range lines {
			if i >= 10 {
				instanceInfo += fmt.Sprintf("\n... 等共计 %d 个实例", len(lines))
				break
			}
			instanceInfo += "\n" + line
		}
	}

	// 构造告警内容
	content := fmt.Sprintf("任务名称：%s\n数据源：%s\n指标类型：%s\n运算符：%s\n阈值：%.2f%s\n检查时间：%s\n状态：%s%s",
		rule.Name,
		rule.DataSource.Name,
		metricName,
		rule.Operator,
		rule.Threshold, unit,
		nowStr,
		statusText,
		instanceInfo,
	)

	if rule.AlertNote != "" {
		content += fmt.Sprintf("\n告警备注：%s", rule.AlertNote)
	}

	alertState := model.AlertStateFiring
	if status == "Recovered" {
		alertState = model.AlertStateRecovered
	}

	webhookName := ""
	webhookType := ""
	if rule.Webhook != nil {
		webhookName = rule.Webhook.Name
		webhookType = rule.Webhook.Type
	}

	// 有通知渠道则发送消息
	if rule.Webhook != nil {
		title := GetAlertEngine().getAlertTitle()
		data := map[string]interface{}{
			"event_type":   "prometheus_alert",
			"rule_name":    rule.Name,
			"datasource":   rule.DataSource.Name,
			"metric_type":  metricName,
			"operator":     rule.Operator,
			"threshold":    rule.Threshold,
			"unit":         unit,
			"check_time":   nowStr,
			"alert_status": status,
			"instances":    results,
		}
		if err := SendNotification(rule.Webhook, title, content, data); err != nil {
			log.Printf("[AlertScheduler] 告警发送失败: %v", err)
		} else {
			log.Printf("[AlertScheduler] 告警发送成功: %s [%s]", rule.Name, status)
		}
	} else {
		log.Printf("[AlertScheduler] 规则 %s 未配置通知渠道，跳过发送，但仍写入历史", rule.Name)
	}

	// 外部通知处理完成后，写入告警历史。
	SaveAlertHistory(model.SourceTypePrometheus, rule.ID, rule.Name, alertState, content, webhookName, webhookType)
}

// floatEpsilon 浮点数比较精度阈值
const floatEpsilon = 1e-9

// floatEquals 安全比较两个浮点数是否相等
func floatEquals(a, b float64) bool {
	return math.Abs(a-b) < floatEpsilon
}

// evaluateCondition 根据运算符判断条件是否满足
func (s *AlertScheduler) evaluateCondition(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return floatEquals(value, threshold)
	case "!=":
		return !floatEquals(value, threshold)
	default:
		return false
	}
}

// loadEarliestPrometheusInstanceFiringAt 从 State 集合中查询指定规则、
// 指定 scopeKey 列表中，firingAt 最早的记录（用于继承持续时间计时器）。
//
// 对应上游 loadEarliestPrometheusInstanceFiringAt(db *gorm.DB, ...) —— GORM 版用
// sql Order("firing_at ASC").First(...)；mongo 版用 sort + limit 1。
func loadEarliestPrometheusInstanceFiringAt(ctx context.Context, ruleID string, scopeKeys []string) (*time.Time, error) {
	if len(scopeKeys) == 0 {
		return nil, nil
	}

	opts := options.FindOne().SetSort(bson.D{{Key: "firingAt", Value: 1}})
	result := database.State().FindOne(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   ruleID,
		"scopeType":  model.SilenceScopeInstance,
		"alertState": model.AlertStateFiring,
		"scopeKey":   bson.M{"$in": scopeKeys},
		"firingAt":   bson.M{"$ne": nil},
	}, opts)

	if result.Err() != nil {
		// mongo.ErrNoDocuments — no row found
		return nil, nil
	}

	var state model.AlertInstanceState
	if err := result.Decode(&state); err != nil {
		return nil, err
	}
	return state.FiringAt, nil
}

// StartAlertScheduler 启动全局告警调度器
func StartAlertScheduler() {
	scheduler := GetAlertScheduler()
	scheduler.Start()
}

// StopAlertScheduler 停止全局告警调度器
func StopAlertScheduler() {
	if globalAlertScheduler != nil {
		globalAlertScheduler.Stop()
	}
}
