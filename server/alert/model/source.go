package model

// 六类数据源在 alert_config 集合的 discriminator(kind)取值。
const (
	KindSource        = "source" // Prometheus(含内置单例)
	KindKafkaInstance = "kafkaInstance"
	KindMongoInstance = "mongoInstance"
	KindMySQLInstance = "mysqlInstance"
	KindRedisInstance = "redisInstance"
	KindESInstance    = "esInstance"
)

// SourceKinds 是聚合列表/启停端点允许的全部 kind 集合。
var SourceKinds = []string{
	KindSource,
	KindKafkaInstance,
	KindMongoInstance,
	KindMySQLInstance,
	KindRedisInstance,
	KindESInstance,
	KindHostInstance,
}

// kindToType 把 discriminator kind 映射为面向前端的 type。
// source(Prometheus)的 type 取文档自身的 Type 字段，不走此表。
var kindToType = map[string]string{
	KindKafkaInstance:      "kafka",
	KindMongoInstance:      "mongodb",
	KindMySQLInstance:      "mysql",
	KindRedisInstance:      "redis",
	KindESInstance:         "elasticsearch",
	KindHostInstance:       "host",
}

// IsValidSourceKind 校验 kind 是否在允许集合内。
func IsValidSourceKind(kind string) bool {
	_, ok := kindToType[kind]
	return ok || kind == KindSource
}

// TypeForKind 返回 kind 对应的 type；source 由调用方用文档 Type 字段补齐。
func TypeForKind(kind string) string {
	return kindToType[kind]
}

// UnifiedSource 是六类数据源的统一只读视图(Detail 已脱敏，绝不含明文密码/Token)。
type UnifiedSource struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"` // source/kafkaInstance/...
	Type     string         `json:"type"` // prometheus/kafka/mongodb/mysql/redis/elasticsearch
	Name     string         `json:"name"`
	Enabled  bool           `json:"enabled"`
	Purposes []string       `json:"purposes"`
	Builtin  bool           `json:"builtin"`
	Detail   map[string]any `json:"detail"` // 脱敏后的关键连接信息
}

// SanitizeMongoURIForDisplay 复用 MongoInstance.Sanitize 的 URI 脱敏逻辑，
// 对外暴露给 api 包做统一视图 Detail 的 uri 脱敏。
func SanitizeMongoURIForDisplay(uri string) string {
	if uri == "" {
		return ""
	}
	return sanitizeMongoURI(uri)
}
