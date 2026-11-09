package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// PortMonitorScheduler 端口连通性监控调度器
type PortMonitorScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time
	lastAlertSentCache map[string]time.Time // 记录每条规则上次发送告警通知的时间（用于提醒间隔）
	runningRules       map[string]bool      // 防止同一规则的 CheckRule 重叠执行
}

var (
	globalPortMonitorScheduler *PortMonitorScheduler
	portMonitorSchedulerOnce   sync.Once
)

// NewPortMonitorScheduler 创建 端口连通性监控调度器
func NewPortMonitorScheduler() *PortMonitorScheduler {
	return &PortMonitorScheduler{
		stopChan:           make(chan struct{}),
		lastRunCache:       make(map[string]time.Time),
		lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypePortMonitor),
		runningRules:       make(map[string]bool),
	}
}

// GetPortMonitorScheduler 获取全局 端口连通性监控调度器实例
func GetPortMonitorScheduler() *PortMonitorScheduler {
	portMonitorSchedulerOnce.Do(func() {
		globalPortMonitorScheduler = NewPortMonitorScheduler()
	})
	return globalPortMonitorScheduler
}

// Start 启动调度器
func (s *PortMonitorScheduler) Start() {
	log.Println("启动 端口连通性监控调度器...")
	s.wg.Add(1)
	go s.dispatcher()
}

// Stop 停止调度器（多次调用安全）
func (s *PortMonitorScheduler) Stop() {
	log.Println("停止 端口连通性监控调度器...")
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

// dispatcher 调度器主循环
func (s *PortMonitorScheduler) dispatcher() {
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

// dispatchRules 分发需要执行的检测规则
func (s *PortMonitorScheduler) dispatchRules() {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "portRule", "status": model.TaskStatusActive})
	if err != nil {
		log.Printf("[PortMonitorScheduler] 查询检测规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.PortMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[PortMonitorScheduler] 解析检测规则失败: %v", err)
		return
	}

	// 批量加载 Webhook，按需注入到规则中
	s.injectWebhooks(ctx, rules)

	now := time.Now()
	for _, rule := range rules {
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
			ruleCopy := rule
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
						log.Printf("[PortMonitorScheduler] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
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

// injectWebhooks 批量查询并注入 Webhook 关联数据
func (s *PortMonitorScheduler) injectWebhooks(ctx context.Context, rules []model.PortMonitorRule) {
	wbIDs := make(map[string]struct{})
	for _, r := range rules {
		if r.WebhookID != "" {
			wbIDs[r.WebhookID] = struct{}{}
		}
	}
	if len(wbIDs) == 0 {
		return
	}
	ids := make([]string, 0, len(wbIDs))
	for id := range wbIDs {
		ids = append(ids, id)
	}
	cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "webhook"})
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var wbs []model.Webhook
	wbMap := make(map[string]model.Webhook)
	if cur.All(ctx, &wbs) == nil {
		for _, wb := range wbs {
			wbMap[wb.ID] = wb
		}
	}
	for i := range rules {
		if rules[i].WebhookID != "" {
			if wb, ok := wbMap[rules[i].WebhookID]; ok {
				rules[i].Webhook = &wb
			}
		}
	}
}

// TryCheckRule 手动触发单条规则检查，与调度器共享 runningRules 互斥保护。
// 若该规则已在执行中则返回错误，避免与定时周期并发重叠。
func (s *PortMonitorScheduler) TryCheckRule(rule *model.PortMonitorRule) error {
	// 先注入关联再检查：定时路径统一注入过，而手动触发（UI「立即检查」）是 api 层
	// 把库里读出的规则直接递进来，通知渠道（Webhook）这类 `bson:"-"` 字段全是零值——
	// 检查即使跑通也发不出通知。放在入口，调用方不必记得注入。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rules := []model.PortMonitorRule{*rule}
	s.injectWebhooks(ctx, rules)
	cancel()
	*rule = rules[0]

	s.mu.Lock()
	if s.runningRules[rule.ID] {
		s.mu.Unlock()
		return fmt.Errorf("rule %s is already running", rule.ID)
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

// CheckRule 检查单条检测规则
func (s *PortMonitorScheduler) CheckRule(rule *model.PortMonitorRule) error {
	return s.checkRule(rule, false)
}

func (s *PortMonitorScheduler) checkRule(rule *model.PortMonitorRule, manualRefresh bool) error {
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypePortMonitor, rule.ID)) {
		logSkipPaused(model.SourceTypePortMonitor, rule.ID, rule.Name)
		return nil
	}

	// 如果 Webhook 未注入（手动触发路径），按需查询
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

	address := net.JoinHostPort(rule.Target, strconv.Itoa(rule.Port))
	timeout := time.Duration(rule.Timeout) * time.Second

	// 执行拨测
	// 注意：在存在透明代理（如 Clash、iptables REDIRECT）的环境中，
	// net.DialTimeout 可能会被代理拦截而"假成功"，随即代理发现目标不可达后
	// 立即关闭连接并返回 EOF。
	// 修复方案：Dial 成功后用剩余超时进行一次短读：
	//   - 读到 EOF（立即关闭）→ 代理拒绝，判定为不可达（isFailed = true）
	//   - 读到超时（io timeout）→ 服务端正常连接但不主动发数据，判定为可达
	//   - 读到实际数据      → 明显可达

	conn, err := net.DialTimeout("tcp", address, timeout)
	isFailed := err != nil
	if !isFailed {
		// === 透明代理抗干扰探测（Write + Read 二阶段）===
		//
		// 只读探测的缺陷：当透明代理（如 Clash）拦截连接时，它需要一段时间来
		// 尝试向上游建立连接。在这段时间内 Read 会阻塞，一旦探测超时先到，
		// 就会被误判为"健康（超时=正常服务不主动发数据）"。
		//
		// 写+读探测的原理：
		//   1. 先写入 1 字节垃圾数据，强制代理立刻决策：把这 1 字节转发到上游。
		//   2. 对真实上游：写成功，服务等待更多数据 → 300ms timeout → 健康 ✓
		//   3. 对透明代理（上游不可达）：代理转发失败 → 立刻返回 EOF → 不健康 ✓

		// Step 1: 写探测字节
		conn.SetWriteDeadline(time.Now().Add(timeout))
		_, writeErr := conn.Write([]byte{0})
		if writeErr != nil {
			conn.Close()
			isFailed = true
			err = fmt.Errorf("TCP 写入探测失败（连接可能被代理拦截）: %v", writeErr)
		} else {
			// Step 2: 读取，固定 300ms 窗口
			// - 真实服务：等待合法协议数据，不会主动响应我们的垃圾字节 → 超时 → 健康
			// - 透明代理（上游不可达）：转发失败后立刻关闭 → EOF → 不健康
			conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			buf := make([]byte, 1)
			_, readErr := conn.Read(buf)
			conn.Close()

			if readErr != nil {
				if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
					// 超时：服务端正常但不主动发数据（如 MySQL、Redis、Nginx 等），视为联通
					isFailed = false
				} else {
					// EOF 或其他错误
					isFailed = true
					if readErr == io.EOF {
						err = fmt.Errorf("TCP 连接被强行中断 (目标端口拒绝访问或被防火墙/代理拦截)")
					} else {
						err = fmt.Errorf("TCP 连接发生异常: %v", readErr)
					}
				}
			}
		}
	}

	// 语义校验
	duration := rule.Duration
	if duration < rule.Interval {
		duration = rule.Interval
	}

	triggered := isFailed
	previousState := rule.AlertState

	now := time.Now()
	if !manualRefresh && !isMonitorActive(model.SourceTypePortMonitor, rule.ID) {
		logSkipPaused(model.SourceTypePortMonitor, rule.ID, rule.Name)
		return nil
	}
	if manualRefresh && !isMonitorActive(model.SourceTypePortMonitor, rule.ID) {
		snapshotState := model.AlertStateOK
		if triggered {
			snapshotState = model.AlertStateFiring
		}
		ctx := context.TODO()
		if _, dbErr := database.Config().UpdateOne(ctx,
			bson.M{"_id": rule.ID, "kind": "portRule"},
			bson.M{"$set": bson.M{
				"alertState":  snapshotState,
				"firingAt":    nil,
				"recoveredAt": nil,
			}},
		); dbErr != nil {
			log.Printf("[PortMonitorScheduler] 更新暂停规则刷新结果失败: %v", dbErr)
			return fmt.Errorf("更新刷新结果失败: %v", dbErr)
		}
		return nil
	}

	if triggered {
		rule.RecoveredAt = nil // 重置恢复计时器

		if rule.FiringAt == nil {
			rule.FiringAt = &now
		}

		durationMet := now.Sub(*rule.FiringAt).Seconds() >= float64(duration)
		if durationMet {
			if previousState != "Firing" {
				rule.AlertState = "Firing"
				s.sendAlert(rule, "Firing", err.Error())
				// 记录首次发送时间
				s.mu.Lock()
				s.lastAlertSentCache[rule.ID] = now
				s.mu.Unlock()
			} else {
				// 保持 Firing
				rule.AlertState = "Firing"
				if rule.RepeatAlert {
					log.Printf("[PortMonitorScheduler] 规则 %s 持续告警触发", rule.Name)
					s.sendAlert(rule, "Firing", err.Error())
				} else if rule.RemindInterval > 0 {
					// 提醒间隔逻辑
					s.mu.Lock()
					lastSent, exists := s.lastAlertSentCache[rule.ID]
					s.mu.Unlock()
					if !exists || now.Sub(lastSent).Seconds() >= float64(rule.RemindInterval) {
						log.Printf("[PortMonitorScheduler] 规则 %s 提醒间隔告警触发", rule.Name)
						s.sendAlert(rule, "Firing", err.Error())
						s.mu.Lock()
						s.lastAlertSentCache[rule.ID] = now
						s.mu.Unlock()
					}
				}
			}
		} else {
			if previousState != "Firing" {
				rule.AlertState = "Pending"
			} else {
				rule.AlertState = "Firing"
			}
		}
	} else {
		// 未触发 (正常连通)
		rule.FiringAt = nil // 重置告警计时器

		if previousState == "Firing" || previousState == "Pending" {
			recoveryDuration := rule.RecoveryDuration
			if recoveryDuration <= 0 {
				if previousState == "Firing" {
					rule.AlertState = "Recovered"
					rule.RecoveredAt = nil
					s.sendAlert(rule, "Recovered", "")
					// 清理提醒间隔缓存
					s.mu.Lock()
					delete(s.lastAlertSentCache, rule.ID)
					s.mu.Unlock()
				} else {
					rule.AlertState = "OK"
					rule.RecoveredAt = nil
				}
			} else {
				if rule.RecoveredAt == nil {
					rule.RecoveredAt = &now
				}

				recoveryMet := now.Sub(*rule.RecoveredAt).Seconds() >= float64(recoveryDuration)
				if recoveryMet {
					if previousState == "Firing" {
						rule.AlertState = "Recovered"
						rule.RecoveredAt = nil
						s.sendAlert(rule, "Recovered", "")
						// 清理提醒间隔缓存
						s.mu.Lock()
						delete(s.lastAlertSentCache, rule.ID)
						s.mu.Unlock()
					} else {
						rule.AlertState = "OK"
						rule.RecoveredAt = nil
					}
				}
			}
		} else {
			rule.AlertState = "OK"
			rule.RecoveredAt = nil
		}
	}

	// 更新数据库
	ctx := context.TODO()
	if _, dbErr := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "portRule", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState":  rule.AlertState,
			"firingAt":    rule.FiringAt,
			"recoveredAt": rule.RecoveredAt,
		}},
	); dbErr != nil {
		log.Printf("[PortMonitorScheduler] 更新规则状态失败: %v", dbErr)
		return fmt.Errorf("更新状态失败: %v", dbErr)
	}

	return nil
}

// sendAlert 发送告警通知（无论是否配置通知渠道，均写入告警历史）
func (s *PortMonitorScheduler) sendAlert(rule *model.PortMonitorRule, status string, errMsg string) {
	if !isMonitorActive(model.SourceTypePortMonitor, rule.ID) {
		logSkipPaused(model.SourceTypePortMonitor, rule.ID, rule.Name)
		return
	}
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	var statusText string
	if status == "Firing" {
		statusText = "告警中"
	} else {
		statusText = "已恢复"
	}

	var errorInfo string
	if status == "Firing" && errMsg != "" {
		errorInfo = fmt.Sprintf("\n错误信息：%s", errMsg)
	}

	content := fmt.Sprintf("任务名称：%s\n监控目标：%s\n端口号：%d\n检查时间：%s\n状态：%s%s",
		rule.Name,
		rule.Target,
		rule.Port,
		nowStr,
		statusText,
		errorInfo,
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

	// 有通知渠道则发送消息
	if rule.Webhook != nil {
		webhookName = rule.Webhook.Name
		webhookType = rule.Webhook.Type
		title := GetAlertEngine().getAlertTitle()
		data := map[string]interface{}{
			"event_type":    "port_monitor_alert",
			"rule_name":     rule.Name,
			"target":        rule.Target,
			"port":          rule.Port,
			"check_time":    nowStr,
			"alert_status":  status,
			"error_message": errMsg,
		}
		if err := SendNotification(rule.Webhook, title, content, data); err != nil {
			log.Printf("[PortMonitorScheduler] 告警发送失败: %v", err)
		} else {
			log.Printf("[PortMonitorScheduler] 告警发送成功: %s [%s]", rule.Name, status)
		}
	} else {
		log.Printf("[PortMonitorScheduler] 规则 %s 未配置通知渠道，跳过发送，但仍写入历史", rule.Name)
	}

	// 无论是否有通知渠道，始终写入告警历史
	SaveAlertHistory(model.SourceTypePortMonitor, rule.ID, rule.Name, alertState, content, webhookName, webhookType)
}

// StartPortMonitorScheduler 启动全局 端口连通性监控调度器
func StartPortMonitorScheduler() {
	scheduler := GetPortMonitorScheduler()
	scheduler.Start()
}

// StopPortMonitorScheduler 停止全局 端口连通性监控调度器
func StopPortMonitorScheduler() {
	if globalPortMonitorScheduler != nil {
		globalPortMonitorScheduler.Stop()
	}
}

