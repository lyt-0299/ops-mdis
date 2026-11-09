package model

import "time"

// KafkaInstance Kafka 实例配置模型
type KafkaInstance struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	Kind      string    `bson:"kind,omitempty" json:"-"`
	Name      string    `bson:"name"          json:"name"`
	Brokers   []string  `bson:"brokers"       json:"brokers"`
	SASL      *SASL     `bson:"sasl"          json:"sasl,omitempty"`
	Enabled   bool      `bson:"enabled"       json:"enabled"`
	Purposes  []string  `bson:"purposes"      json:"purposes"` // 取值 "metrics" / "alert"
	CreatedAt time.Time `bson:"createdAt"     json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"     json:"updatedAt"`
}

// Sanitize 清除敏感字段，用于 API 响应输出
func (ki *KafkaInstance) Sanitize() {
	if ki.SASL != nil {
		ki.SASL.Password = ""
	}
}

// SASL SASL 认证配置
type SASL struct {
	Enabled   bool   `bson:"enabled"    json:"enabled"`
	Mechanism string `bson:"mechanism"  json:"mechanism"` // PLAIN, SCRAM-SHA-256, SCRAM-SHA-512
	Username  string `bson:"username"   json:"username"`
	Password  string `bson:"password"   json:"password"`
	TLS       bool   `bson:"tls"        json:"tls"`     // 是否启用 TLS (SASL_SSL)，默认 false 为 SASL_PLAINTEXT
	SkipTLS   bool   `bson:"skipTls"    json:"skipTls"` // 跳过服务端证书校验（仅当 TLS=true 时生效）
}

// IsValidMechanism 校验 SASL 机制是否为支持的值
func (s *SASL) IsValidMechanism() bool {
	switch s.Mechanism {
	case "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512":
		return true
	}
	return false
}
