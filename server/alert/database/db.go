// Package database 提供告警子系统的 mongo 集合句柄(连专用 ops-mongo,与被监控目标解耦)。
package database

import (
	"context"
	"errors"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/collname"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

var (
	client *mongo.Client
	db     *mongo.Database
)

// Init 连接专用 ops-mongo(ENV_OPS_MONGODB_URI)并绑定库句柄。
// 告警数据存自有 ops-mongo,不复用 common 里连被监控目标的客户端。
func Init() error {
	uri := os.Getenv("ENV_OPS_MONGODB_URI")
	if uri == "" {
		return errors.New("ENV_OPS_MONGODB_URI 未设置(告警自有 ops-mongo)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	if err := c.Ping(ctx, nil); err != nil {
		_ = c.Disconnect(context.Background()) // Ping 失败即断开,避免重试时泄漏 client
		return err
	}
	client = c
	db = c.Database("mdservicedata")
	return nil
}

// DB 返回库句柄(测试/调试用)。
func DB() *mongo.Database { return db }

func Config() *mongo.Collection  { return db.Collection(collname.AlertConfig()) }
func State() *mongo.Collection   { return db.Collection(collname.AlertState()) }
func History() *mongo.Collection { return db.Collection(collname.AlertHistory()) }

// EnsureBuiltinPrometheus upsert 内置 Prometheus 数据源单例。
// 受管字段(type/name/url/builtin/enabled/purposes/authType)每次启动用 env 覆盖,
// URL 取 ENV_PROMETHEUS_INTERNAL_URL,防止漂移;默认含 ops-prometheus 的
// route-prefix(/prometheus/server),即 API 在 <url>/api/v1/query。
func EnsureBuiltinPrometheus() error {
	url := os.Getenv("ENV_PROMETHEUS_INTERNAL_URL")
	if url == "" {
		url = "http://ops-prometheus:9090/prometheus/server"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"_id": model.BuiltinPrometheusID, "kind": kindDataSource}
	update := bson.M{
		"$set": bson.M{
			"kind":     kindDataSource,
			"type":     "prometheus",
			"name":     model.BuiltinPrometheusID,
			"url":      url,
			"builtin":  true,
			"enabled":  true,
			"purposes": []string{"alert"},
			"authType": "none",
		},
		"$setOnInsert": bson.M{
			"_id": model.BuiltinPrometheusID,
		},
	}
	_, err := Config().UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

// 1.5.0 移除 EnsureBuiltinDiagnoseMongo：
// 它曾把 ENV_MONGODB_URI 显形成一条 builtin=true 的 diagnose 源。但 MongoDB 本质是
// 「被监控对象」，却因为 builtin 身份在 UI 上没有编辑按钮——地址要改时页面上改不了、
// 改 ENV 也不生效，两条路同时堵死。现在它就是一条普通数据源：用户自己添加、勾「诊断」
// 用途，慢查询从 purposes 含 diagnose 的实例里取。builtin 只留给内置 Prometheus
// （那才是平台自带的基础设施，而不是被监控对象）。

// kindDataSource 是数据源在 alert_config 集合的 discriminator 值(与 api 包保持一致)。
const kindDataSource = "source"
