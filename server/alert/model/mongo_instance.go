package model

import (
	"strings"
	"time"
)

// MongoLogPurpose enables periodic collection of the server log buffer.
const MongoLogPurpose = "logs"

// MongoInstance MongoDB 实例配置模型
type MongoInstance struct {
	ID               string            `bson:"_id,omitempty" json:"id"`
	Kind             string            `bson:"kind,omitempty" json:"-"`
	Name             string            `bson:"name"          json:"name"`
	URI              string            `bson:"uri"           json:"uri"`      // mongodb://... 或 mongodb+srv://...
	Username         string            `bson:"username"      json:"username"` // 可选，覆盖 URI 中未指定的凭据
	Password         string            `bson:"password"      json:"password"` // 可选
	AuthDB           string            `bson:"authDb"        json:"authDb"`   // 认证数据库
	TLS              bool              `bson:"tls"           json:"tls"`      // 是否启用 TLS
	SkipSSL          bool              `bson:"skipSSL"       json:"skipSSL"`  // InsecureSkipVerify
	Enabled          bool              `bson:"enabled"       json:"enabled"`
	Builtin          bool              `bson:"builtin"       json:"builtin"`
	MetricCollection *MetricCollection `bson:"metricCollection,omitempty" json:"metricCollection,omitempty"`
	Purposes         []string          `bson:"purposes"      json:"purposes"` // metrics / alert / diagnose / logs
	CreatedAt        time.Time         `bson:"createdAt"     json:"createdAt"`
	UpdatedAt        time.Time         `bson:"updatedAt"     json:"updatedAt"`
}

// Sanitize 清除敏感字段，用于 API 响应输出
func (mi *MongoInstance) Sanitize() {
	if mi.Password != "" {
		mi.Password = MaskValue
	}
	// URI 中可能包含嵌入的凭据（mongodb://user:pass@host），也需要脱敏
	if mi.URI != "" {
		mi.URI = sanitizeMongoURI(mi.URI)
	}
}

// mongoURIParts 切分 mongodb://user:pass@rest 为四段。
type mongoURIParts struct {
	scheme   string // 不含 "://"，例如 "mongodb" / "mongodb+srv"
	user     string
	password string
	rest     string // @ 之后到末尾（host[:port][/db][?opts]）
}

func splitMongoURI(uri string) (mongoURIParts, bool) {
	scheme, body, ok := strings.Cut(uri, "://")
	if !ok {
		return mongoURIParts{}, false
	}
	userInfo, rest, ok := strings.Cut(body, "@")
	if !ok {
		return mongoURIParts{}, false
	}
	user, password, ok := strings.Cut(userInfo, ":")
	if !ok {
		return mongoURIParts{}, false
	}
	return mongoURIParts{
		scheme:   scheme,
		user:     user,
		password: password,
		rest:     rest,
	}, true
}

func buildMongoURI(p mongoURIParts) string {
	return p.scheme + "://" + p.user + ":" + p.password + "@" + p.rest
}

// sanitizeMongoURI 脱敏 MongoDB URI 中的密码部分。
func sanitizeMongoURI(uri string) string {
	parts, ok := splitMongoURI(uri)
	if !ok {
		return uri
	}
	parts.password = MaskValue
	return buildMongoURI(parts)
}

// RestoreMongoURIPassword 将 masked URI 中等于 MaskValue 的密码字段，
// 用 original URI 中的密码还原。用于解决前端回写脱敏 URI 导致密码丢失的问题。
func RestoreMongoURIPassword(masked, original string) string {
	maskedParts, ok := splitMongoURI(masked)
	if !ok || maskedParts.password != MaskValue {
		return masked
	}
	originalParts, ok := splitMongoURI(original)
	if !ok {
		return masked
	}
	maskedParts.password = originalParts.password
	return buildMongoURI(maskedParts)
}
