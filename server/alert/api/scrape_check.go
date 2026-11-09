package api

import (
	"context"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// ============================================================================
// 「到底采到数据没有」核验（1.5.9）。
//
// 起因是客户原话：「这个数据源检测判断连接成功的逻辑标准是啥，感觉这个连接成功
// 没有啥意义，还是得看面板是否有数据」——他说得对。连接测试只验 TCP 可达 + 认证
// 通过，而真正决定面板有没有图的是「exporter 采到了、Prometheus 也抓进去了」，
// 这两件事之间还隔着 SD 下发、exporter 起没起、抓取有没有报错三道。1.5.8 的
// MongoDB 就正好卡在中间：连接测试通过、进程在跑，但每次抓取都 panic，面板全空。
//
// 所以这里补一条独立的判据：直接问内置 Prometheus「这条数据源现在有没有指标」。
// 判据沿用 roles/agent.sh 里 check_metrics 那套（同一套"真采到了"的标准，
// 两边别各说各话），只是换到 Prometheus 侧查，因而连"抓取有没有真进库"也覆盖到。
//
// instance 标签的取值：中间件类 job 都有 relabel（hostname → instance），
// 所以 instance == 数据源名；host job 没有该 relabel，instance == 抓取地址。
// ============================================================================

// 采集核验状态。文案归前端（见 CLAUDE.md 约定），这里只给枚举 + 英文 key。
const (
	scrapeStateOK      = "ok"      // Prometheus 里有该源的指标且指标自述正常
	scrapeStateNoData  = "noData"  // 一条 series 都查不到
	scrapeStateDown    = "down"    // 有 series，但 exporter 自述连不上目标（如 mysql_up=0）
	scrapeStateUnknown = "unknown" // 查不动 Prometheus，别把「不知道」说成「没数据」
	scrapeStateSkipped = "skipped" // 这条源本来就不该被抓（没开、没选看图用途、无采集概念的类型）
)

// scrapeProbe 按类型给出的核验查询：指标名 + 「算正常」的判据。
// 指标选取与 agent.sh check_metrics 对齐：
//
//	kafka         kafka_brokers >= 1
//	elasticsearch elasticsearch_clusterinfo_up / elasticsearch_cluster_health_up == 1
//	redis         redis_up == 1
//	mysql         mysql_up == 1
//	mongodb       mongodb_up == 1（agent 侧看 mongodb_ss_*，同一件事的另一面）
//	host          up == 1（node_exporter 由 Prometheus 直抓，没有 exporter 自述指标）
var scrapeProbeMetrics = map[string][]string{
	"mysql":         {"mysql_up"},
	"mongodb":       {"mongodb_up"},
	"redis":         {"redis_up"},
	"kafka":         {"kafka_brokers"},
	"elasticsearch": {"elasticsearch_cluster_health_up", "elasticsearch_clusterinfo_up"},
	"host":          {"up"},
}

// scrapeCheck 核验某条数据源在 Prometheus 里的采集实况。
// 返回 {state, key?, promql?}：key 是英文机器键，promql 原样带回，
// 方便用户直接粘到「PromQL 预览」里自己看一眼（比让他猜指标名快得多）。
func scrapeCheck(ctx context.Context, typ, id string) map[string]any {
	metrics, ok := scrapeProbeMetrics[typ]
	if !ok || strings.TrimSpace(id) == "" {
		// prometheus（平台自身）、custom（自定义外链页面，不采指标）没有采集概念
		return map[string]any{"state": scrapeStateSkipped}
	}

	kind := kindForSourceType(typ)
	if kind == "" {
		return map[string]any{"state": scrapeStateSkipped}
	}

	var doc bson.M
	if err := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kind}).Decode(&doc); err != nil {
		// 还没保存的新建表单（探测时没有 id 对应的记录）——没什么可核验的
		return map[string]any{"state": scrapeStateSkipped}
	}
	if enabled, isBool := doc["enabled"].(bool); isBool && !enabled {
		return map[string]any{"state": scrapeStateSkipped, "key": "alert_scrape_source_disabled"}
	}
	if !purposesContain(doc["purposes"], "metrics") {
		return map[string]any{"state": scrapeStateSkipped, "key": "alert_scrape_purpose_off"}
	}

	// host 用抓取地址匹配，其余类型用数据源名（job 里已 relabel 成 instance）
	instance := strings.TrimSpace(bsonString(doc["name"]))
	if typ == "host" {
		instance = strings.TrimSpace(bsonString(doc["address"]))
	}
	if instance == "" {
		return map[string]any{"state": scrapeStateUnknown}
	}

	selector := `{job=` + strconv.Quote(typ) + `,instance=` + strconv.Quote(instance) + `}`
	exprs := make([]string, 0, len(metrics))
	for _, m := range metrics {
		exprs = append(exprs, m+selector)
	}
	// 多个候选指标用 or 串起来：ES exporter 新旧版本暴露的健康指标名不同，
	// 写死一个会在另一个版本上恒报「没数据」。
	promQL := strings.Join(exprs, " or ")

	client, err := builtinPrometheusClient(ctx)
	if err != nil {
		return map[string]any{"state": scrapeStateUnknown, "key": "alert_scrape_query_failed", "promql": promQL}
	}
	vals, err := client.GetVectorValues(promQL)
	if err != nil {
		return map[string]any{"state": scrapeStateUnknown, "key": "alert_scrape_query_failed", "promql": promQL}
	}
	if len(vals) == 0 {
		return map[string]any{"state": scrapeStateNoData, "key": "alert_scrape_no_series", "promql": promQL}
	}
	for _, v := range vals {
		if v.Value > 0 {
			return map[string]any{"state": scrapeStateOK, "promql": promQL}
		}
	}
	// 指标在，但值是 0：Prometheus 抓到了 exporter，exporter 自己连不上目标。
	return map[string]any{"state": scrapeStateDown, "key": "alert_scrape_exporter_down", "promql": promQL}
}

// kindForSourceType type → kind 反查（model 侧只有 kind → type 的正向表）。
func kindForSourceType(typ string) string {
	for _, kind := range []string{
		model.KindMySQLInstance,
		model.KindMongoInstance,
		model.KindRedisInstance,
		model.KindKafkaInstance,
		model.KindESInstance,
		model.KindHostInstance,
	} {
		if model.TypeForKind(kind) == typ {
			return kind
		}
	}
	return ""
}

// purposesContain 判断 bson 里的 purposes 数组是否含某用途。
func purposesContain(raw any, want string) bool {
	arr, ok := raw.(bson.A)
	if !ok {
		return false
	}
	for _, v := range arr {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// builtinPrometheusClient 取内置 Prometheus 的查询客户端（解密后）。
// 平台自己的 Prometheus 是唯一的指标真相源，多处需要——采集核验、主机采集状态。
func builtinPrometheusClient(ctx context.Context) (*service.PrometheusClient, error) {
	var ds model.DataSource
	if err := database.Config().FindOne(ctx,
		bson.M{"_id": builtinPrometheusID, "kind": kindDataSource}).Decode(&ds); err != nil {
		return nil, err
	}
	if err := decryptDSSecrets(&ds); err != nil {
		return nil, err
	}
	return service.NewPrometheusClient(&ds), nil
}
