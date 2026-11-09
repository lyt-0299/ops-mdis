// Package model 定义告警子系统的数据模型(mongo/bson + JSON camelCase)。
package model

// BuiltinPrometheusID 是内置 Prometheus 数据源单例的固定 _id。
const BuiltinPrometheusID = "builtin-prometheus"

// DataSource 数据源配置(存于 alert_config 集合，discriminator: kind="source")。
// 密码/Token 以密文形式存储(crypto.Encrypt)，读取时不在 API 响应中返回。
// ID 以应用生成的 hex string 直接映射到 mongo _id。
type DataSource struct {
	ID       string `bson:"_id,omitempty" json:"id"`
	Kind     string `bson:"kind,omitempty" json:"-"`
	Name     string `bson:"name"          json:"name"`
	Type     string `bson:"type"          json:"type"`     // 如 prometheus；默认 prometheus
	URL      string `bson:"url"           json:"url"`
	AuthType string `bson:"authType"      json:"authType"` // none / basic / bearer
	Username string `bson:"username"      json:"username"`
	Password string `bson:"password"      json:"password"` // 密文(crypto.Encrypt)；API 响应中置空
	Token    string `bson:"token"         json:"token"`    // 密文；API 响应中置空
	SkipSSL  bool   `bson:"skipSSL"       json:"skipSSL"`

	Enabled  bool     `bson:"enabled"  json:"enabled"`
	Purposes []string `bson:"purposes" json:"purposes"` // 取值 "metrics" / "alert"
	Builtin  bool     `bson:"builtin"  json:"builtin"`  // 内置单例(只读、受管)
}
