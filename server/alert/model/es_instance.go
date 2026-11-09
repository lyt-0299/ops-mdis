package model

import "time"

// ESInstance Elasticsearch 实例配置模型(仅看图，存于 alert_config 集合，discriminator: kind="esInstance")。
type ESInstance struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	Kind      string    `bson:"kind,omitempty" json:"-"`
	Name      string    `bson:"name"          json:"name"`
	URL       string    `bson:"url"           json:"url"` // 单地址写法(旧)，nodes 为空时回退到它
	// Nodes 是完整 URL 列表（含 scheme）。elasticsearch_exporter 的 --es.uri 只吃一个地址，
	// 配多个节点时每个各起一个 exporter，任一节点挂掉不至于整条源断采。
	Nodes []string `bson:"nodes,omitempty" json:"nodes"`
	Username  string    `bson:"username"      json:"username"`
	Password  string    `bson:"password"      json:"password"` // 密文(crypto.Encrypt)；API 响应中脱敏
	SkipSSL   bool      `bson:"skipSSL"       json:"skipSSL"`
	Enabled   bool      `bson:"enabled"       json:"enabled"`
	Purposes  []string  `bson:"purposes"      json:"purposes"` // 取值 "metrics" / "alert"
	CreatedAt time.Time `bson:"createdAt"     json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"     json:"updatedAt"`
}

// Sanitize 清除敏感字段，用于 API 响应输出。
func (ei *ESInstance) Sanitize() {
	if ei.Password != "" {
		ei.Password = MaskValue
	}
}

// NormalizeNodes 让 nodes 与旧的单地址 url 互相兜底，理由同 RedisInstance.NormalizeNodes。
func (ei *ESInstance) NormalizeNodes() {
	if len(ei.Nodes) > 0 {
		if ei.URL == "" {
			ei.URL = ei.Nodes[0]
		}
		return
	}
	if ei.URL != "" {
		ei.Nodes = []string{ei.URL}
	}
}
