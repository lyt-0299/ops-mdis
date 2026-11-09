/**
 * alertApi.js — API module for the alert subsystem.
 *
 * Convention: all alert API paths are prefixed with /alert.
 * This file is the single source of truth for the /alert/* surface.
 *
 * The axios instance is imported from api.js. It already has:
 *   - baseURL = /api  (resolved at runtime from <base> tag)
 *   - 401 → redirect to /auth
 *   - 20 s timeout
 */

import axios from 'axios';

import { API_BASE_PATH } from '../config';

// ─── shared axios instance (mirrors api.js, avoids circular import) ────────
const alertAxios = axios.create({
  baseURL: API_BASE_PATH,
  timeout: 20000,
});

alertAxios.interceptors.response.use(
  (response) => {
    if (response.status === 200) return response;
    return Promise.reject(response);
  },
  (error) => {
    if (error.status === 401 || (error.response && error.response.status === 401)) {
      const base = (document.querySelector('base')?.getAttribute('href') || '/web/').replace(/\/$/, '');
      const authPath = `${base}/auth`;
      if (authPath !== window.location.pathname) {
        // 立即跳转(不延迟),避免未登录时先渲染主框架再跳登录的空白闪烁;
        // replace 不在历史留未登录 URL
        window.location.replace(authPath);
      }
    }
    return Promise.reject(error);
  }
);

const get = (url, params = {}, config = {}) =>
  alertAxios.get(url, { params, ...config });

const post = (url, data = {}, config = {}) =>
  alertAxios.post(url, data, config);

const put = (url, data = {}, config = {}) =>
  alertAxios.put(url, data, config);

const del = (url, config = {}) =>
  alertAxios.delete(url, config);

// ─────────────────────────────────────────────────────────────────────────────
// § Data Sources  GET/POST /alert/datasources  PUT/DELETE /alert/datasources/:id
// Fields: id, name, type("prometheus"), url, authType(none/basic/bearer),
//         username, password, token, skipSSL
// List response returns password/token blanked.
// ─────────────────────────────────────────────────────────────────────────────

/** List all Prometheus data sources. */
export const listDataSources = () => get('/alert/datasources');

/** Create a new data source. */
export const createDataSource = (payload) => post('/alert/datasources', payload);

/** Update an existing data source. */
export const updateDataSource = (id, payload) =>
  put(`/alert/datasources/${id}`, payload);

/** Delete a data source. */
export const deleteDataSource = (id) => del(`/alert/datasources/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § Webhooks / Notification Channels
// GET/POST /alert/webhooks  PUT/DELETE /alert/webhooks/:id
// Fields: id, name, type(feishu/dingtalk/wecom/email/custom),
//         url, proxyUrl, mentionedList, mentionedMobileList, isAtAll,
//         smtpHost, smtpPort, smtpUser, smtpPass, skipSSL, emailTo
// ─────────────────────────────────────────────────────────────────────────────

/** List all notification channels (webhooks). */
export const listWebhooks = () => get('/alert/webhooks');

/** Create a notification channel. */
export const createWebhook = (payload) => post('/alert/webhooks', payload);

/** Update a notification channel. */
export const updateWebhook = (id, payload) => put(`/alert/webhooks/${id}`, payload);

/** Delete a notification channel. */
export const deleteWebhook = (id) => del(`/alert/webhooks/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § Kafka Instances  GET/POST /alert/kafka-instances  PUT/DELETE /:id
// ─────────────────────────────────────────────────────────────────────────────

/** List Kafka instances. */
export const listKafkaInstances = () => get('/alert/kafka-instances');

/** Create a Kafka instance. */
export const createKafkaInstance = (payload) => post('/alert/kafka-instances', payload);

/** Update a Kafka instance. */
export const updateKafkaInstance = (id, payload) => put(`/alert/kafka-instances/${id}`, payload);

/** Delete a Kafka instance. */
export const deleteKafkaInstance = (id) => del(`/alert/kafka-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § Kafka Lag Tasks (Kafka 堆积)
// GET/POST /alert/tasks  PUT/DELETE /:id
// POST /:id/refresh  POST /:id/pause  POST /:id/resume
// GET  /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List all Kafka lag tasks. */
export const listTasks = () => get('/alert/tasks');

/** Create a Kafka lag task. */
export const createTask = (payload) => post('/alert/tasks', payload);

/** Update a Kafka lag task. */
export const updateTask = (id, payload) => put(`/alert/tasks/${id}`, payload);

/** Delete a Kafka lag task. */
export const deleteTask = (id) => del(`/alert/tasks/${id}`);

/** Manually refresh a Kafka lag task's check. */
export const refreshTask = (id) => post(`/alert/tasks/${id}/refresh`);

/** Pause a Kafka lag task. payload: { mode, paused_until?, reason? } (backend pauseRequest uses snake_case) */
export const pauseTask = (id, payload) => post(`/alert/tasks/${id}/pause`, payload);

/** Resume a paused Kafka lag task. */
export const resumeTask = (id) => post(`/alert/tasks/${id}/resume`);

/** Get alert history for a Kafka lag task. */
export const getTaskAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/tasks/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § Kafka Rebalance Monitors
// GET/POST /alert/kafka-rebalance-monitors  PUT/DELETE /:id
// POST /:id/refresh|pause|resume  GET /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List all Kafka rebalance monitors. */
export const listKafkaRebalanceMonitors = () => get('/alert/kafka-rebalance-monitors');

/** Create a Kafka rebalance monitor. */
export const createKafkaRebalanceMonitor = (payload) =>
  post('/alert/kafka-rebalance-monitors', payload);

/** Update a Kafka rebalance monitor. */
export const updateKafkaRebalanceMonitor = (id, payload) =>
  put(`/alert/kafka-rebalance-monitors/${id}`, payload);

/** Delete a Kafka rebalance monitor. */
export const deleteKafkaRebalanceMonitor = (id) =>
  del(`/alert/kafka-rebalance-monitors/${id}`);

/** Refresh a Kafka rebalance monitor. */
export const refreshKafkaRebalanceMonitor = (id) =>
  post(`/alert/kafka-rebalance-monitors/${id}/refresh`);

/** Pause a Kafka rebalance monitor. */
export const pauseKafkaRebalanceMonitor = (id, payload) =>
  post(`/alert/kafka-rebalance-monitors/${id}/pause`, payload);

/** Resume a Kafka rebalance monitor. */
export const resumeKafkaRebalanceMonitor = (id) =>
  post(`/alert/kafka-rebalance-monitors/${id}/resume`);

/** Get alert history for a Kafka rebalance monitor. */
export const getKafkaRebalanceMonitorAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/kafka-rebalance-monitors/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § Alert Rules (基础资源 / Prometheus)
// GET/POST /alert/alert-rules  PUT/DELETE /:id
// POST /:id/refresh|pause|resume
// GET  /:id/instances
// POST /:id/silences  DELETE /:id/silences (with ?scope_key=)
// GET  /:id/alert-history
// GET  /alert/metric-templates
// ─────────────────────────────────────────────────────────────────────────────

/** List alert rules (optional category filter). */
export const listAlertRules = (params = {}) => get('/alert/alert-rules', params);

/** Create an alert rule. */
export const createAlertRule = (payload) => post('/alert/alert-rules', payload);

/** Update an alert rule. */
export const updateAlertRule = (id, payload) => put(`/alert/alert-rules/${id}`, payload);

/** Delete an alert rule. */
export const deleteAlertRule = (id) => del(`/alert/alert-rules/${id}`);

/** Manually refresh an alert rule's evaluation. */
export const refreshAlertRule = (id) => post(`/alert/alert-rules/${id}/refresh`);

/** Pause an alert rule. */
export const pauseAlertRule = (id, payload) => post(`/alert/alert-rules/${id}/pause`, payload);

/** Resume an alert rule. */
export const resumeAlertRule = (id) => post(`/alert/alert-rules/${id}/resume`);

/** List per-instance states for an alert rule. */
export const listAlertRuleInstances = (id) => get(`/alert/alert-rules/${id}/instances`);

/**
 * Create silences for specific instances of an alert rule.
 * payload: { scopeKeys: string[], mode: 'temporary'|'permanent', muted_until?, reason? }
 * (AlertSilenceRequest struct uses snake_case JSON tags)
 */
export const createAlertRuleSilences = (id, payload) =>
  post(`/alert/alert-rules/${id}/silences`, payload);

/**
 * Cancel a silence for a specific instance of an alert rule.
 * @param {string} scopeKey - the scope_key of the instance to un-silence
 */
export const cancelAlertRuleSilence = (id, scopeKey) =>
  del(`/alert/alert-rules/${id}/silences`, {
    params: { scopeKey: scopeKey },
  });

/** Get alert history for an alert rule. */
export const getAlertRuleAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/alert-rules/${id}/alert-history`, { page, pageSize: pageSize });

/** Get all metric templates for alert rule creation. */
export const getMetricTemplates = () => get('/alert/metric-templates');

// ─────────────────────────────────────────────────────────────────────────────
// § Port Monitors
// GET/POST /alert/port-monitors  PUT/DELETE /:id
// POST /:id/refresh|pause|resume  GET /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List all port connectivity monitors. */
export const listPortMonitors = () => get('/alert/port-monitors');

/** Create a port monitor. */
export const createPortMonitor = (payload) => post('/alert/port-monitors', payload);

/** Update a port monitor. */
export const updatePortMonitor = (id, payload) => put(`/alert/port-monitors/${id}`, payload);

/** Delete a port monitor. */
export const deletePortMonitor = (id) => del(`/alert/port-monitors/${id}`);

/** Refresh a port monitor. */
export const refreshPortMonitor = (id) => post(`/alert/port-monitors/${id}/refresh`);

/** Pause a port monitor. */
export const pausePortMonitor = (id, payload) => post(`/alert/port-monitors/${id}/pause`, payload);

/** Resume a port monitor. */
export const resumePortMonitor = (id) => post(`/alert/port-monitors/${id}/resume`);

/** Get alert history for a port monitor. */
export const getPortMonitorAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/port-monitors/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § SSL Monitors
// GET/POST /alert/ssl-monitors  PUT/DELETE /:id
// POST /:id/refresh|pause|resume  GET /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List all SSL certificate monitors. */
export const listSslMonitors = () => get('/alert/ssl-monitors');

/** Create an SSL monitor. */
export const createSslMonitor = (payload) => post('/alert/ssl-monitors', payload);

/** Update an SSL monitor. */
export const updateSslMonitor = (id, payload) => put(`/alert/ssl-monitors/${id}`, payload);

/** Delete an SSL monitor. */
export const deleteSslMonitor = (id) => del(`/alert/ssl-monitors/${id}`);

/** Refresh an SSL monitor. */
export const refreshSslMonitor = (id) => post(`/alert/ssl-monitors/${id}/refresh`);

/** Pause an SSL monitor. */
export const pauseSslMonitor = (id, payload) => post(`/alert/ssl-monitors/${id}/pause`, payload);

/** Resume an SSL monitor. */
export const resumeSslMonitor = (id) => post(`/alert/ssl-monitors/${id}/resume`);

/** Get alert history for an SSL monitor. */
export const getSslMonitorAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/ssl-monitors/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § MongoDB Instances
// GET/POST /alert/mongo-instances  PUT/DELETE /:id
// ─────────────────────────────────────────────────────────────────────────────

/** List MongoDB instances. */
export const listMongoInstances = () => get('/alert/mongo-instances');

/** Create a MongoDB instance. */
export const createMongoInstance = (payload) => post('/alert/mongo-instances', payload);

/** Update a MongoDB instance. */
export const updateMongoInstance = (id, payload) => put(`/alert/mongo-instances/${id}`, payload);

/** Delete a MongoDB instance. */
export const deleteMongoInstance = (id) => del(`/alert/mongo-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § MongoDB Monitors
// GET/POST /alert/mongo-monitors  PUT/DELETE /:id
// POST /:id/refresh|pause|resume
// GET  /:id/instances
// POST /:id/silences  DELETE /:id/silences
// GET  /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List MongoDB monitors. */
export const listMongoMonitors = () => get('/alert/mongo-monitors');

/** Create a MongoDB monitor. */
export const createMongoMonitor = (payload) => post('/alert/mongo-monitors', payload);

/** Update a MongoDB monitor. */
export const updateMongoMonitor = (id, payload) => put(`/alert/mongo-monitors/${id}`, payload);

/** Delete a MongoDB monitor. */
export const deleteMongoMonitor = (id) => del(`/alert/mongo-monitors/${id}`);

/** Refresh a MongoDB monitor. */
export const refreshMongoMonitor = (id) => post(`/alert/mongo-monitors/${id}/refresh`);

/** Pause a MongoDB monitor. */
export const pauseMongoMonitor = (id, payload) => post(`/alert/mongo-monitors/${id}/pause`, payload);

/** Resume a MongoDB monitor. */
export const resumeMongoMonitor = (id) => post(`/alert/mongo-monitors/${id}/resume`);

/** List per-instance states for a MongoDB monitor. */
export const listMongoMonitorInstances = (id) => get(`/alert/mongo-monitors/${id}/instances`);

/** Create silences for a MongoDB monitor. */
export const createMongoMonitorSilences = (id, payload) =>
  post(`/alert/mongo-monitors/${id}/silences`, payload);

/** Cancel a silence for a MongoDB monitor instance. */
export const cancelMongoMonitorSilence = (id, scopeKey) =>
  del(`/alert/mongo-monitors/${id}/silences`, { params: { scopeKey: scopeKey } });

/** Get alert history for a MongoDB monitor. */
export const getMongoMonitorAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/mongo-monitors/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § MySQL Instances
// GET/POST /alert/mysql-instances  PUT/DELETE /:id
// ─────────────────────────────────────────────────────────────────────────────

/** List MySQL instances. */
export const listMysqlInstances = () => get('/alert/mysql-instances');

/** Create a MySQL instance. */
export const createMysqlInstance = (payload) => post('/alert/mysql-instances', payload);

/** Update a MySQL instance. */
export const updateMysqlInstance = (id, payload) => put(`/alert/mysql-instances/${id}`, payload);

/** Delete a MySQL instance. */
export const deleteMysqlInstance = (id) => del(`/alert/mysql-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § MySQL Monitors
// GET/POST /alert/mysql-monitors  PUT/DELETE /:id
// POST /:id/refresh|pause|resume
// GET  /:id/instances
// POST /:id/silences  DELETE /:id/silences
// GET  /:id/alert-history
// ─────────────────────────────────────────────────────────────────────────────

/** List MySQL monitors. */
export const listMysqlMonitors = () => get('/alert/mysql-monitors');

/** Create a MySQL monitor. */
export const createMysqlMonitor = (payload) => post('/alert/mysql-monitors', payload);

/** Update a MySQL monitor. */
export const updateMysqlMonitor = (id, payload) => put(`/alert/mysql-monitors/${id}`, payload);

/** Delete a MySQL monitor. */
export const deleteMysqlMonitor = (id) => del(`/alert/mysql-monitors/${id}`);

/** Refresh a MySQL monitor. */
export const refreshMysqlMonitor = (id) => post(`/alert/mysql-monitors/${id}/refresh`);

/** Pause a MySQL monitor. */
export const pauseMysqlMonitor = (id, payload) => post(`/alert/mysql-monitors/${id}/pause`, payload);

/** Resume a MySQL monitor. */
export const resumeMysqlMonitor = (id) => post(`/alert/mysql-monitors/${id}/resume`);

/** List per-instance states for a MySQL monitor. */
export const listMysqlMonitorInstances = (id) => get(`/alert/mysql-monitors/${id}/instances`);

/** Create silences for a MySQL monitor. */
export const createMysqlMonitorSilences = (id, payload) =>
  post(`/alert/mysql-monitors/${id}/silences`, payload);

/** Cancel a silence for a MySQL monitor instance. */
export const cancelMysqlMonitorSilence = (id, scopeKey) =>
  del(`/alert/mysql-monitors/${id}/silences`, { params: { scopeKey: scopeKey } });

/** Get alert history for a MySQL monitor. */
export const getMysqlMonitorAlertHistory = (id, page = 1, pageSize = 20) =>
  get(`/alert/mysql-monitors/${id}/alert-history`, { page, pageSize: pageSize });

// ─────────────────────────────────────────────────────────────────────────────
// § Global Alert History
// GET    /alert/alert-history          (paginated, filterable)
// POST   /alert/alert-history/batch-delete
// POST   /alert/alert-history/delete-all
// GET    /alert/alert-history/source-types
// ─────────────────────────────────────────────────────────────────────────────

/**
 * List global alert history with optional filters.
 * Query params are camelCase to match the backend handler:
 * @param {Object} params - { sourceType, sourceName, alertState, startTime,
 *                            endTime, sort('asc'|'desc'), page, pageSize }
 */
export const listGlobalAlertHistory = (params = {}) =>
  get('/alert/alert-history', params);

/**
 * Batch delete alert history records by IDs.
 * @param {number[]} ids
 */
export const batchDeleteAlertHistory = (ids) =>
  post('/alert/alert-history/batch-delete', { ids });

/**
 * Delete all alert history matching optional filter params.
 * @param {Object} params - same filter keys as listGlobalAlertHistory (no pagination)
 */
export const deleteAllAlertHistory = (params = {}) =>
  post('/alert/alert-history/delete-all', params);

/** Get available source_type values for the history filter dropdown. */
export const getAlertHistorySourceTypes = () =>
  get('/alert/alert-history/source-types');

// ─────────────────────────────────────────────────────────────────────────────
// § Aggregated Data Sources (启用态聚合)
// GET  /alert/sources
//   → bare array [{ id, kind, type, name, enabled, purposes, builtin, detail }]
// POST /alert/sources/:kind/:id/toggle  body { enabled }
//   → { ok: true }
// ─────────────────────────────────────────────────────────────────────────────

/** List all aggregated data sources (mixed kinds) with enabled state. */
export const listSources = () => get('/alert/sources');

/** 批量导入数据源。body 是 YAML 或 JSON 文本（同构，字段名与各数据源 API 一致）。
 *  dryRun=true 只做校验 + 连接探测、不写库，用于导入前预检。
 *  返回 { total, created, updated, failed, dryRun, results:[{index,kind,name,action,
 *  errorKey,detail,connectOK,connectError}] } —— 逐条结果，单条失败不影响其余条目。 */
export const importSources = (text, { dryRun = false } = {}) =>
  post('/alert/sources/import', text, {
    params: { dryRun: dryRun ? 'true' : 'false' },
    headers: { 'Content-Type': 'application/yaml' },
    transformRequest: [(d) => d], // 原样发文本，别让 axios 按对象序列化
  });

/** 导入模板下载地址（带注释的 YAML，覆盖全部数据源类型）。
 *  走 <a download> 直接下载，不经 axios——省得为一个静态文件做 blob 中转。 */
export const importTemplateUrl = () => `${API_BASE_PATH}/alert/sources/import-template`;

/** 数据源配置导出地址（YAML，可被「批量导入」原样导回）。
 *  includeSecrets=true 才带明文密码；默认不带，导回同一套平台时留空 = 沿用原密码。
 *  同样走 <a download> 直接下载。 */
export const exportSourcesUrl = ({ includeSecrets = false } = {}) =>
  `${API_BASE_PATH}/alert/sources/export?includeSecrets=${includeSecrets ? 'true' : 'false'}`;

/** 数据源采集状态（agent 上报）：
 *  { reported, mode, reportedAt, staleSec, sources: {<id>:{running,collecting,reason,...}}, unmatched:[...] } */
export const getSourcesHealth = () => get('/alert/sources/health');

/** 新建数据源前的同实例检测（只提示不拦截）。
 *  入参与 testDataSource 同构；返回 { duplicates: [{id,name,type,purposes,builtin}] }。
 *  判定「同实例」看的是完整连接标识（含库名/账号/db 序号），不是只比地址端口。 */
export const checkSourceDuplicate = (payload) => post('/alert/sources/check-duplicate', payload);

/** 部署自检：一次性汇总「装完该确认的事」——采集 / 主机 / 服务日志 / 链路 / 告警通知 / 慢查询。
 *  返回 { mode, items: [{key,title,status,detail,hint}] }，status ∈ ok|warn|error|unknown。 */
export const getSelfCheck = () => get('/alert/selfcheck');

/**
 * Toggle the enabled state of a source.
 * @param {string} kind - source kind (e.g. "datasource", "kafka", "mongo", ...)
 * @param {string} id   - source id
 * @param {boolean} enabled
 */
export const toggleSource = (kind, id, enabled) =>
  post(`/alert/sources/${kind}/${id}/toggle`, { enabled });

// ─────────────────────────────────────────────────────────────────────────────
// § Data Source Connectivity / Query Tests
// POST /alert/datasources/test        body = data source fields (may include id)
//   → { ok, message, detail? }   (always HTTP 200; read body.ok)
// POST /alert/datasources/test-query  body { dataSourceId, promql }
//   → { ok, resultType, count, series:[{metric,value,valueStr}], message? }
//     (always HTTP 200; read body.ok)
// NOTE: these endpoints encode success/failure in body.ok, NOT via HTTP status.
//       Do not treat a non-ok body as an error here — let the caller read .ok.
// ─────────────────────────────────────────────────────────────────────────────

/** Test connectivity of a data source. Resolves on HTTP 200; read body.ok. */
export const testDataSource = (payload) => post('/alert/datasources/test', payload);

/**
 * Probe a data source for connectivity AND per-purpose capability.
 * @param {Object} payload - same connection fields as testDataSource (type, id?, uri/host/…)
 * Response shape (always HTTP 200):
 *   {
 *     connect: { ok: boolean, message: string },
 *     purposes: {
 *       metrics?:  { available: boolean, reason: string },
 *       alert?:    { available: boolean, reason: string },
 *       diagnose?: { available: boolean, reason: string },
 *     },
 *     // 采集实况核验：直接问内置 Prometheus 有没有这条源的指标。
 *     // 「连得上」不等于「面板有数据」，两者中间还隔着 SD / exporter / 抓取三道。
 *     scrape: { state: 'ok'|'noData'|'down'|'unknown'|'skipped', key?: string, promql?: string }
 *   }
 * Do NOT treat a non-ok body as an HTTP error — read connect.ok.
 */
export const probeDataSource = (payload) => post('/alert/datasources/probe', payload);

/**
 * Preview a PromQL query against a data source.
 * @param {Object} payload - { dataSourceId, promql }
 * Resolves on HTTP 200; read body.ok.
 */
export const previewPromQL = (payload) => post('/alert/datasources/test-query', payload);

// ─────────────────────────────────────────────────────────────────────────────
// § Redis Instances  GET/POST /alert/redis-instances  PUT/DELETE /:id
// ─────────────────────────────────────────────────────────────────────────────

/** List Redis instances. */
export const listRedisInstances = () => get('/alert/redis-instances');

/** Create a Redis instance. */
export const createRedisInstance = (payload) => post('/alert/redis-instances', payload);

/** Update a Redis instance. */
export const updateRedisInstance = (id, payload) => put(`/alert/redis-instances/${id}`, payload);

/** Delete a Redis instance. */
export const deleteRedisInstance = (id) => del(`/alert/redis-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § Host Instances  GET/POST /alert/host-instances  PUT/DELETE /:id
// Fields: id, name, address(node_exporter host:port), purposes(["metrics"]), enabled
// 无密码。聚合 /alert/sources 里 type=host、detail={address}。
// ─────────────────────────────────────────────────────────────────────────────

/** List host instances. */
export const listHostInstances = () => get('/alert/host-instances');

/** Create a host instance. */
export const createHostInstance = (payload) => post('/alert/host-instances', payload);

/** Update a host instance. */
export const updateHostInstance = (id, payload) => put(`/alert/host-instances/${id}`, payload);

/** Delete a host instance. */
export const deleteHostInstance = (id) => del(`/alert/host-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § ES Instances  GET/POST /alert/es-instances  PUT/DELETE /:id
// ─────────────────────────────────────────────────────────────────────────────

/** List Elasticsearch instances. */
export const listEsInstances = () => get('/alert/es-instances');

/** Create an Elasticsearch instance. */
export const createEsInstance = (payload) => post('/alert/es-instances', payload);

/** Update an Elasticsearch instance. */
export const updateEsInstance = (id, payload) => put(`/alert/es-instances/${id}`, payload);

/** Delete an Elasticsearch instance. */
export const deleteEsInstance = (id) => del(`/alert/es-instances/${id}`);

// ─────────────────────────────────────────────────────────────────────────────
// § Custom Pages (把任意 Web UI 挂进菜单的外链页，非被监控数据源)
// GET    /alert/custom-pages        → [{ id, name, url, mode, enabled, path }]
// POST   /alert/custom-pages   body { name, url, mode?, enabled? } → { page, probeError? }
// PUT    /alert/custom-pages/{id}   同上(id/访问路径建后不变)
// DELETE /alert/custom-pages/{id}
// mode: 'proxy'(经平台反代，同源带鉴权) | 'iframe'(前端直接嵌目标地址)
// ─────────────────────────────────────────────────────────────────────────────

/** List custom pages. Resolves to an array (empty if none configured). */
export const listCustomPages = () => get('/alert/custom-pages');

/** Create a custom page. payload: { name, url, mode, enabled }. */
export const createCustomPage = (payload) => post('/alert/custom-pages', payload);

/** Update a custom page. payload: { name, url, mode, enabled }. */
export const updateCustomPage = (id, payload) => put(`/alert/custom-pages/${id}`, payload);

/** Delete a custom page. */
export const deleteCustomPage = (id) => del(`/alert/custom-pages/${id}`);

// ── 自定义监控面板 ───────────────────────────────────────────────────────
export const listMonitorPanels = (signal) => get('/alert/monitor-panels', {}, { signal });
export const saveMonitorPanel = (id, payload) => id
  ? put(`/alert/monitor-panels/${id}`, payload)
  : post('/alert/monitor-panels', payload);
export const deleteMonitorPanel = (id) => del(`/alert/monitor-panels/${id}`);
export const runMonitorQuery = (payload, signal) => post('/alert/promql/query', payload, { signal });
export const listMonitorMetrics = (params, signal) => get('/alert/promql/metrics', params, { signal });
export const listMonitorJobs = (signal) => get('/alert/promql/jobs', {}, { signal });
