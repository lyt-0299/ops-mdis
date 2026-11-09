package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// 端口方案：对每个类型，只取 enabled==true 且 purposes 含 "metrics" 的源，
// 按 _id 字符串升序排序，第 i 个(i 从 0 开始)的 exporter 监听端口 = 类型高位基 + i。
// ops-agent 与 ops-prometheus http_sd 必须严格用同一套，改这里会牵动整条链路。
var typePortBase = map[string]int{
	"kafka":         19500,
	"elasticsearch": 19200,
	"redis":         19300,
	"mysql":         19100,
	"mongodb":       19400,
}

// typeToKind 把面向 agent/prometheus 的 type 名映射回 alert_config 的 discriminator kind。
var typeToKind = map[string]string{
	"kafka":         model.KindKafkaInstance,
	"elasticsearch": model.KindESInstance,
	"redis":         model.KindRedisInstance,
	"mysql":         model.KindMySQLInstance,
	"mongodb":       model.KindMongoInstance,
}

// RegisterSDRoutes 注册服务发现端点(不经 atoken，自带 ?token 校验)。
func RegisterSDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sd/sources", SourcesForAgent)
	mux.HandleFunc("GET /api/sd/prom/{job}", PromHTTPSD)
	mux.HandleFunc("GET /api/sd/promfile/{job}", PromFileSD)
	// ops-agent 定期上报各 exporter 的采集状态，供 UI 数据源页显示红绿点与失败原因
	mux.HandleFunc("POST /api/sd/agent-status", ReceiveAgentStatus)
}

// checkSDToken 校验 query 中的 token。
// ENV_OPS_SD_TOKEN 非空时必须相等，否则 403；为空时放行(仅 docker 内网)。
func checkSDToken(w http.ResponseWriter, r *http.Request) bool {
	want := os.Getenv("ENV_OPS_SD_TOKEN")
	if want == "" {
		return true
	}
	if r.URL.Query().Get("token") == want {
		return true
	}
	httputil.WriteErr(w, http.StatusForbidden, "alert_invalid_sd_token")
	return false
}

// enabledMetricsSources 查 {kind, enabled:true, purposes:"metrics"}，按 _id 升序返回原始 bson.M。
// mongo 中 purposes:"metrics" 对数组字段会匹配「数组含该值」，正是所需语义。
func enabledMetricsSources(ctx context.Context, kind string) ([]bson.M, error) {
	cur, err := database.Config().Find(ctx,
		bson.M{"kind": kind, "enabled": true, "purposes": "metrics"},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// sdTarget 是一条数据源展开后的单个采集目标。
type sdTarget struct {
	doc      bson.M
	node     string // 展开出来的节点地址；不展开的类型为空
	port     int
	hostname string // Prometheus 侧 relabel 成 instance 的标签值
}

// nodesOfDoc 返回该数据源要「各起一个 exporter」的节点列表。
//
// MySQL / Redis / Elasticsearch 的多地址是多个各自独立的实例，必须一节点一 exporter：
// 此前 MySQL 虽然模型上支持 nodes，agent 却只取 nodes[0]，配三个节点静默只采第一个。
// Kafka / MongoDB 相反——多 broker 属于同一个集群、多 host 属于同一个副本集，
// exporter 一个进程连整体即可，不展开（返回 nil）。
func nodesOfDoc(kind string, doc bson.M) []string {
	switch kind {
	case model.KindMySQLInstance:
		return asStringSlice(doc["nodes"])
	case model.KindRedisInstance:
		if ns := asStringSlice(doc["nodes"]); len(ns) > 0 {
			return ns
		}
		host := asString(doc["host"])
		if host == "" {
			return nil
		}
		port := asInt(doc["port"])
		if port == 0 {
			port = 6379
		}
		return []string{host + ":" + strconv.Itoa(port)}
	case model.KindESInstance:
		if ns := asStringSlice(doc["nodes"]); len(ns) > 0 {
			return ns
		}
		if url := asString(doc["url"]); url != "" {
			return []string{url}
		}
		return nil
	}
	return nil
}

// expandTargets 把数据源列表展开成采集目标并分配确定性端口。
// 端口在整个列表上线性递增：agent 与 prometheus 两侧都从这一个函数取，不会算岔。
func expandTargets(kind string, docs []bson.M, base int) []sdTarget {
	out := make([]sdTarget, 0, len(docs))
	port := base
	for _, doc := range docs {
		name := asString(doc["name"])
		nodes := nodesOfDoc(kind, doc)
		if len(nodes) == 0 {
			out = append(out, sdTarget{doc: doc, port: port, hostname: name})
			port++
			continue
		}
		for _, node := range nodes {
			hostname := name
			if len(nodes) > 1 {
				// 只有真配了多节点才加后缀。单节点保持原样，免得给存量部署换掉 instance，
				// 把看板里的历史曲线断成两截。
				hostname = name + "/" + node
			}
			out = append(out, sdTarget{doc: doc, node: node, port: port, hostname: hostname})
			port++
		}
	}
	return out
}

// applyNodeOverride 把展开出的单个节点写回条目，覆盖「整条源的地址集合」。
// 这样 agent 侧仍是「一个条目 = 一个 exporter」的老语义，agent.sh 不用改。
func applyNodeOverride(entry map[string]any, kind, node string) {
	if node == "" {
		return
	}
	switch kind {
	case model.KindMySQLInstance:
		entry["nodes"] = []string{node}
	case model.KindRedisInstance:
		host, port := node, 6379
		if i := strings.LastIndex(node, ":"); i > 0 {
			if p, err := strconv.Atoi(node[i+1:]); err == nil {
				host, port = node[:i], p
			}
		}
		entry["host"] = host
		entry["redisPort"] = port
	case model.KindESInstance:
		entry["url"] = node
	}
}

// agentHost 取环境变量 ENV_AGENT_SD_HOST，默认 docker 服务名 "agent"。
func agentHost() string {
	if h := os.Getenv("ENV_AGENT_SD_HOST"); h != "" {
		return h
	}
	return "agent"
}

// decryptSilent 解密密文，失败时返回原值(避免一条坏数据拖垮整批 SD 输出)。
func decryptSilent(v string) string {
	if v == "" {
		return ""
	}
	plain, err := crypto.Decrypt(v)
	if err != nil {
		return v
	}
	return plain
}

// SourcesForAgent GET /api/sd/sources?type=<type>
// 给 ops-agent：返回每个源的 id/name/port + 解密后的连接信息(camelCase)。
func SourcesForAgent(w http.ResponseWriter, r *http.Request) {
	if !checkSDToken(w, r) {
		return
	}
	typ := r.URL.Query().Get("type")
	// 主机(node_exporter)跑在被监控主机本机，agent 不为其起 exporter，返回空数组。
	if typ == "host" {
		httputil.WriteJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	kind, ok := typeToKind[typ]
	if !ok {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_unknown_type")
		return
	}
	base := typePortBase[typ]

	ctx := r.Context()
	docs, err := enabledMetricsSources(ctx, kind)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}

	targets := expandTargets(kind, docs, base)
	out := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		doc := t.doc
		entry := map[string]any{
			"id":   asString(doc["_id"]),
			"name": t.hostname,
			"port": t.port,
		}
		switch kind {
		case model.KindKafkaInstance:
			fillKafka(entry, doc)
		case model.KindMongoInstance:
			fillMongo(entry, doc)
		case model.KindMySQLInstance:
			fillMySQL(entry, doc)
		case model.KindRedisInstance:
			fillRedis(entry, doc)
		case model.KindESInstance:
			fillES(entry, doc)
		}
		if args := metricCollectorArgs(kind, doc); len(args) > 0 {
			entry["collectorArgs"] = args
		}
		applyNodeOverride(entry, kind, t.node)
		out = append(out, entry)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// PromHTTPSD GET /api/sd/prom/{job}
// 给 ops-prometheus http_sd：标准格式 [{"targets":["host:port"],"labels":{"hostname":"<源name>"}}]，不含凭据。
func PromHTTPSD(w http.ResponseWriter, r *http.Request) {
	if !checkSDToken(w, r) {
		return
	}
	job := r.PathValue("job")
	// 主机分支：target 直接取 host 文档的 address 字段(原样)，不经 agent、不算端口基。
	if job == "host" {
		promHostHTTPSD(w, r)
		return
	}
	kind, ok := typeToKind[job]
	if !ok {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_unknown_job")
		return
	}
	base := typePortBase[job]
	host := agentHost()

	ctx := r.Context()
	docs, err := enabledMetricsSources(ctx, kind)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}

	type promTarget struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}
	targets := expandTargets(kind, docs, base)
	out := make([]promTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, promTarget{
			Targets: []string{host + ":" + strconv.Itoa(t.port)},
			Labels:  map[string]string{"hostname": t.hostname},
		})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// PromFileSD GET /api/sd/promfile/{job}
// 给 ops-prometheus file_sd：直接返回 file_sd 的 YAML 文本(text/plain)，
// prometheus entrypoint 在 http 模式下 `curl > <job>.yaml` 即可，无需 jq 解析。
// 输出与原 entrypoint 的 format() 完全一致(targets + labels.hostname)，故 label 兼容。
func PromFileSD(w http.ResponseWriter, r *http.Request) {
	if !checkSDToken(w, r) {
		return
	}
	job := r.PathValue("job")
	// 主机分支：target 直接取 host 文档的 address 字段(原样)，不经 agent、不算端口基。
	if job == "host" {
		promHostFileSD(w, r)
		return
	}
	kind, ok := typeToKind[job]
	if !ok {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_unknown_job")
		return
	}
	base := typePortBase[job]
	host := agentHost()

	ctx := r.Context()
	docs, err := enabledMetricsSources(ctx, kind)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}

	var b strings.Builder
	for _, t := range expandTargets(kind, docs, base) {
		b.WriteString("- targets:\n  - ")
		b.WriteString(host + ":" + strconv.Itoa(t.port))
		b.WriteString("\n  labels:\n    hostname: ")
		b.WriteString(t.hostname)
		b.WriteString("\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// ---- 主机(node_exporter)SD 分支：直接用文档 address，不经 agent / 端口基 ----

// promHostHTTPSD 主机 http_sd：对每条 enabled+metrics 的 hostInstance，
// 输出 target = 文档 address(原样)，labels.hostname = 文档 name。
func promHostHTTPSD(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	docs, err := enabledMetricsSources(ctx, model.KindHostInstance)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}

	type promTarget struct {
		Targets []string          `json:"targets"`
		Labels  map[string]string `json:"labels"`
	}
	out := make([]promTarget, 0, len(docs))
	for _, doc := range docs {
		addr := asString(doc["address"])
		if addr == "" {
			continue
		}
		out = append(out, promTarget{
			Targets: []string{addr},
			Labels:  map[string]string{"hostname": asString(doc["name"])},
		})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// promHostFileSD 主机 file_sd：同 promHostHTTPSD，但输出 YAML 文本(text/plain)。
func promHostFileSD(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	docs, err := enabledMetricsSources(ctx, model.KindHostInstance)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}

	var b strings.Builder
	for _, doc := range docs {
		addr := asString(doc["address"])
		if addr == "" {
			continue
		}
		b.WriteString("- targets:\n  - ")
		b.WriteString(addr)
		b.WriteString("\n  labels:\n    hostname: ")
		b.WriteString(asString(doc["name"]))
		b.WriteString("\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

func fillKafka(entry map[string]any, doc bson.M) {
	entry["brokers"] = asStringSlice(doc["brokers"])
	saslOut := map[string]any{
		"enabled":   false,
		"mechanism": "",
		"username":  "",
		"password":  "",
		"tls":       false,
		"skipTls":   false,
	}
	if raw, ok := doc["sasl"].(bson.M); ok {
		saslOut["enabled"] = asBool(raw["enabled"])
		saslOut["mechanism"] = asString(raw["mechanism"])
		saslOut["username"] = asString(raw["username"])
		saslOut["password"] = decryptSilent(asString(raw["password"]))
		saslOut["tls"] = asBool(raw["tls"])
		saslOut["skipTls"] = asBool(raw["skipTls"])
	}
	entry["sasl"] = saslOut
}

// metricCollectorArgs decodes stored options before constructing the agent's
// argument list. Older data source documents have no metricCollection field.
func metricCollectorArgs(kind string, doc bson.M) []string {
	raw, _ := doc["metricCollection"].(bson.M)
	if ordered, ok := doc["metricCollection"].(bson.D); ok {
		raw = make(bson.M, len(ordered))
		for _, field := range ordered {
			raw[field.Key] = field.Value
		}
	}
	if raw == nil {
		return nil
	}
	collection := model.MetricCollection{
		Collectors: asStringSlice(raw["collectors"]),
		Scope:      asStringSlice(raw["scope"]),
	}
	args, err := collection.ExporterArgs(kind)
	if err != nil {
		return nil
	}
	return args
}

func fillMongo(entry map[string]any, doc bson.M) {
	uri := asString(doc["uri"])
	username := asString(doc["username"])
	password := decryptSilent(asString(doc["password"]))
	// uri 内嵌的密码也可能是密文，解密后拼回
	if dec, ok := decryptMongoURIPassword(uri); ok {
		uri = dec
	}
	entry["uri"] = uri
	entry["username"] = username
	entry["password"] = password
	entry["authDb"] = asString(doc["authDb"])
	entry["tls"] = asBool(doc["tls"])
	entry["skipSSL"] = asBool(doc["skipSSL"])
}

func fillMySQL(entry map[string]any, doc bson.M) {
	entry["nodes"] = asStringSlice(doc["nodes"])
	entry["username"] = asString(doc["username"])
	entry["password"] = decryptSilent(asString(doc["password"]))
	entry["database"] = asString(doc["database"])
}

func fillRedis(entry map[string]any, doc bson.M) {
	entry["host"] = asString(doc["host"])
	entry["redisPort"] = asInt(doc["port"])
	entry["password"] = decryptSilent(asString(doc["password"]))
	entry["db"] = asInt(doc["db"])
	entry["tls"] = asBool(doc["tls"])
	entry["skipSSL"] = asBool(doc["skipSSL"])
}

func fillES(entry map[string]any, doc bson.M) {
	entry["url"] = asString(doc["url"])
	entry["username"] = asString(doc["username"])
	entry["password"] = decryptSilent(asString(doc["password"]))
	entry["skipSSL"] = asBool(doc["skipSSL"])
}

// decryptMongoURIPassword 解密 mongodb://user:pass@rest 中内嵌的密文密码并拼回。
// 仅当 uri 可切分(含 ://、@、user:pass)时返回 (newURI, true)；密码段非密文时
// decryptSilent 原样返回，故对明文 uri 也安全(等价无操作)。
func decryptMongoURIPassword(uri string) (string, bool) {
	scheme, body, ok := strings.Cut(uri, "://")
	if !ok {
		return uri, false
	}
	userInfo, rest, ok := strings.Cut(body, "@")
	if !ok {
		return uri, false
	}
	user, pass, ok := strings.Cut(userInfo, ":")
	if !ok {
		return uri, false
	}
	return scheme + "://" + user + ":" + decryptSilent(pass) + "@" + rest, true
}

// ---- bson 取值小工具(http_sd / agent 输出都来自动态 bson.M) ----

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bson.ObjectID:
		return x.Hex()
	case nil:
		return ""
	default:
		return ""
	}
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any) int {
	switch x := v.(type) {
	case int32:
		return int(x)
	case int64:
		return int(x)
	case int:
		return x
	case float64:
		return int(x)
	default:
		return 0
	}
}

func asStringSlice(v any) []string {
	// mongo 反序列化给的是 bson.A，但同样的文档也可能来自 JSON 解码([]any)或
	// 代码内构造([]string)。只认 bson.A 时后两者会静默变成空切片——多节点源
	// 会因此被当成「一个节点都没配」直接跳过采集，且没有任何报错。
	var arr []any
	switch x := v.(type) {
	case bson.A:
		arr = x
	case []any:
		arr = x
	case []string:
		return append([]string{}, x...)
	default:
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ============================================================================
// ops-agent 采集状态上报（1.4.8）
//
// UI「数据源」页要能直接显示每个源「有没有真的在采、采不到是为什么」。失败原因只有
// agent 手上有（exporter 的退出输出落在它容器内的 logs/*.log），所以由 agent 定期推上来，
// ops-server 只做暂存与转发——客户不必再去猜该进哪个容器看哪个日志文件。
// ============================================================================

// AgentExporterStatus 单个 exporter 的状态（字段与 roles/agent.sh 的上报 payload 对齐）。
type AgentExporterStatus struct {
	Type       string `json:"type"`
	Port       int    `json:"port"`
	Target     string `json:"target"`
	Running    bool   `json:"running"`
	Collecting bool   `json:"collecting"`
	Reason     string `json:"reason"`
}

type agentStatusPayload struct {
	Mode      string                `json:"mode"`
	Exporters []AgentExporterStatus `json:"exporters"`
}

var (
	agentStatusMu   sync.RWMutex
	agentStatusData agentStatusPayload
	agentStatusAt   time.Time
)

// ReceiveAgentStatus POST /api/sd/agent-status
func ReceiveAgentStatus(w http.ResponseWriter, r *http.Request) {
	if !checkSDToken(w, r) {
		return
	}
	var p agentStatusPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	agentStatusMu.Lock()
	agentStatusData = p
	agentStatusAt = time.Now()
	agentStatusMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AgentStatusSnapshot 返回最近一次上报（含时间；零值表示从未上报）。
func AgentStatusSnapshot() (string, []AgentExporterStatus, time.Time) {
	agentStatusMu.RLock()
	defer agentStatusMu.RUnlock()
	return agentStatusData.Mode, agentStatusData.Exporters, agentStatusAt
}

// SourcePortOf 返回某数据源在 SD 端口方案里分配到的端口（与 SourcesForAgent 完全同一套规则），
// 供 UI 侧把「数据源」和 agent 上报的 exporter 对应起来。找不到返回 0。
func SourcePortOf(ctx context.Context, typ, id string) int {
	ports := SourcePortsOf(ctx, typ, id)
	if len(ports) == 0 {
		return 0
	}
	return ports[0]
}

// SourcePortsOf 返回某数据源占用的全部端口。多节点源（MySQL/Redis/ES 配了多个节点）
// 一条源会展开成多个 exporter，各占一个端口——只返回第一个的话，其余节点的采集状态
// 在 UI 上会落进「未匹配」，看着像多出来几个来路不明的采集进程。
func SourcePortsOf(ctx context.Context, typ, id string) []int {
	base, ok := typePortBase[typ]
	if !ok {
		return nil
	}
	kind := typeToKind[typ]
	docs, err := enabledMetricsSources(ctx, kind)
	if err != nil {
		return nil
	}
	var ports []int
	for _, t := range expandTargets(kind, docs, base) {
		if asString(t.doc["_id"]) == id {
			ports = append(ports, t.port)
		}
	}
	return ports
}
