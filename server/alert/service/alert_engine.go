package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// AlertEngine 告警引擎（差值感知状态机）
type AlertEngine struct {
	mu                 sync.Mutex
	alertTitle         string
	lastAlertSentCache map[string]time.Time // key: task.ID，记录上次发送告警通知的时间（用于提醒间隔）
}

var (
	globalAlertEngine *AlertEngine
	alertOnce         sync.Once

	// fallbackAlertEngine 是 globalAlertEngine 未初始化时的降级实例，见 GetAlertEngine。
	// lastAlertSentCache 必须在这里建好：ProcessLag 会往里写，nil map 写入照样 panic。
	fallbackAlertEngine = &AlertEngine{lastAlertSentCache: map[string]time.Time{}}
)

const defaultAlertTitle = "MDIS 告警消息"

// GetAlertEngine 获取全局告警引擎实例。
//
// 这里必须兜底。1.4.0 从 InfraPulse 移植时只搬了 InitAlertEngine 的定义、
// 没搬调用（原调用在上游的 cfg 装配处），globalAlertEngine 从此一直是 nil。
// 七个调度器（基础资源/端口/SSL/Kafka堆积/Kafka重平衡/MySQL/MongoDB）在发通知前
// 都要先 GetAlertEngine().getAlertTitle()，于是每次真要发告警的那一刻就空指针
// panic——客户堆栈精确落在下面 getAlertTitle 的 `if a.alertTitle != ""` 那行。
// 后果不止「通知发不出去」：mongo 等调度器把 SaveAlertHistory 排在发送之后，
// panic 顺手把它一起带走，表现为「告警历史永远是空的」。
func GetAlertEngine() *AlertEngine {
	if globalAlertEngine == nil {
		// 走到这里说明 InitAlertEngine 还没跑（DB 未就绪，或将来又有人漏调）。
		// 不能在这里顺手 InitAlertEngine：它要 RestoreLastAlertSentCache 去读
		// ops-mongo，DB 没起时反而换个地方 panic。返回一个不依赖 DB 的降级实例，
		// 只丢「上次发送时间」缓存（影响提醒间隔去重），通知本身照发。
		return fallbackAlertEngine
	}
	return globalAlertEngine
}

// InitAlertEngine 初始化告警引擎（alertTitle 对应上游 cfg.Monitor.AlertTitle）
func InitAlertEngine(alertTitle string) {
	alertOnce.Do(func() {
		globalAlertEngine = &AlertEngine{
			alertTitle:         alertTitle,
			lastAlertSentCache: RestoreLastAlertSentCache(model.SourceTypeKafka),
		}
	})
}

// topicLabel 返回告警正文里的 Topic 展示值。Topic 留空表示「整个消费组」，
// 直接打空串会渲染成「Topic：」后面什么都没有，看着像配置丢了。
func topicLabel(topic string) string {
	if topic == "" {
		return "（整个消费组）"
	}
	return topic
}

// getAlertTitle 获取告警标题
func (a *AlertEngine) getAlertTitle() string {
	if a.alertTitle != "" {
		return a.alertTitle
	}
	return defaultAlertTitle
}

// ProcessLag 检查 Lag 并决定是否报警
func (a *AlertEngine) ProcessLag(task *model.Task, currentLag int64) {
	title := a.getAlertTitle()

	// 如果任务已暂停，不触发告警
	if task.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeKafka, task.ID) {
		log.Printf("任务 %s 处于暂停状态，跳过告警检查", task.Name)
		return
	}

	// 简单的状态机逻辑
	previousState := task.AlertState

	// 告警触发条件：Lag > 阈值
	if currentLag > task.Threshold {
		// 计算趋势
		var trend string
		if previousState != model.AlertStateFiring {
			trend = "首次触发"
		} else {
			delta := currentLag - task.LastLag
			if delta > 0 {
				trend = fmt.Sprintf("增加 +%d", delta)
			} else if delta < 0 {
				trend = fmt.Sprintf("减少 %d", delta)
			} else {
				trend = "无变化"
			}
		}

		// 判断是否需要发送告警
		isNewFiring := previousState != model.AlertStateFiring
		shouldRepeat := previousState == model.AlertStateFiring && task.RepeatAlert

		// 提醒间隔逻辑：未开启持续告警时，按间隔重复提醒
		isRemind := false
		if previousState == model.AlertStateFiring && !task.RepeatAlert && task.RemindInterval > 0 {
			a.mu.Lock()
			lastSent, exists := a.lastAlertSentCache[task.ID]
			a.mu.Unlock()
			if !exists || time.Since(lastSent).Seconds() >= float64(task.RemindInterval) {
				isRemind = true
			}
		}

		if isNewFiring || shouldRepeat || isRemind {
			nowStr := time.Now().Format("2006-01-02 15:04:05")
			statusText := "告警中"
			if shouldRepeat {
				log.Printf("[AlertEngine] 任务 %s 持续告警触发", task.Name)
			} else if isRemind {
				log.Printf("[AlertEngine] 任务 %s 提醒间隔告警触发", task.Name)
			}

			content := fmt.Sprintf("任务名称：%s\nKafka 实例：%s\nTopic：%s\n消费组：%s\n当前堆积：%d\n堆积变化：%s\n告警阈值：%d\n检查时间：%s\n状态：%s",
				task.Name, task.KafkaInstance.Name, topicLabel(task.Topic), task.ConsumerGroup, currentLag, trend, task.Threshold, nowStr, statusText)

			if task.AlertNote != "" {
				content += fmt.Sprintf("\n告警备注：%s", task.AlertNote)
			}

			data := map[string]interface{}{
				"event_type":     "kafka_lag_alert",
				"task_name":      task.Name,
				"kafka_instance": task.KafkaInstance.Name,
				"topic":          task.Topic,
				"consumer_group": task.ConsumerGroup,
				"current_lag":    currentLag,
				"threshold":      task.Threshold,
				"trend":          trend,
				"check_time":     nowStr,
				"alert_status":   model.AlertStateFiring,
			}
			a.sendAlert(task, title, content, data)

			// 记录发送时间（用于提醒间隔）
			a.mu.Lock()
			a.lastAlertSentCache[task.ID] = time.Now()
			a.mu.Unlock()
		}
		task.AlertState = model.AlertStateFiring

	} else {
		// Lag 低于阈值
		if previousState == model.AlertStateFiring {
			nowStr := time.Now().Format("2006-01-02 15:04:05")
			content := fmt.Sprintf("任务名称：%s\nKafka 实例：%s\nTopic：%s\n消费组：%s\n当前堆积：%d\n告警阈值：%d\n检查时间：%s\n状态：已恢复",
				task.Name,
				task.KafkaInstance.Name,
				topicLabel(task.Topic),
				task.ConsumerGroup,
				currentLag,
				task.Threshold,
				nowStr,
			)

			if task.AlertNote != "" {
				content += fmt.Sprintf("\n告警备注：%s", task.AlertNote)
			}
			data := map[string]interface{}{
				"event_type":     "kafka_lag_alert",
				"task_name":      task.Name,
				"kafka_instance": task.KafkaInstance.Name,
				"topic":          task.Topic,
				"consumer_group": task.ConsumerGroup,
				"current_lag":    currentLag,
				"threshold":      task.Threshold,
				"check_time":     nowStr,
				"alert_status":   model.AlertStateRecovered,
			}
			a.sendAlert(task, title, content, data)
			task.AlertState = model.AlertStateRecovered

			// 清理提醒间隔缓存
			a.mu.Lock()
			delete(a.lastAlertSentCache, task.ID)
			a.mu.Unlock()
		} else {
			task.AlertState = model.AlertStateOK
		}
	}

	// 更新任务状态（仅当任务仍为 active 时）
	task.LastLag = currentLag
	ctx := context.TODO()
	_, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": task.ID, "kind": "task", "status": model.TaskStatusActive},
		bson.M{"$set": bson.M{
			"alertState": task.AlertState,
			"lastLag":    task.LastLag,
		}},
	)
	if err != nil {
		log.Printf("更新任务状态失败: %v", err)
	}
}

// sendAlert 内部封装，调用通用的 SendNotification（无论是否配置通知渠道，均写入告警历史）
func (a *AlertEngine) sendAlert(task *model.Task, title, content string, data map[string]interface{}) {
	if !isMonitorActive(model.SourceTypeKafka, task.ID) {
		logSkipPaused(model.SourceTypeKafka, task.ID, task.Name)
		return
	}
	alertState := model.AlertStateFiring
	if v, ok := data["alert_status"]; ok && v == model.AlertStateRecovered {
		alertState = model.AlertStateRecovered
	}

	webhookName := ""
	webhookType := ""

	// 有通知渠道则发送消息
	if task.Webhook != nil {
		webhookName = task.Webhook.Name
		webhookType = task.Webhook.Type
		if err := SendNotification(task.Webhook, title, content, data); err != nil {
			log.Printf("告警发送失败: %v", err)
		} else {
			log.Printf("告警发送成功: %s", task.Name)
		}
	} else {
		log.Printf("任务 %s 未配置通知渠道，跳过发送，但仍写入历史", task.Name)
	}

	// 无论是否有通知渠道，始终写入告警历史
	SaveAlertHistory(model.SourceTypeKafka, task.ID, task.Name, alertState, content, webhookName, webhookType)
}

// SplitMentions 辅助函数：分割逗号分隔的字符串并去除空白
func SplitMentions(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// 用户输入中性化：把可能在告警内容中被 Markdown / 富文本平台解释为控制 token
// 的字符切断。仅用零宽空格（ZWSP）阻断 token 起始，不改变可视化呈现。
//
// 注意触发时机：此替换发生在 SendNotification 入口、各渠道分支构造卡片 / 引用块
// 之前；调度器为状态颜色注入的 <font color='...'> 与 mention 拼接的 <at id='...'>
// 都在 sanitize 之后才追加，因此不会误伤合法 token。
var userMarkdownSanitizer = strings.NewReplacer(
	"<at", "<​at",
	"<AT", "<​AT",
	"<At", "<​At",
	"<font", "<​font",
	"<FONT", "<​FONT",
	"<Font", "<​Font",
	"<a ", "<​a ",
	"<A ", "<​A ",
	"<script", "<​script",
	"<iframe", "<​iframe",
	"<img", "<​img",
)

// SendNotification 通用告警发送函数
func SendNotification(webhook *model.Webhook, title, content string, data map[string]interface{}) error {
	// 用户输入中性化（仅作用于 Markdown 类渠道；email 走 HTML 转义，已在 formatEmailHTML 处理）。
	fullContent := content
	if webhook.Type != "email" && webhook.Type != "custom" {
		fullContent = userMarkdownSanitizer.Replace(fullContent)
	}
	var payload interface{}

	switch webhook.Type {
	case "feishu":
		// 飞书 Interactive (卡片) 格式
		headerColor := "blue" // 默认
		headerTitle := title

		if strings.Contains(fullContent, "状态：告警中") {
			headerColor = "red"
		} else if strings.Contains(fullContent, "状态：已恢复") {
			headerColor = "green"
		}

		// 构造 Card 内容
		// 飞书卡片 Markdown：列表项（- xxx）之后若直接跟 \n文字，飞书会将其
		// 解析为列表延续（粘连），必须用 \n\n 空行才能真正另起段落。
		// 仅当 content 中含有 "- " 列表项时才做此替换，避免无列表内容时出现多余空行。
		cardContent := fullContent
		if strings.Contains(fullContent, "\n- ") {
			cardContent = strings.ReplaceAll(fullContent, "\n告警备注", "\n\n告警备注")
		}

		// 处理 Mentions
		if webhook.IsAtAll {
			cardContent += "\n<at id=\"all\"></at>"
		}
		if webhook.MentionedList != "" {
			ids := SplitMentions(webhook.MentionedList)
			for _, id := range ids {
				cardContent += fmt.Sprintf("\n<at id=\"%s\"></at>", id)
			}
		}

		payload = map[string]interface{}{
			"msg_type": "interactive",
			"card": map[string]interface{}{
				"schema": "2.0",
				"config": map[string]interface{}{
					"update_multi": true,
				},
				"header": map[string]interface{}{
					"template": headerColor,
					"title": map[string]interface{}{
						"content": headerTitle,
						"tag":     "plain_text",
					},
				},
				"body": map[string]interface{}{
					"elements": []interface{}{
						map[string]interface{}{
							"tag":       "markdown",
							"content":   cardContent,
							"text_size": "normal",
						},
					},
				},
			},
		}

	case "dingtalk":
		// 钉钉 Markdown 格式
		formattedContent := strings.ReplaceAll(fullContent, "\n", "\n\n> ")
		formattedContent = strings.ReplaceAll(formattedContent, "状态：告警中", "状态：<font color='#FF0000'>告警中</font>")
		formattedContent = strings.ReplaceAll(formattedContent, "状态：已恢复", "状态：<font color='#388E3C'>已恢复</font>")

		markdownText := fmt.Sprintf("#### %s\n\n> %s", title, formattedContent)

		var atMobiles []string
		var atUserIds []string

		if webhook.IsAtAll {
			markdownText += "\n\n@所有人"
		} else {
			atMobiles = SplitMentions(webhook.MentionedMobileList)
			atUserIds = SplitMentions(webhook.MentionedList)

			if len(atMobiles) > 0 {
				markdownText += "\n\n"
				for _, mobile := range atMobiles {
					markdownText += fmt.Sprintf("@%s ", mobile)
				}
			}
		}

		payload = map[string]interface{}{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"title": title,
				"text":  markdownText,
			},
			"at": map[string]interface{}{
				"atMobiles": atMobiles,
				"atUserIds": atUserIds,
				"isAtAll":   webhook.IsAtAll,
			},
		}

	case "wecom":
		// 企业微信 Markdown 格式
		formattedContent := strings.ReplaceAll(fullContent, "\n", "\n> ")
		formattedContent = strings.ReplaceAll(formattedContent, "状态：告警中", "状态：<font color=\"warning\">告警中</font>")
		formattedContent = strings.ReplaceAll(formattedContent, "状态：已恢复", "状态：<font color=\"info\">已恢复</font>")

		var mentionText string
		if webhook.IsAtAll {
			mentionText += "\n@所有人"
		} else {
			userIds := SplitMentions(webhook.MentionedList)
			mobiles := SplitMentions(webhook.MentionedMobileList)

			for _, uid := range userIds {
				mentionText += fmt.Sprintf(" <@%s>", uid)
			}
			for _, mob := range mobiles {
				mentionText += fmt.Sprintf(" @%s", mob)
			}
		}

		finalText := fmt.Sprintf("#### %s\n> %s%s", title, formattedContent, mentionText)

		payload = map[string]interface{}{
			"msgtype": "markdown",
			"markdown": map[string]string{
				"content": finalText,
			},
		}

	case "email":
		return sendEmailNotification(webhook, title, fullContent)

	case "custom":
		// 自定义 Webhook: 发送标准 JSON 载荷
		var contentVal interface{} = fullContent
		eventType := "unknown_alert"

		if data != nil {
			if et, ok := data["event_type"]; ok {
				eventType = fmt.Sprintf("%v", et)
				contentData := make(map[string]interface{}, len(data)-1)
				for k, v := range data {
					if k != "event_type" {
						contentData[k] = v
					}
				}
				contentVal = contentData
			} else {
				contentVal = data
			}
		}

		payload = map[string]interface{}{
			"title":      title,
			"event_type": eventType,
			"timestamp":  time.Now().Format(time.RFC3339),
			"content":    contentVal,
		}

	default:
		return fmt.Errorf("不支持的 Webhook 类型: %s", webhook.Type)
	}

	// 序列化 JSON (Only for Webhooks)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化告警消息失败: %v", err)
	}

	// 动态创建 HTTP 客户端（如果有代理配置）
	httpClient := NewHTTPClient(webhook.ProxyURL)

	// 发送 HTTP 请求
	req, err := http.NewRequest("POST", webhook.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 HTTP 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送告警失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("告警发送失败，状态码: %d", resp.StatusCode)
	}

	return nil
}

// sendEmailNotification 发送邮件告警
func sendEmailNotification(webhook *model.Webhook, title, content string) error {
	// CRLF 注入防御
	if strings.ContainsAny(webhook.SMTPUser, "\r\n") {
		return fmt.Errorf("SMTP 发件用户名不可含换行字符")
	}
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("邮件主题不可含换行字符")
	}
	for _, addr := range SplitMentions(webhook.EmailTo) {
		if strings.ContainsAny(addr, "\r\n") {
			return fmt.Errorf("邮件收件人地址不可含换行字符")
		}
	}

	htmlBody := formatEmailHTML(title, content)

	to := SplitMentions(webhook.EmailTo)
	if len(to) == 0 {
		return fmt.Errorf("未配置收件人")
	}

	header := make(map[string]string)
	header["From"] = webhook.SMTPUser
	header["To"] = strings.Join(to, ",")
	header["Subject"] = title
	header["Content-Type"] = "text/html; charset=UTF-8"

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + htmlBody

	addr := net.JoinHostPort(webhook.SMTPHost, strconv.Itoa(webhook.SMTPPort))
	auth := smtp.PlainAuth("", webhook.SMTPUser, webhook.SMTPPass, webhook.SMTPHost)

	tlsConfig := &tls.Config{
		InsecureSkipVerify: webhook.SkipSSL,
		ServerName:         webhook.SMTPHost,
	}

	// 端口 465：SMTPS（Explicit TLS）
	if webhook.SMTPPort == 465 {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("连接 SMTP 服务器失败: %v", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, webhook.SMTPHost)
		if err != nil {
			return fmt.Errorf("创建 SMTP 客户端失败: %v", err)
		}
		defer client.Quit()

		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %v", err)
		}
		if err = client.Mail(webhook.SMTPUser); err != nil {
			return err
		}
		for _, addr := range to {
			if err = client.Rcpt(addr); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(message))
		if err != nil {
			return err
		}
		return w.Close()
	}

	// 其他端口：TCP + STARTTLS
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %v", err)
	}

	c, err := smtp.NewClient(conn, webhook.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("创建 SMTP 客户端失败: %v", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS 失败: %v", err)
		}
	}

	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("认证失败: %v", err)
	}
	if err = c.Mail(webhook.SMTPUser); err != nil {
		return err
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(message))
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	return c.Quit()
}

// formatEmailHTML 将纯文本内容转为精美的 HTML 模板
func formatEmailHTML(title, content string) string {
	headerColor := "#3b82f6"
	badgeText := "系统通知"
	badgeColor := "#3b82f6"
	badgeBg := "#eff6ff"

	if strings.Contains(content, "状态：告警中") || strings.Contains(content, model.AlertStateFiring) {
		headerColor = "#ef4444"
		badgeText = "告警中"
		badgeColor = "#ef4444"
		badgeBg = "#fef2f2"
	} else if strings.Contains(content, "状态：已恢复") || strings.Contains(content, model.AlertStateRecovered) {
		headerColor = "#10b981"
		badgeText = "已恢复"
		badgeColor = "#10b981"
		badgeBg = "#ecfdf5"
	}

	lines := strings.Split(content, "\n")
	var itemsHTML string
	var noteHTML string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "：") {
			parts := strings.SplitN(line, "：", 2)
			label := html.EscapeString(strings.TrimSpace(parts[0]))
			value := html.EscapeString(strings.TrimSpace(parts[1]))

			if label == "告警备注" {
				noteHTML = fmt.Sprintf(`
					<div style="margin-top: 24px; padding: 16px; background: #f8fafc; border-left: 4px solid #cbd5e1; border-radius: 4px; color: #475569; font-size: 14px;">
						<strong>%s：</strong>%s
					</div>`, label, value)
				continue
			}

			itemsHTML += fmt.Sprintf(`
				<tr>
					<td style="padding: 12px 0; border-bottom: 1px solid #f1f5f9; width: 100px; font-weight: 600; color: #64748b; font-size: 14px; vertical-align: top;">%s</td>
					<td style="padding: 12px 0; border-bottom: 1px solid #f1f5f9; color: #1e293b; font-size: 14px; font-family: 'Segoe UI', Roboto, sans-serif; word-break: break-all;">%s</td>
				</tr>`, label, value)
		} else {
			itemsHTML += fmt.Sprintf(`
				<tr>
					<td colspan="2" style="padding: 12px 0; border-bottom: 1px solid #f1f5f9; color: #1e293b; font-size: 14px;">%s</td>
				</tr>`, html.EscapeString(line))
		}
	}

	safeTitle := html.EscapeString(title)
	nowStr := time.Now().Format("2006-01-02 15:04:05")

	return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f8fafc; margin: 0; padding: 20px;">
    <table cellpadding="0" cellspacing="0" border="0" width="100%%" style="max-width: 600px; margin: 0 auto; background: white; border-radius: 12px; overflow: hidden; box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1); border: 1px solid #e2e8f0;">
        <!-- Header -->
        <tr>
            <td style="padding: 30px; color: white; font-weight: bold; font-size: 20px; text-align: center; background: %s;">
                %s
            </td>
        </tr>
        <!-- Content -->
        <tr>
            <td style="padding: 32px; color: #334155;">
                <table cellpadding="0" cellspacing="0" border="0" width="100%%">
                    <tr>
                        <td>
                            <div style="display: inline-block; padding: 4px 12px; border-radius: 9999px; font-size: 12px; font-weight: 600; text-transform: uppercase; margin-bottom: 20px; background: %s; color: %s;">
                                %s
                            </div>
                        </td>
                    </tr>
                    %s
                </table>
                %s
            </td>
        </tr>
        <!-- Footer -->
        <tr>
            <td style="padding: 24px; text-align: center; color: #94a3b8; font-size: 12px; border-top: 1px solid #f1f5f9; background: #fafafa;">
                <div>发送时间: %s</div>
            </td>
        </tr>
    </table>
</body>
</html>`, headerColor, safeTitle, badgeBg, badgeColor, badgeText, itemsHTML, noteHTML, nowStr)
}
