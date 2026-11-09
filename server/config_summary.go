package main

import (
	"log"
	"os"
	"sort"
	"strings"

	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// ============================================================================
// 配置来源与启动自检。
//
// 1.5.0 起本平台只有一个数据源入口：UI「数据源」页（含 YAML 批量导入）。
// ops.yaml 只描述「平台自己要用什么」，不再描述「要监控谁」——判据就这一条。
//
// 历史（1.4.x）：ops.yaml 的 ENV_MYSQL_*/ENV_REDIS_*/… 会在首次启动时被播种成
// 数据源，之后改它不生效。这个「配一次就固化」的隐性状态是最难排查的一类问题：
// 配置文件明明摆在那里，内容却早已不作数，改了没反应且零提示。1.5.0 整段删除，
// 换成导入即验证的批量导入，不再有两套入口、两套语法、两处真相。
// ============================================================================

// deprecatedSourceEnvs 是 1.5.0 起不再被读取的「被监控对象」环境变量。
// 它们曾经能配出数据源，现在一个字节都不会被使用——必须显式告警，
// 否则又变成「配了没反应」的静默失败（正是本次要根除的东西）。
var deprecatedSourceEnvs = []string{
	"ENV_MYSQL_HOST", "ENV_MYSQL_PORT", "ENV_MYSQL_USERNAME", "ENV_MYSQL_PASSWORD",
	"ENV_MONGODB_URI", "ENV_MONGODB_OPTIONS",
	"ENV_MONGODB_URI_WSROWS", "ENV_MONGODB_WSROWS_EXTEND_URI",
	"ENV_REDIS_HOST", "ENV_REDIS_PORT", "ENV_REDIS_PASSWORD",
	"ENV_KAFKA_ENDPOINTS",
	"ENV_ELASTICSEARCH_ENDPOINTS", "ENV_ELASTICSEARCH_PASSWORD",
	"ENV_FLINK_URL",
	"ENV_PROMETHEUS_HOST",
	"ENV_PROMETHEUS_MYSQL", "ENV_PROMETHEUS_REDIS", "ENV_PROMETHEUS_MONGODB",
	"ENV_PROMETHEUS_KAFKA", "ENV_PROMETHEUS_ELASTICSEARCH",
	"ENV_PROMETHEUS_K8S_KSM", "ENV_PROMETHEUS_K8S_NODE", "ENV_PROMETHEUS_K8S_KUBELET",
	"ENV_PROMETHEUS_SD_MODE",
}

// warnDeprecatedSourceEnvs 启动时扫一遍已废弃的数据源类变量，配了就明确告知去哪配。
// 只读不改，也不阻止启动——ops.yaml 里留着这些行不影响运行，只是不再有任何效果。
func warnDeprecatedSourceEnvs() {
	var found []string
	for _, k := range deprecatedSourceEnvs {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			found = append(found, k)
		}
	}
	if len(found) == 0 {
		return
	}
	sort.Strings(found)
	log.Printf("[config] ⚠️ ops.yaml 里这 %d 个变量已废弃、不再被读取：%s", len(found), strings.Join(found, " "))
	log.Printf("[config] ⚠️ 被监控对象一律在 UI「数据源」页配置（支持下载 YAML 模板批量导入，导入时逐条测连接）。" +
		"ops.yaml 现在只描述平台自身依赖（ENV_OPS_* / ENV_PROMETHEUS_SERVER / ENV_LOKI_URL / ENV_S3_* 等），删掉上面这些行即可。")
}

// ============================================================================
// logConfigSummary：启动时把「谁是配置的真相源」「几个同名/近名变量各是什么」打清楚。
//
// 真实客户部署反馈里，最耗时间的困惑出在命名相近的变量上：
//
//	· ENV_OPS_MONGODB_URI（平台自己的库）—— 1.5.0 后已无 ENV_MONGODB_URI 与之混淆
//	· HAP 侧的 ENV_LOKI_URL（日志写入开关）vs 本平台的 ENV_LOKI_URL（日志查询地址）——完全同名
//
// 排查的人第一件事就是 docker logs，把这些直接摆在启动日志里，能省掉一轮来回。
// ============================================================================
func logConfigSummary() {
	log.Printf("[config] 采集配置来源 = UI「数据源」页（唯一来源）。" +
		"被监控对象全部在该页维护，支持 YAML 模板批量导入；ops.yaml 不再参与数据源配置。")

	show := func(v string) string {
		if v == "" {
			return "(未配置)"
		}
		return v
	}
	log.Printf("[config] ENV_OPS_MONGODB_URI = %s  ← 平台自有库，存告警规则/数据源/慢查询历史，不是被监控对象",
		show(model.SanitizeMongoURIForDisplay(os.Getenv("ENV_OPS_MONGODB_URI"))))
	log.Printf("[config] ENV_LOKI_URL        = %s  ← 本平台去哪读日志。⚠️ HAP 私有部署侧另有同名变量，"+
		"那个才是「HAP 要不要把服务日志写给 Loki」的开关，不配则服务日志页为空",
		show(os.Getenv("ENV_LOKI_URL")))

	warnDeprecatedSourceEnvs()
}
