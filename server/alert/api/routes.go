// Package api 实现告警子系统的 HTTP 处理器(net/http + mongo-driver，无 gin 依赖)。
package api

import "net/http"

// RegisterRoutes 在子 mux 注册告警业务路由（鉴权在外层 AuthMiddlewareHandler 完成）。
// 路径规则：上游 gin `:id` → Go 1.22 `{id}`；每条路由含 HTTP 方法前缀。
// 已排除（未移植/延后）：login/captcha/logout/user、import-template/import、
// connection-test、embeds/embed-proxy、k8s-instances、test-query(QueryPromQL)。
func RegisterRoutes(mux *http.ServeMux) {
	// ── 任务管理 ──────────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/tasks", ListTasks)
	mux.HandleFunc("POST /api/alert/tasks", CreateTask)
	mux.HandleFunc("PUT /api/alert/tasks/{id}", UpdateTask)
	mux.HandleFunc("DELETE /api/alert/tasks/{id}", DeleteTask)
	mux.HandleFunc("GET /api/alert/tasks/{id}/history", GetTaskHistory)
	mux.HandleFunc("POST /api/alert/tasks/{id}/refresh", RefreshTask)
	mux.HandleFunc("POST /api/alert/tasks/{id}/pause", PauseTask)
	mux.HandleFunc("POST /api/alert/tasks/{id}/resume", ResumeTask)
	mux.HandleFunc("GET /api/alert/tasks/{id}/alert-history", GetTaskAlertHistory)

	// ── Kafka 实例管理 ────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/kafka-instances", ListKafkaInstances)
	mux.HandleFunc("POST /api/alert/kafka-instances", CreateKafkaInstance)
	mux.HandleFunc("PUT /api/alert/kafka-instances/{id}", UpdateKafkaInstance)
	mux.HandleFunc("DELETE /api/alert/kafka-instances/{id}", DeleteKafkaInstance)
	// POST /kafka-instances/test — 未移植，跳过

	// ── Kafka 重平衡监控管理 ──────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/kafka-rebalance-monitors", ListKafkaRebalanceMonitors)
	mux.HandleFunc("POST /api/alert/kafka-rebalance-monitors", CreateKafkaRebalanceMonitor)
	mux.HandleFunc("PUT /api/alert/kafka-rebalance-monitors/{id}", UpdateKafkaRebalanceMonitor)
	mux.HandleFunc("DELETE /api/alert/kafka-rebalance-monitors/{id}", DeleteKafkaRebalanceMonitor)
	mux.HandleFunc("POST /api/alert/kafka-rebalance-monitors/{id}/refresh", RefreshKafkaRebalanceMonitor)
	mux.HandleFunc("POST /api/alert/kafka-rebalance-monitors/{id}/pause", PauseKafkaRebalanceMonitor)
	mux.HandleFunc("POST /api/alert/kafka-rebalance-monitors/{id}/resume", ResumeKafkaRebalanceMonitor)
	mux.HandleFunc("GET /api/alert/kafka-rebalance-monitors/{id}/alert-history", GetKafkaRebalanceMonitorHistory)

	// ── Webhook 管理 ──────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/webhooks", ListWebhooks)
	mux.HandleFunc("POST /api/alert/webhooks", CreateWebhook)
	mux.HandleFunc("PUT /api/alert/webhooks/{id}", UpdateWebhook)
	mux.HandleFunc("DELETE /api/alert/webhooks/{id}", DeleteWebhook)
	// POST /webhooks/test — 未移植，跳过

	// ── 数据源管理 ────────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/datasources", ListDataSources)
	mux.HandleFunc("POST /api/alert/datasources", CreateDataSource)
	mux.HandleFunc("PUT /api/alert/datasources/{id}", UpdateDataSource)
	mux.HandleFunc("DELETE /api/alert/datasources/{id}", DeleteDataSource)
	mux.HandleFunc("POST /api/alert/datasources/test", TestDataSource)
	mux.HandleFunc("POST /api/alert/datasources/probe", ProbeDataSource)
	mux.HandleFunc("POST /api/alert/datasources/test-query", PreviewPromQL)

	// 自定义监控使用内置 Prometheus，面板配置与告警规则独立。
	mux.HandleFunc("POST /api/alert/monitor-dashboard/open", openMonitorDashboard)
	mux.HandleFunc("GET /api/alert/monitor-panels", listMonitorPanels)
	mux.HandleFunc("POST /api/alert/monitor-panels", saveMonitorPanel)
	mux.HandleFunc("PUT /api/alert/monitor-panels/{id}", saveMonitorPanel)
	mux.HandleFunc("DELETE /api/alert/monitor-panels/{id}", deleteMonitorPanel)
	mux.HandleFunc("POST /api/alert/promql/query", runMonitorQuery)
	mux.HandleFunc("GET /api/alert/promql/metrics", listMonitorMetrics)
	mux.HandleFunc("GET /api/alert/promql/jobs", listMonitorJobs)
	// GET  /datasources/{id}/instances    — 未移植，跳过

	// ── 数据源聚合视图(跨六类) ────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/sources", ListSources)
	mux.HandleFunc("POST /api/alert/sources/{kind}/{id}/toggle", ToggleSource)
	// 当前采集配置真相源(http=UI 数据源 / env=ops.yaml 的 ENV_*)，前端据此提示
	// 数据源页「采集状态」列：agent 上报的 exporter 实时状态
	mux.HandleFunc("GET /api/alert/sources/health", SourcesHealth)
	// 新建数据源前的同实例检测（只提示不拦截）
	mux.HandleFunc("POST /api/alert/sources/check-duplicate", CheckSourceDuplicate)
	// 1.5.0 批量导入：YAML/JSON 模板导入数据源，逐条校验 + 逐条探测连接。
	// dryRun=true 只校验不写库。取代 1.4.x 的 ops.yaml ENV_* 播种。
	mux.HandleFunc("POST /api/alert/sources/import", ImportSources)
	mux.HandleFunc("GET /api/alert/sources/import-template", ImportTemplate)
	// 1.5.9 导出：产出可被上面 import 原样吃回去的 YAML（备份 / 换环境重建）。
	// includeSecrets=true 才带明文密码，默认留空（导回同一套平台会沿用原密码）。
	mux.HandleFunc("GET /api/alert/sources/export", ExportSources)
	// 部署自检：装完/出问题时一屏看清各链路状态
	mux.HandleFunc("GET /api/alert/selfcheck", SelfCheck)

	// ── 自定义页面(把任意 Web UI 挂进菜单；反代模式见 main 的 /api/custom-proxy/) ──
	mux.HandleFunc("GET /api/alert/custom-pages", ListCustomPages)
	mux.HandleFunc("POST /api/alert/custom-pages", CreateCustomPage)
	mux.HandleFunc("PUT /api/alert/custom-pages/{id}", UpdateCustomPage)
	mux.HandleFunc("DELETE /api/alert/custom-pages/{id}", DeleteCustomPage)

	// ── 告警规则管理 ──────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/alert-rules", ListAlertRules)
	mux.HandleFunc("POST /api/alert/alert-rules", CreateAlertRule)
	mux.HandleFunc("PUT /api/alert/alert-rules/{id}", UpdateAlertRule)
	mux.HandleFunc("DELETE /api/alert/alert-rules/{id}", DeleteAlertRule)
	mux.HandleFunc("POST /api/alert/alert-rules/{id}/refresh", RefreshAlertRule)
	mux.HandleFunc("POST /api/alert/alert-rules/{id}/pause", PauseAlertRule)
	mux.HandleFunc("POST /api/alert/alert-rules/{id}/resume", ResumeAlertRule)
	mux.HandleFunc("GET /api/alert/alert-rules/{id}/instances", ListAlertRuleInstances)
	mux.HandleFunc("POST /api/alert/alert-rules/{id}/silences", CreateAlertRuleSilences)
	mux.HandleFunc("DELETE /api/alert/alert-rules/{id}/silences", CancelAlertRuleSilence)
	mux.HandleFunc("GET /api/alert/alert-rules/{id}/alert-history", GetAlertRuleHistory)
	mux.HandleFunc("GET /api/alert/metric-templates", GetMetricTemplates)
	// GET  /alert-rules/import-template   — 未移植，跳过
	// POST /alert-rules/import            — 未移植，跳过
	// POST /alert-rules/test-query        — 未移植，跳过

	// ── 端口连通性监控管理 ────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/port-monitors", ListPortMonitors)
	mux.HandleFunc("POST /api/alert/port-monitors", CreatePortMonitor)
	mux.HandleFunc("PUT /api/alert/port-monitors/{id}", UpdatePortMonitor)
	mux.HandleFunc("DELETE /api/alert/port-monitors/{id}", DeletePortMonitor)
	mux.HandleFunc("POST /api/alert/port-monitors/{id}/refresh", RefreshPortMonitor)
	mux.HandleFunc("POST /api/alert/port-monitors/{id}/pause", PausePortMonitor)
	mux.HandleFunc("POST /api/alert/port-monitors/{id}/resume", ResumePortMonitor)
	mux.HandleFunc("GET /api/alert/port-monitors/{id}/alert-history", GetPortMonitorHistory)
	// GET  /port-monitors/import-template — 未移植，跳过
	// POST /port-monitors/import          — 未移植，跳过

	// ── SSL 证书监控管理 ──────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/ssl-monitors", ListSslMonitors)
	mux.HandleFunc("POST /api/alert/ssl-monitors", CreateSslMonitor)
	mux.HandleFunc("PUT /api/alert/ssl-monitors/{id}", UpdateSslMonitor)
	mux.HandleFunc("DELETE /api/alert/ssl-monitors/{id}", DeleteSslMonitor)
	mux.HandleFunc("POST /api/alert/ssl-monitors/{id}/refresh", RefreshSslMonitor)
	mux.HandleFunc("POST /api/alert/ssl-monitors/{id}/pause", PauseSslMonitor)
	mux.HandleFunc("POST /api/alert/ssl-monitors/{id}/resume", ResumeSslMonitor)
	mux.HandleFunc("GET /api/alert/ssl-monitors/{id}/alert-history", GetSslMonitorHistory)
	// GET  /ssl-monitors/import-template  — 未移植，跳过
	// POST /ssl-monitors/import           — 未移植，跳过

	// ── MongoDB 实例管理 ──────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/mongo-instances", ListMongoInstances)
	mux.HandleFunc("POST /api/alert/mongo-instances", CreateMongoInstance)
	mux.HandleFunc("PUT /api/alert/mongo-instances/{id}", UpdateMongoInstance)
	mux.HandleFunc("DELETE /api/alert/mongo-instances/{id}", DeleteMongoInstance)
	// POST /mongo-instances/test          — 未移植，跳过

	// ── MongoDB 监控管理 ──────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/mongo-monitors", ListMongoMonitors)
	mux.HandleFunc("POST /api/alert/mongo-monitors", CreateMongoMonitor)
	mux.HandleFunc("PUT /api/alert/mongo-monitors/{id}", UpdateMongoMonitor)
	mux.HandleFunc("DELETE /api/alert/mongo-monitors/{id}", DeleteMongoMonitor)
	mux.HandleFunc("POST /api/alert/mongo-monitors/{id}/refresh", RefreshMongoMonitor)
	mux.HandleFunc("POST /api/alert/mongo-monitors/{id}/pause", PauseMongoMonitor)
	mux.HandleFunc("POST /api/alert/mongo-monitors/{id}/resume", ResumeMongoMonitor)
	mux.HandleFunc("GET /api/alert/mongo-monitors/{id}/alert-history", GetMongoMonitorHistory)
	mux.HandleFunc("GET /api/alert/mongo-monitors/{id}/instances", ListMongoMonitorInstances)
	mux.HandleFunc("POST /api/alert/mongo-monitors/{id}/silences", CreateMongoMonitorSilences)
	mux.HandleFunc("DELETE /api/alert/mongo-monitors/{id}/silences", CancelMongoMonitorSilence)
	// GET  /mongo-monitors/import-template — 未移植，跳过
	// POST /mongo-monitors/import          — 未移植，跳过

	// ── MySQL 实例管理 ────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/mysql-instances", ListMySQLInstances)
	mux.HandleFunc("POST /api/alert/mysql-instances", CreateMySQLInstance)
	mux.HandleFunc("PUT /api/alert/mysql-instances/{id}", UpdateMySQLInstance)
	mux.HandleFunc("DELETE /api/alert/mysql-instances/{id}", DeleteMySQLInstance)
	// POST /mysql-instances/test          — 未移植，跳过

	// ── Redis 实例管理(仅看图) ────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/redis-instances", ListRedisInstances)
	mux.HandleFunc("POST /api/alert/redis-instances", CreateRedisInstance)
	mux.HandleFunc("PUT /api/alert/redis-instances/{id}", UpdateRedisInstance)
	mux.HandleFunc("DELETE /api/alert/redis-instances/{id}", DeleteRedisInstance)

	// ── Elasticsearch 实例管理(仅看图) ────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/es-instances", ListESInstances)
	mux.HandleFunc("POST /api/alert/es-instances", CreateESInstance)
	mux.HandleFunc("PUT /api/alert/es-instances/{id}", UpdateESInstance)
	mux.HandleFunc("DELETE /api/alert/es-instances/{id}", DeleteESInstance)

	// ── 主机实例管理(node_exporter，SD 直接输出主机地址) ──────────────────────
	mux.HandleFunc("GET /api/alert/host-instances", ListHostInstances)
	mux.HandleFunc("POST /api/alert/host-instances", CreateHostInstance)
	mux.HandleFunc("PUT /api/alert/host-instances/{id}", UpdateHostInstance)
	mux.HandleFunc("DELETE /api/alert/host-instances/{id}", DeleteHostInstance)

	// ── MySQL 监控管理 ────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/mysql-monitors", ListMySQLMonitors)
	mux.HandleFunc("POST /api/alert/mysql-monitors", CreateMySQLMonitor)
	mux.HandleFunc("PUT /api/alert/mysql-monitors/{id}", UpdateMySQLMonitor)
	mux.HandleFunc("DELETE /api/alert/mysql-monitors/{id}", DeleteMySQLMonitor)
	mux.HandleFunc("POST /api/alert/mysql-monitors/{id}/refresh", RefreshMySQLMonitor)
	mux.HandleFunc("POST /api/alert/mysql-monitors/{id}/pause", PauseMySQLMonitor)
	mux.HandleFunc("POST /api/alert/mysql-monitors/{id}/resume", ResumeMySQLMonitor)
	mux.HandleFunc("GET /api/alert/mysql-monitors/{id}/alert-history", GetMySQLMonitorHistory)
	mux.HandleFunc("GET /api/alert/mysql-monitors/{id}/instances", ListMySQLMonitorInstances)
	mux.HandleFunc("POST /api/alert/mysql-monitors/{id}/silences", CreateMySQLMonitorSilences)
	mux.HandleFunc("DELETE /api/alert/mysql-monitors/{id}/silences", CancelMySQLMonitorSilence)
	// GET  /mysql-monitors/import-template — 未移植，跳过
	// POST /mysql-monitors/import          — 未移植，跳过

	// ── 全局告警历史 ──────────────────────────────────────────────────────────
	mux.HandleFunc("GET /api/alert/alert-history", GetGlobalAlertHistory)
	mux.HandleFunc("POST /api/alert/alert-history/batch-delete", DeleteGlobalAlertHistory)
	mux.HandleFunc("POST /api/alert/alert-history/delete-all", DeleteAllGlobalAlertHistory)
	mux.HandleFunc("GET /api/alert/alert-history/source-types", GetAlertHistorySourceTypes)
}
