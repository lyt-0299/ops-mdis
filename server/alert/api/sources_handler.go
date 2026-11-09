package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// ListSources GET /api/alert/sources
// 跨六类数据源的统一只读视图(Detail 已脱敏)，按 (type, name) 排序，返回裸数组。
func ListSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]model.UnifiedSource, 0)

	// ── Prometheus(source) ───────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindSource})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.DataSource
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			t := d.Type
			if t == "" {
				t = "prometheus"
			}
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindSource,
				Type:     t,
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Builtin:  d.Builtin,
				Detail:   map[string]any{"url": d.URL},
			})
		}
	}

	// ── Kafka ─────────────────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindKafkaInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.KafkaInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindKafkaInstance,
				Type:     model.TypeForKind(model.KindKafkaInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Detail:   map[string]any{"brokers": d.Brokers, "hasSecret": d.SASL != nil && d.SASL.Password != ""},
			})
		}
	}

	// ── MongoDB ───────────────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindMongoInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.MongoInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindMongoInstance,
				Type:     model.TypeForKind(model.KindMongoInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Builtin:  d.Builtin,
				Purposes: d.Purposes,
				Detail: map[string]any{
					"uri": model.SanitizeMongoURIForDisplay(d.URI), "username": d.Username,
					"authDb": d.AuthDB, "tls": d.TLS, "skipSSL": d.SkipSSL,
					"hasSecret": d.Password != "", "metricCollection": d.MetricCollection,
				},
			})
		}
	}

	// ── MySQL ─────────────────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindMySQLInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.MySQLInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindMySQLInstance,
				Type:     model.TypeForKind(model.KindMySQLInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Detail: map[string]any{
					"nodes": d.Nodes, "architecture": d.Architecture, "username": d.Username,
					"database": d.Database, "tls": d.TLS, "skipSSL": d.SkipSSL,
					"hasSecret": d.Password != "", "metricCollection": d.MetricCollection,
				},
			})
		}
	}

	// ── Redis ─────────────────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindRedisInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.RedisInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindRedisInstance,
				Type:     model.TypeForKind(model.KindRedisInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Detail:   map[string]any{"host": d.Host, "port": d.Port, "hasSecret": d.Password != ""},
			})
		}
	}

	// ── Elasticsearch ─────────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindESInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.ESInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindESInstance,
				Type:     model.TypeForKind(model.KindESInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Detail:   map[string]any{"url": d.URL, "hasSecret": d.Password != ""},
			})
		}
	}

	// ── 主机(node_exporter) ────────────────────────────────────────────────
	{
		cur, err := database.Config().Find(ctx, bson.M{"kind": model.KindHostInstance})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		var docs []model.HostInstance
		if err := cur.All(ctx, &docs); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
			return
		}
		for _, d := range docs {
			out = append(out, model.UnifiedSource{
				ID:       d.ID,
				Kind:     model.KindHostInstance,
				Type:     model.TypeForKind(model.KindHostInstance),
				Name:     d.Name,
				Enabled:  d.Enabled,
				Purposes: d.Purposes,
				Detail:   map[string]any{"address": d.Address},
			})
		}
	}

	// 按 (type, name) 排序
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})

	httputil.WriteJSON(w, http.StatusOK, out)
}

// toggleSourceReq 是启停请求体。
type toggleSourceReq struct {
	Enabled bool `json:"enabled"`
}

// ToggleSource POST /api/alert/sources/{kind}/{id}/toggle
// 启停任意一类数据源；内置 Prometheus 禁止停用。
func ToggleSource(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := r.PathValue("id")

	if !model.IsValidSourceKind(kind) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_datasource_type", kind)
		return
	}
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	var req toggleSourceReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// 内置 Prometheus 不可停用
	if kind == model.KindSource && id == model.BuiltinPrometheusID && !req.Enabled {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"key": "alert_builtin_prometheus_undisableable"})
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kind}
	update := bson.M{"$set": bson.M{"enabled": req.Enabled, "updatedAt": time.Now()}}
	res, err := database.Config().UpdateOne(ctx, filter, update)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_toggle_failed", err.Error())
		return
	}
	if res.MatchedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_datasource_not_found")
		return
	}

	// 停用/启用一条 MongoDB 源可能改变慢查询的可用目标集合(diagnose 用途)，
	// 与增删改同样要重建连接，否则页面上停用了、慢查询还在往那台库上打。
	if kind == model.KindMongoInstance {
		reloadSlowQueryTargets("启停 MongoDB 实例 " + id)
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// 1.5.0 移除 SDMode 接口与 ENV_PROMETHEUS_SD_MODE 开关：
// 本页永远是采集配置的唯一真相源，不存在第二种模式，前端也就不必再问「现在算哪套」。

// SourcesHealth GET /api/alert/sources/health
// 数据源页的「采集状态」列数据：把 ops-agent 上报的 exporter 状态按端口对回每条数据源。
//
// 为什么按端口对：exporter 端口由 ops-server 按 (类型, _id 升序) 确定性分配，
// agent 用的就是这套端口，因此 type+port 是两边唯一可靠的关联键（名字可以改、目标可以重复）。
//
// unmatched = agent 报了这个 exporter，但端口对不上任何数据源。1.5.0 前这通常是
// env 模式下按 ops.yaml 起的 exporter；现在 ops.yaml 不再产生采集目标，剩下的正常成因
// 是「数据源刚被删、exporter 还没回收」——agent 下一轮对账（30s 内）会自动清掉。
// mergeNodeStatus 把同一条数据源下多个节点的采集状态并成一条：running/collecting 取
// 逻辑与（有一个不行就算不行），reason 取第一条非空的，target 换成「N 个节点」。
func mergeNodeStatus(a, b map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, a)
	out["running"] = asBoolAny(a["running"]) && asBoolAny(b["running"])
	out["collecting"] = asBoolAny(a["collecting"]) && asBoolAny(b["collecting"])
	if r, _ := a["reason"].(string); r == "" {
		out["reason"] = b["reason"]
	}
	n := 2
	if v, ok := a["nodeCount"].(int); ok {
		n = v + 1
	}
	out["nodeCount"] = n
	out["target"] = fmt.Sprintf("%d 个节点", n)
	return out
}

func asBoolAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func SourcesHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mode, exporters, at := service.AgentStatusSnapshot()

	// 主机(node_exporter)的状态不来自 agent，得单独查 Prometheus——且必须在下面
	// at.IsZero() 早退之前算好：只配了主机、没配任何中间件时 agent 一次都不会上报，
	// 早退会把主机状态一起吞掉。
	hostSources := hostHealth(ctx)
	mongoLogs := service.MongoLogStatuses(ctx)

	if at.IsZero() {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"reported":  len(hostSources) > 0,
			"mode":      mode,
			"sources":   hostSources,
			"mongoLogs": mongoLogs,
			"unmatched": []any{},
		})
		return
	}

	// 端口 → 数据源 id
	portToID := map[int]string{}
	for typ := range map[string]string{
		"kafka": "", "elasticsearch": "", "redis": "", "mysql": "", "mongodb": "",
	} {
		for _, d := range listMetricsSourceIDs(ctx, typ) {
			// 一条源可能占多个端口（多节点展开），逐个登记，否则其余节点会被判成「未匹配」
			for _, p := range service.SourcePortsOf(ctx, typ, d) {
				if p > 0 {
					portToID[p] = d
				}
			}
		}
	}

	sources := map[string]any{}
	maps.Copy(sources, hostSources)
	unmatched := make([]map[string]any, 0)
	for _, e := range exporters {
		item := map[string]any{
			"type": e.Type, "target": e.Target, "port": e.Port,
			"running": e.Running, "collecting": e.Collecting, "reason": e.Reason,
		}
		id, ok := portToID[e.Port]
		if !ok {
			unmatched = append(unmatched, item)
			continue
		}
		// 多节点源：同一个 id 会收到多份状态，按「最差的那份」汇总——只要有一个节点
		// 采不到，这条源就不能显示成正常，否则挂掉的那个节点在列表上完全看不出来。
		if prev, dup := sources[id].(map[string]any); dup {
			sources[id] = mergeNodeStatus(prev, item)
			continue
		}
		sources[id] = item
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"reported":   true,
		"mode":       mode,
		"reportedAt": at.Format(time.RFC3339),
		"staleSec":   int(time.Since(at).Seconds()),
		"sources":    sources,
		"mongoLogs":  mongoLogs,
		"unmatched":  unmatched,
	})
}

// hostHealth 主机数据源的采集状态，直接查内置 Prometheus 的 up{job="host"}。
//
// 为什么要单独一条路：主机(node_exporter)跑在被监控主机上、由 Prometheus 直抓，
// 不经 ops-agent，所以 /api/sd/agent-status 里永远不会有它。1.5.8 及以前这一列因此
// 恒显示「未采集」——即使 target 明明是 up 的。客户界面上唯一一条 Host 标着「未采集」、
// 其余五类全「采集中」，只会理解成自己把主机配错了，而实际什么都不用改（实测反馈）。
//
// 匹配键用 address ↔ instance，不用 name ↔ hostname：host job 没有「把 instance 改写成
// 数据源名」的 relabel（其余类型才有），instance 就是 SD 下发的原始地址，比可以随时
// 改名的 name 稳。
//
// 查不通 Prometheus 时返回空 map，让 UI 回到「未知」——绝不能退化成「未采集」，
// 那是把「我不知道」说成「确定没在采」，比不显示更糟。
func hostHealth(ctx context.Context) map[string]any {
	cur, err := database.Config().Find(ctx,
		bson.M{"kind": model.KindHostInstance, "enabled": true, "purposes": "metrics"})
	if err != nil {
		return map[string]any{}
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if cur.All(ctx, &docs) != nil || len(docs) == 0 {
		return map[string]any{}
	}

	client, err := builtinPrometheusClient(ctx)
	if err != nil {
		return map[string]any{}
	}
	vals, err := client.GetVectorValues(`up{job="host"}`)
	if err != nil {
		return map[string]any{}
	}
	upByInstance := make(map[string]float64, len(vals))
	for _, v := range vals {
		upByInstance[v.Metric["instance"]] = v.Value
	}

	out := make(map[string]any, len(docs))
	for _, d := range docs {
		id := bsonString(d["_id"])
		addr := strings.TrimSpace(bsonString(d["address"]))
		if id == "" || addr == "" {
			continue
		}
		item := map[string]any{"type": "host", "target": addr, "port": 0, "reason": ""}
		switch v, ok := upByInstance[addr]; {
		case ok && v == 1:
			item["running"], item["collecting"] = true, true
		case ok:
			// target 在册但抓取失败：地址填了但对端不是 node_exporter，或端口不通。
			item["running"], item["collecting"] = true, false
			item["reasonKey"] = "alert_host_exporter_unreachable"
		default:
			// Prometheus 还没这个 target：多为刚加完源没到下一轮 SD（30s 内自愈）。
			item["running"], item["collecting"] = false, false
			item["reasonKey"] = "alert_host_target_not_scraped"
		}
		out[id] = item
	}
	return out
}

// listMetricsSourceIDs 取某类型「已启用且用途含 metrics」的数据源 id（顺序与端口分配一致）。
func listMetricsSourceIDs(ctx context.Context, typ string) []string {
	kindByType := map[string]string{
		"kafka": model.KindKafkaInstance, "elasticsearch": model.KindESInstance,
		"redis": model.KindRedisInstance, "mysql": model.KindMySQLInstance,
		"mongodb": model.KindMongoInstance,
	}
	kind, ok := kindByType[typ]
	if !ok {
		return nil
	}
	cur, err := database.Config().Find(ctx,
		bson.M{"kind": kind, "enabled": true, "purposes": "metrics"},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if cur.All(ctx, &docs) != nil {
		return nil
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		switch v := d["_id"].(type) {
		case string:
			out = append(out, v)
		case bson.ObjectID:
			out = append(out, v.Hex())
		}
	}
	return out
}

// ============================================================================
// 同实例重复检测（1.4.9）
//
// 真实客户部署反馈：同一个中间件被登记成好几条数据源，用户自己也说不清哪条在生效。
// 多数情况下他并不是想加第二个实例，而是「想让这个实例也出现在另一个功能里」——
// 不知道该去改用途，就又建了一条。
//
// 这里只做检测、不做拦截：同地址但不同库 / 不同账号 / 不同 db 序号都是合理场景
// （同一台 MySQL 的两个库、同一台 Redis 的 db0 和 db1），硬拦会误伤。
// 前端拿到结果后给「去补用途 / 仍然新建 / 取消」三条路。
// ============================================================================

// connKey 生成某数据源的「连接标识」：同类型下标识相同 = 同一个实例。
// 刻意把库名 / 账号 / db 序号也算进去，只比地址端口会把合理的多条判成重复。
func connKey(typ string, d bson.M) string {
	norm := func(v any) string { return strings.TrimSpace(bsonString(v)) }
	switch typ {
	case "kafka":
		var bs []string
		if arr, ok := d["brokers"].(bson.A); ok {
			for _, b := range arr {
				bs = append(bs, norm(b))
			}
		}
		sort.Strings(bs)
		user := ""
		if s, ok := d["sasl"].(bson.M); ok {
			user = norm(s["username"])
		}
		return strings.Join(bs, ",") + "|" + user
	case "elasticsearch":
		u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(norm(d["url"]), "http://"), "https://"), "/")
		return u + "|" + norm(d["username"])
	case "redis":
		return norm(d["host"]) + ":" + norm(d["port"]) + "|db" + norm(d["db"])
	case "mysql":
		var ns []string
		if arr, ok := d["nodes"].(bson.A); ok {
			for _, n := range arr {
				ns = append(ns, norm(n))
			}
		}
		sort.Strings(ns)
		return strings.Join(ns, ",") + "|" + norm(d["username"]) + "|" + norm(d["database"])
	case "mongodb":
		return model.SanitizeMongoURIForDisplay(norm(d["uri"])) + "|" + norm(d["username"]) + "|" + norm(d["authDb"])
	}
	return ""
}

func bsonString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int32:
		return strconv.Itoa(int(x))
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case bson.ObjectID:
		return x.Hex()
	}
	return ""
}

// CheckSourceDuplicate POST /api/alert/sources/check-duplicate
// 入参与「测试连接」同构（前端复用同一份 payload），返回同实例的已有数据源。
func CheckSourceDuplicate(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	typ, _ := req["type"].(string)
	selfID, _ := req["id"].(string)
	kindByType := map[string]string{
		"kafka": model.KindKafkaInstance, "elasticsearch": model.KindESInstance,
		"redis": model.KindRedisInstance, "mysql": model.KindMySQLInstance,
		"mongodb": model.KindMongoInstance,
	}
	kind, ok := kindByType[typ]
	if !ok { // 其余类型（prometheus/host/k8s/custom）不做重复检测
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"duplicates": []any{}})
		return
	}

	want := connKey(typ, toBsonM(req))
	if want == "" || strings.Trim(want, "|:db,") == "" {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"duplicates": []any{}})
		return
	}

	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kind})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	dups := make([]map[string]any, 0)
	for _, d := range docs {
		id := bsonString(d["_id"])
		if id == selfID { // 编辑自己时不算重复
			continue
		}
		if connKey(typ, d) != want {
			continue
		}
		purposes := []string{}
		if arr, ok := d["purposes"].(bson.A); ok {
			for _, p := range arr {
				purposes = append(purposes, bsonString(p))
			}
		}
		dups = append(dups, map[string]any{
			"id": id, "name": bsonString(d["name"]), "type": typ,
			"purposes": purposes, "builtin": d["builtin"] == true,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"duplicates": dups})
}

// toBsonM 把前端 JSON 转成和库里同构的 bson.M，便于走同一个 connKey。
func toBsonM(m map[string]any) bson.M {
	out := bson.M{}
	for k, v := range m {
		switch x := v.(type) {
		case []any:
			arr := bson.A{}
			for _, e := range x {
				arr = append(arr, e)
			}
			out[k] = arr
		case map[string]any:
			out[k] = toBsonM(x)
		default:
			out[k] = v
		}
	}
	return out
}

// ============================================================================
// 部署自检（1.4.9）
//
// 装完之后客户没有任何「我装对了吗」的判断依据，只能挨个点菜单看有没有图；出问题也不知道
// 该看哪里，只能找我们。这个接口把「装完该确认的事」一次性汇总，让客户自己就能闭环：
// 采集通没通、主机监控在不在、服务日志接没接、告警通不通知。
//
// 每一项都给 status + 一句可操作的 hint，UI 直接照搬即可。
// ============================================================================

type checkItem struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok / warn / error / unknown
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// lokiDiscarded 读 Loki 自身 /metrics 里的 loki_discarded_samples_total，返回总丢弃条数
// 与占比最大的原因。用来把「根本没推过来」和「推了但被拒收」这两种完全不同的处境分开——
// 只看 label 有没有值时，两者长得一模一样。
func lokiDiscarded(lokiURL string) (int64, string) {
	body, ok := httpProbeJSON(lokiURL+"/metrics", 5*time.Second)
	if !ok {
		return 0, ""
	}
	return parseLokiDiscarded(body)
}

// parseLokiDiscarded 从 prometheus 文本格式里累加丢弃计数并取占比最大的原因。
func parseLokiDiscarded(body []byte) (int64, string) {
	var total int64
	topReason, topN := "", int64(0)
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "loki_discarded_samples_total{") {
			continue
		}
		i := strings.LastIndex(line, " ")
		if i < 0 {
			continue
		}
		// 计数是 prometheus 文本格式，可能写成 105343 或 1.05343e+05
		f, err := strconv.ParseFloat(strings.TrimSpace(line[i+1:]), 64)
		if err != nil || f <= 0 {
			continue
		}
		n := int64(f)
		total += n
		reason := ""
		if j := strings.Index(line, `reason="`); j >= 0 {
			rest := line[j+len(`reason="`):]
			if k := strings.Index(rest, `"`); k >= 0 {
				reason = rest[:k]
			}
		}
		if n > topN {
			topN, topReason = n, reason
		}
	}
	return total, topReason
}

func httpProbeJSON(url string, timeout time.Duration) ([]byte, bool) {
	if url == "" {
		return nil, false
	}
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return b, true
}

// SelfCheck GET /api/alert/selfcheck
func SelfCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items := make([]checkItem, 0, 6)

	// ① 采集状态：以 agent 上报为准
	mode, exporters, at := service.AgentStatusSnapshot()
	if mode == "" {
		mode = "http"
	}
	switch {
	case at.IsZero():
		items = append(items, checkItem{"collect", "指标采集", "unknown", "ops-agent 尚未上报采集状态",
			"确认 ops-agent 容器已启动；启动后约 1 分钟上报一次"})
	default:
		bad := 0
		for _, e := range exporters {
			if !e.Collecting {
				bad++
			}
		}
		st, detail := "ok", fmt.Sprintf("%d 个采集目标全部正常", len(exporters))
		hint := ""
		if bad > 0 {
			st = "error"
			detail = fmt.Sprintf("%d 个采集目标中有 %d 个采不到数据", len(exporters), bad)
			hint = "到「数据源」页看红色的「采集状态」，悬停即显示失败原因原文"
		}
		if len(exporters) == 0 {
			st, detail = "warn", "还没有任何采集目标"
			hint = "到「数据源」页登记要监控的中间件"
		}
		items = append(items, checkItem{"collect", "指标采集", st, detail, hint})
	}

	// ② 主机监控：这次真实客户升级后整类丢失过，单独列一项
	nHost, _ := database.Config().CountDocuments(ctx, bson.M{"kind": model.KindHostInstance, "enabled": true})
	if nHost > 0 {
		items = append(items, checkItem{"host", "主机监控", "ok", fmt.Sprintf("已登记 %d 台主机", nHost), ""})
	} else {
		items = append(items, checkItem{"host", "主机监控", "warn", "没有登记任何主机",
			"到「数据源」页新建「主机」，地址填 <部署机IP>:59100（node_exporter 由 ops-nodeagent 自带）"})
	}

	// ③ 服务日志：查 Loki 有没有 HAP 微服务的日志标签
	lokiURL := strings.TrimRight(os.Getenv("ENV_LOKI_URL"), "/")
	if lokiURL == "" {
		lokiURL = "http://ops-loki:3100"
	}
	if b, ok := httpProbeJSON(lokiURL+"/loki/api/v1/label/service_name/values", 5*time.Second); ok {
		var lr struct {
			Data []string `json:"data"`
		}
		_ = json.Unmarshal(b, &lr)
		if len(lr.Data) > 0 {
			items = append(items, checkItem{"servicelog", "HAP 服务日志", "ok",
				fmt.Sprintf("已接入 %d 个服务", len(lr.Data)), ""})
		} else if n, reason := lokiDiscarded(lokiURL); n > 0 {
			// 查得到丢弃计数 = 日志确实推过来了，是 Loki 自己拒收的。此时再让用户去
			// 「配置 ENV_LOKI_URL」纯属把人往反方向带——他明明已经配好了。
			// 实测 local_50 上就有 10 万条被判 too_far_behind：样本时间戳超出 Loki 的
			// 接受窗口，最常见的成因是发送端容器时区/时钟不对（差 8 小时就足够全丢）。
			items = append(items, checkItem{"servicelog", "HAP 服务日志", "warn",
				fmt.Sprintf("日志推过来了，但被 Loki 丢弃 %d 条（原因：%s）", n, reason),
				"reason=too_far_behind 表示样本时间戳超出接受窗口，先核对发送端容器的时区与系统时钟；其余原因多为超出摄入速率限制，可先降低日志级别再观察"})
		} else {
			items = append(items, checkItem{"servicelog", "HAP 服务日志", "warn", "Loki 里还没有服务日志",
				"需在 HAP 私有部署侧配置 ENV_LOKI_URL 指向本平台 Loki 并重启 HAP 服务；不配则 HAP 不会写日志。注意别和本平台同名变量混淆"})
		}
	} else {
		items = append(items, checkItem{"servicelog", "HAP 服务日志", "unknown", "查询 Loki 失败",
			"确认 ops-loki 容器已启动"})
	}

	// ④ 链路追踪
	tempoURL := strings.TrimRight(os.Getenv("ENV_TEMPO_URL"), "/")
	if tempoURL == "" {
		tempoURL = "http://ops-tempo:3200"
	}
	if b, ok := httpProbeJSON(tempoURL+"/api/search/tag/service.name/values", 5*time.Second); ok {
		var tr struct {
			TagValues []string `json:"tagValues"`
		}
		_ = json.Unmarshal(b, &tr)
		if len(tr.TagValues) > 0 {
			items = append(items, checkItem{"trace", "链路追踪", "ok",
				fmt.Sprintf("已接收 %d 个服务的链路", len(tr.TagValues)), ""})
		} else {
			items = append(items, checkItem{"trace", "链路追踪", "warn", "还没有收到链路数据",
				"应用需把 OTLP 上报到 ops-alloy 的 4317/4318；未接入可忽略此项"})
		}
	} else {
		items = append(items, checkItem{"trace", "链路追踪", "unknown", "查询 Tempo 失败", "确认 ops-tempo 容器已启动"})
	}

	// ⑤ 告警通知：有规则却没有任何渠道 = 告警是哑的
	nRule, _ := database.Config().CountDocuments(ctx, bson.M{"kind": kindAlertRule})
	nHook, _ := database.Config().CountDocuments(ctx, bson.M{"kind": kindWebhook})
	switch {
	case nHook > 0:
		items = append(items, checkItem{"notify", "告警通知", "ok", fmt.Sprintf("已配置 %d 个通知渠道", nHook), ""})
	case nRule > 0:
		items = append(items, checkItem{"notify", "告警通知", "error",
			fmt.Sprintf("已有 %d 条告警规则，但一个通知渠道都没有", nRule),
			"规则触发后只会写入告警历史、不会通知任何人。到「通知渠道」页添加后，再到规则里选上"})
	default:
		items = append(items, checkItem{"notify", "告警通知", "warn", "还没有配置通知渠道", "到「通知渠道」页添加"})
	}

	// ⑥ 慢查询诊断目标
	nDiag, _ := database.Config().CountDocuments(ctx, bson.M{"kind": model.KindMongoInstance, "enabled": true, "purposes": "diagnose"})
	if nDiag > 0 {
		items = append(items, checkItem{"slowquery", "慢查询诊断", "ok", "已配置诊断目标库", ""})
	} else {
		items = append(items, checkItem{"slowquery", "慢查询诊断", "warn", "没有配置诊断目标库",
			"到「数据源」页给被监控的 MongoDB 勾上「慢查询诊断」用途"})
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{"mode": mode, "items": items})
}
