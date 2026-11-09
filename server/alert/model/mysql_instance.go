package model

import (
	"strings"
	"time"
)

const (
	MySQLArchitectureStandalone  = "standalone"
	MySQLArchitectureReplication = "replication"
	MySQLArchitectureMGR         = "mgr"
)

// MySQLInstance MySQL 数据源配置模型。
type MySQLInstance struct {
	ID               string            `bson:"_id,omitempty" json:"id"`
	Kind             string            `bson:"kind,omitempty" json:"-"`
	Name             string            `bson:"name"          json:"name"`
	Architecture     string            `bson:"architecture"  json:"architecture"` // standalone/replication/mgr
	Nodes            []string          `bson:"nodes"         json:"nodes"`        // host:port 列表
	Username         string            `bson:"username"      json:"username"`
	Password         string            `bson:"password"      json:"password"`
	Database         string            `bson:"database"      json:"database"`
	TLS              bool              `bson:"tls"           json:"tls"`
	SkipSSL          bool              `bson:"skipSSL"       json:"skipSSL"`
	Enabled          bool              `bson:"enabled"       json:"enabled"`
	MetricCollection *MetricCollection `bson:"metricCollection,omitempty" json:"metricCollection,omitempty"`
	Purposes         []string          `bson:"purposes"      json:"purposes"` // 取值 "metrics" / "alert"
	CreatedAt        time.Time         `bson:"createdAt"     json:"createdAt"`
	UpdatedAt        time.Time         `bson:"updatedAt"     json:"updatedAt"`
}

func (mi *MySQLInstance) Sanitize() {
	if mi.Password != "" {
		mi.Password = MaskValue
	}
}

func (mi *MySQLInstance) Normalize() {
	if mi.Architecture == "" {
		mi.Architecture = MySQLArchitectureStandalone
	}
	if mi.Database == "" {
		mi.Database = "mysql"
	}
	nodes := make([]string, 0, len(mi.Nodes))
	seen := make(map[string]bool)
	for _, node := range mi.Nodes {
		node = strings.TrimSpace(node)
		if node == "" || seen[node] {
			continue
		}
		seen[node] = true
		nodes = append(nodes, node)
	}
	mi.Nodes = nodes
}
