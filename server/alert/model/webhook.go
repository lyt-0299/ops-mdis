package model

import "time"

// Webhook 通知渠道配置模型（存于 alert_config 集合，kind="webhook"）。
// SMTPPass 以密文形式存储（crypto.Encrypt）。
type Webhook struct {
	ID                  string    `bson:"_id,omitempty"         json:"id"`
	Kind                string    `bson:"kind,omitempty"        json:"-"`
	Name                string    `bson:"name"                  json:"name"`
	Type                string    `bson:"type"                  json:"type"`                // feishu/dingtalk/wecom/email/custom
	URL                 string    `bson:"url"                   json:"url"`
	ProxyURL            string    `bson:"proxyUrl"              json:"proxyUrl"`            // 正向代理
	MentionedList       string    `bson:"mentionedList"         json:"mentionedList"`       // @用户 ID 列表，逗号分隔
	MentionedMobileList string    `bson:"mentionedMobileList"   json:"mentionedMobileList"` // @手机号列表，逗号分隔
	IsAtAll             bool      `bson:"isAtAll"               json:"isAtAll"`             // 是否 @所有人

	// Email (SMTP) 相关配置
	SMTPHost string `bson:"smtpHost" json:"smtpHost"`
	SMTPPort int    `bson:"smtpPort" json:"smtpPort"`
	SMTPUser string `bson:"smtpUser" json:"smtpUser"`
	SMTPPass string `bson:"smtpPass" json:"smtpPass"` // 密文（crypto.Encrypt）；API 响应中置空
	SkipSSL  bool   `bson:"skipSSL"  json:"skipSSL"`
	EmailTo  string `bson:"emailTo"  json:"emailTo"` // 收件人列表，逗号分隔

	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// Sanitize 清除敏感字段，用于 API 响应输出
func (w *Webhook) Sanitize() {
	if w.SMTPPass != "" {
		w.SMTPPass = MaskValue
	}
}
