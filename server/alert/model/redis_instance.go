package model

import (
	"strconv"
	"strings"
	"time"
)

// RedisInstance Redis 实例配置模型(仅看图，存于 alert_config 集合，discriminator: kind="redisInstance")。
type RedisInstance struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	Kind      string    `bson:"kind,omitempty" json:"-"`
	Name      string    `bson:"name"          json:"name"`
	Host      string    `bson:"host"          json:"host"` // 单节点写法(旧)，nodes 为空时回退到它
	Port      int       `bson:"port"          json:"port"`
	// Nodes 是 host:port 列表。哨兵/集群部署下每个节点各起一个 exporter——redis_exporter
	// 不做哨兵拓扑发现，与其猜当前主库，不如把主从都采上，角色变化由 redis_instance_info
	// 的 role 标签体现，主从切换也不用重配。
	Nodes []string `bson:"nodes,omitempty" json:"nodes"`
	Password  string    `bson:"password"      json:"password"` // 密文(crypto.Encrypt)；API 响应中脱敏
	DB        int       `bson:"db"            json:"db"`
	TLS       bool      `bson:"tls"           json:"tls"`
	SkipSSL   bool      `bson:"skipSSL"       json:"skipSSL"`
	Enabled   bool      `bson:"enabled"       json:"enabled"`
	Purposes  []string  `bson:"purposes"      json:"purposes"` // 取值 "metrics" / "alert"
	CreatedAt time.Time `bson:"createdAt"     json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt"     json:"updatedAt"`
}

// Sanitize 清除敏感字段，用于 API 响应输出。
func (ri *RedisInstance) Sanitize() {
	if ri.Password != "" {
		ri.Password = MaskValue
	}
}

// NormalizeNodes 让 nodes 与旧的 host/port 互相兜底。
//
// 两条路都会进来：UI 表单填的是多节点框（前端已把首节点回填 host/port），而 YAML
// 批量导入只写 nodes——后者若不补 host/port，会被创建接口的必填校验直接挡掉，
// 客户只看到「host/port 不能为空」，而他配的明明是 nodes。反过来，存量数据只有
// host/port、没有 nodes，补出 nodes 才能走统一的展开逻辑。
func (ri *RedisInstance) NormalizeNodes() {
	if len(ri.Nodes) > 0 {
		if ri.Host == "" {
			host, port := splitHostPort(ri.Nodes[0], 6379)
			ri.Host, ri.Port = host, port
		}
		return
	}
	if ri.Host != "" {
		port := ri.Port
		if port == 0 {
			port = 6379
		}
		ri.Nodes = []string{ri.Host + ":" + strconv.Itoa(port)}
	}
}

// splitHostPort 拆 host:port，无端口或端口非法时用 def。
func splitHostPort(node string, def int) (string, int) {
	if i := strings.LastIndex(node, ":"); i > 0 {
		if p, err := strconv.Atoi(node[i+1:]); err == nil && p > 0 {
			return node[:i], p
		}
	}
	return node, def
}
