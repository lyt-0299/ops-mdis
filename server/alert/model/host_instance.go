package model

import "time"

// KindHostInstance 是主机(node_exporter)数据源在 alert_config 集合的 discriminator(kind)取值。
const KindHostInstance = "hostInstance"

// HostInstance 主机数据源配置模型(node_exporter，存于 alert_config 集合，discriminator: kind="hostInstance")。
// 与中间件不同：node_exporter 跑在被监控主机本机，Address 即它自己的地址(host:port，
// 如 172.17.30.50:59100)，SD 直接原样输出该地址，不经 agent、不算端口基。
type HostInstance struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	Kind      string    `bson:"kind,omitempty" json:"-"`
	Name      string    `bson:"name"          json:"name"`
	Address   string    `bson:"address"       json:"address"` // node_exporter 地址 host:port
	Enabled   bool      `bson:"enabled"       json:"enabled"`
	Purposes  []string  `bson:"purposes"      json:"purposes"` // 取值 "metrics" / "alert"
	CreatedAt time.Time `bson:"createdAt"     json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"     json:"updatedAt"`
}

// Sanitize 无敏感字段，留空实现以与其他实例模型保持一致。
func (hi *HostInstance) Sanitize() {}
