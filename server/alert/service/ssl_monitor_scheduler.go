package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// SslMonitorScheduler SSL 证书监控调度器
type SslMonitorScheduler struct {
	stopChan           chan struct{}
	stopOnce           sync.Once
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	lastRunCache       map[string]time.Time
	lastAlertSentCache map[string]time.Time // 记录每条规则上次发送告警通知的时间（用于提醒间隔）
	runningRules       map[string]bool      // 防止同一规则的 CheckRule 重叠执行
}

var (
	globalSslMonitorScheduler *SslMonitorScheduler
	sslMonitorSchedulerOnce   sync.Once
)

// NewSslMonitorScheduler 创建 SSL 证书监控调度器
func NewSslMonitorScheduler() *SslMonitorScheduler {
	return &SslMonitorScheduler{
		stopChan:           make(chan struct{}),
		lastRunCache:       make(map[string]time.Time),
		lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypeSslMonitor),
		runningRules:       make(map[string]bool),
	}
}

// GetSslMonitorScheduler 获取全局 SSL 证书监控调度器实例
func GetSslMonitorScheduler() *SslMonitorScheduler {
	sslMonitorSchedulerOnce.Do(func() {
		globalSslMonitorScheduler = NewSslMonitorScheduler()
	})
	return globalSslMonitorScheduler
}

// Start 启动调度器
func (s *SslMonitorScheduler) Start() {
	log.Println("启动 SSL 证书监控调度器...")
	s.wg.Add(1)
	go s.dispatcher()
}

// Stop 停止调度器（多次调用安全）
func (s *SslMonitorScheduler) Stop() {
	log.Println("停止 SSL 证书监控调度器...")
	s.stopOnce.Do(func() { close(s.stopChan) })
	s.wg.Wait()
}

// dispatcher 调度器主循环
func (s *SslMonitorScheduler) dispatcher() {
	defer s.wg.Done()

	ticker := time.NewTicker(15 * time.Second)
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
func (s *SslMonitorScheduler) dispatchRules() {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "sslRule", "status": model.TaskStatusActive})
	if err != nil {
		log.Printf("[SslMonitorScheduler] 查询规则失败: %v", err)
		return
	}
	defer cur.Close(ctx)

	var rules []model.SslMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		log.Printf("[SslMonitorScheduler] 解析规则失败: %v", err)
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
						log.Printf("[SslMonitorScheduler] 规则 %s 检查发生 panic: %v", ruleCopy.Name, r)
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
func (s *SslMonitorScheduler) injectWebhooks(ctx context.Context, rules []model.SslMonitorRule) {
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
func (s *SslMonitorScheduler) TryCheckRule(rule *model.SslMonitorRule) error {
	// 先注入关联再检查：定时路径统一注入过，而手动触发（UI「立即检查」）是 api 层
	// 把库里读出的规则直接递进来，通知渠道（Webhook）这类 `bson:"-"` 字段全是零值——
	// 检查即使跑通也发不出通知。放在入口，调用方不必记得注入。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	rules := []model.SslMonitorRule{*rule}
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

// CheckRule 检查单条 SSL 证书规则
func (s *SslMonitorScheduler) CheckRule(rule *model.SslMonitorRule) error {
	return s.checkRule(rule, false)
}

func (s *SslMonitorScheduler) checkRule(rule *model.SslMonitorRule, manualRefresh bool) error {
	if !manualRefresh && (rule.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeSslMonitor, rule.ID)) {
		logSkipPaused(model.SourceTypeSslMonitor, rule.ID, rule.Name)
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

	address := net.JoinHostPort(rule.Domain, strconv.Itoa(rule.Port))
	timeout := time.Duration(rule.Timeout) * time.Second

	now := time.Now()
	if !manualRefresh && !isMonitorActive(model.SourceTypeSslMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeSslMonitor, rule.ID, rule.Name)
		return nil
	}

	// 执行 TLS 握手，获取证书信息
	var daysLeft int
	var certExpiry time.Time
	var dialErr error

	dialer := &net.Dialer{Timeout: timeout}
	tlsCfg := &tls.Config{
		ServerName:         rule.Domain,
		InsecureSkipVerify: rule.IgnoreSslErrors, // 开启时允许跳过自签证书的严格验证
	}

	conn, err := tls.DialWithDialer(dialer, "tcp", address, tlsCfg)
	if err != nil {
		dialErr = fmt.Errorf("TLS 握手失败: %v", err)
	} else {
		// 取叶子证书（链的第一张）的到期时间
		certs := conn.ConnectionState().PeerCertificates
		conn.Close()
		if len(certs) > 0 {
			certExpiry = certs[0].NotAfter
			daysLeft = int(time.Until(certExpiry).Hours() / 24)
		} else {
			dialErr = fmt.Errorf("未获取到对端证书")
		}
	}

	// 判断是否触发告警：TLS 握手失败，或剩余天数 < 告警阈值
	isFailed := dialErr != nil || daysLeft < rule.WarnDays

	triggered := isFailed
	previousState := rule.AlertState

	// 构造告警错误消息
	var alertErrMsg string
	if dialErr != nil {
		alertErrMsg = dialErr.Error()
	} else if daysLeft < rule.WarnDays {
		alertErrMsg = fmt.Sprintf("证书将于 %s 到期，剩余 %d 天（告警阈值: %d 天）",
			certExpiry.Format("2006-01-02"), daysLeft, rule.WarnDays)
	}

	if manualRefresh && !isMonitorActive(model.SourceTypeSslMonitor, rule.ID) {
		snapshotState := model.AlertStateOK
		if triggered {
			snapshotState = model.AlertStateFiring
		}
		updates := bson.M{
			"alertState":  snapshotState,
			"firingAt":    nil,
			"recoveredAt": nil,
			"lastCheckAt": now,
		}
		if dialErr == nil {
			updates["certExpiry"] = certExpiry
			updates["daysLeft"] = daysLeft
		}
		ctx := context.TODO()
		if _, dbErr := database.Config().UpdateOne(ctx,
			bson.M{"_id": rule.ID, "kind": "sslRule"},
			bson.M{"$set": updates},
		); dbErr != nil {
			log.Printf("[SslMonitorScheduler] 更新暂停规则刷新结果失败: %v", dbErr)
			return fmt.Errorf("更新刷新结果失败: %v", dbErr)
		}
		return nil
	}

	if triggered {
		rule.RecoveredAt = nil

		if rule.FiringAt == nil {
			rule.FiringAt = &now
		}

		if previousState != "Firing" {
			rule.AlertState = "Firing"
			s.sendAlert(rule, "Firing", alertErrMsg, daysLeft, certExpiry)
			// 记录首次发送时间
			s.mu.Lock()
			s.lastAlertSentCache[rule.ID] = now
			s.mu.Unlock()
		} else if rule.RepeatAlert {
			// 开启了持续告警，且仍处于触发状态
			log.Printf("[SslMonitorScheduler] 规则 %s 持续告警触发", rule.Name)
			s.sendAlert(rule, "Firing", alertErrMsg, daysLeft, certExpiry)
		} else if rule.RemindInterval > 0 {
			// 提醒间隔逻辑
			s.mu.Lock()
			lastSent, exists := s.lastAlertSentCache[rule.ID]
			s.mu.Unlock()
			if !exists || now.Sub(lastSent).Seconds() >= float64(rule.RemindInterval) {
				log.Printf("[SslMonitorScheduler] 规则 %s 提醒间隔告警触发", rule.Name)
				s.sendAlert(rule, "Firing", alertErrMsg, daysLeft, certExpiry)
				s.mu.Lock()
				s.lastAlertSentCache[rule.ID] = now
				s.mu.Unlock()
			}
		}
	} else {
		// 证书健康
		rule.FiringAt = nil

		if previousState == "Firing" || previousState == "Pending" {
			if previousState == "Firing" {
				rule.AlertState = "Recovered"
				rule.RecoveredAt = nil
				s.sendAlert(rule, "Recovered", "", daysLeft, certExpiry)
				// 清理提醒间隔缓存
				s.mu.Lock()
				delete(s.lastAlertSentCache, rule.ID)
				s.mu.Unlock()
			} else {
				rule.AlertState = "OK"
				rule.RecoveredAt = nil
			}
		} else {
			rule.AlertState = "OK"
			rule.RecoveredAt = nil
		}
	}

	// 更新数据库（同时写入证书到期时间和剩余天数）
	updates := bson.M{
		"alertState":  rule.AlertState,
		"firingAt":    rule.FiringAt,
		"recoveredAt": rule.RecoveredAt,
		"lastCheckAt": now,
	}
	if dialErr == nil {
		updates["certExpiry"] = certExpiry
		updates["daysLeft"] = daysLeft
	}

	ctx := context.TODO()
	if _, dbErr := database.Config().UpdateOne(ctx,
		bson.M{"_id": rule.ID, "kind": "sslRule", "status": model.TaskStatusActive},
		bson.M{"$set": updates},
	); dbErr != nil {
		log.Printf("[SslMonitorScheduler] 更新规则状态失败: %v", dbErr)
		return fmt.Errorf("更新状态失败: %v", dbErr)
	}

	return nil
}

// sendAlert 发送告警通知（无论是否配置通知渠道，均写入告警历史）
func (s *SslMonitorScheduler) sendAlert(rule *model.SslMonitorRule, status string, errMsg string, daysLeft int, certExpiry time.Time) {
	if !isMonitorActive(model.SourceTypeSslMonitor, rule.ID) {
		logSkipPaused(model.SourceTypeSslMonitor, rule.ID, rule.Name)
		return
	}
	nowStr := time.Now().Format("2006-01-02 15:04:05")
	var statusText string
	if status == "Firing" {
		statusText = "告警中"
	} else {
		statusText = "已恢复"
	}

	var content string
	if status == "Firing" {
		if errMsg != "" {
			content = fmt.Sprintf("任务名称：%s\n监控域名：%s\n端口：%d\n检查时间：%s\n状态：%s\n错误信息：%s",
				rule.Name, rule.Domain, rule.Port, nowStr, statusText, errMsg)
		} else {
			content = fmt.Sprintf("任务名称：%s\n监控域名：%s\n端口：%d\n证书到期日：%s\n剩余天数：%d 天\n检查时间：%s\n状态：%s",
				rule.Name, rule.Domain, rule.Port,
				certExpiry.Format("2006-01-02"), daysLeft, nowStr, statusText)
		}
	} else {
		content = fmt.Sprintf("任务名称：%s\n监控域名：%s\n端口：%d\n证书到期日：%s\n剩余天数：%d 天\n检查时间：%s\n状态：%s",
			rule.Name, rule.Domain, rule.Port,
			certExpiry.Format("2006-01-02"), daysLeft, nowStr, statusText)
	}

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
			"event_type":   "ssl_monitor_alert",
			"rule_name":    rule.Name,
			"domain":       rule.Domain,
			"port":         rule.Port,
			"days_left":    daysLeft,
			"cert_expiry":  certExpiry.Format("2006-01-02"),
			"check_time":   nowStr,
			"alert_status": status,
			"error":        errMsg,
		}
		if err := SendNotification(rule.Webhook, title, content, data); err != nil {
			log.Printf("[SslMonitorScheduler] 告警发送失败: %v", err)
		} else {
			log.Printf("[SslMonitorScheduler] 告警发送成功: %s [%s]", rule.Name, status)
		}
	} else {
		log.Printf("[SslMonitorScheduler] 规则 %s 未配置通知渠道，跳过发送，但仍写入历史", rule.Name)
	}

	// 无论是否有通知渠道，始终写入告警历史
	SaveAlertHistory(model.SourceTypeSslMonitor, rule.ID, rule.Name, alertState, content, webhookName, webhookType)
}

// StartSslMonitorScheduler 启动全局 SSL 证书监控调度器
func StartSslMonitorScheduler() {
	scheduler := GetSslMonitorScheduler()
	scheduler.Start()
}

// StopSslMonitorScheduler 停止全局 SSL 证书监控调度器
func StopSslMonitorScheduler() {
	if globalSslMonitorScheduler != nil {
		globalSslMonitorScheduler.Stop()
	}
}
