package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"gopkg.in/yaml.v3"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// ============================================================================
// 数据源批量导入（1.5.0）。
//
// 取代 1.4.x 的「ops.yaml ENV_* 首次启动播种」。那套的问题不在于用环境变量，
// 而在于它是第二个入口：两套语法（ENV 多目标用 ";"，UI 用换行/逗号）、只生效一次、
// 出错无反馈。真实案例：客户把三节点 MySQL 写成 "h1,h2,h3" + "3306,3307,3308"，
// 播种出一条 nodes=["h1,h2,h3:3306,3307,3308"] 的假地址——UI 上看着正常、agent 不报错、
// 改 ops.yaml 也不再生效，只能一层层往回刨才能发现。
//
// 所以本接口的重点不是「换个地方填」，而是**导入即验证**：逐条校验 + 逐条探测连接，
// 明确告诉用户哪条不通、为什么。批量的需求（一次接十几个实例）由它承接。
//
// 与 UI 表单共用同一套 model / 校验 / 加密 / 探测，不存在第二套解析逻辑。
// ============================================================================

// importPayload 是导入文件的顶层结构。YAML 与 JSON 同构，字段名与各数据源的
// JSON API 完全一致——YAML 先解析成 map 再走 json.Unmarshal 进 model，
// 因此不需要给每个 model 补一套 yaml tag，也不会出现两种格式字段名对不上。
type importPayload struct {
	Sources []map[string]any `json:"sources" yaml:"sources"`
}

// importItemResult 是单条导入的结果。无论成功失败都逐条返回，前端按 action 着色。
type importItemResult struct {
	Index int    `json:"index"`
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`
	// created / updated / failed —— failed 表示校验没过，该条未写入。
	Action   string `json:"action"`
	ErrorKey string `json:"errorKey,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// 连接探测结果。探测失败不阻止导入（与手工保存的行为一致：
	// 「此刻通不通」不该否决配置，但必须让用户知情），只在这里如实标出。
	ConnectOK    bool   `json:"connectOK"`
	ConnectError string `json:"connectError,omitempty"`
}

type importResponse struct {
	Total   int                `json:"total"`
	Created int                `json:"created"`
	Updated int                `json:"updated"`
	Failed  int                `json:"failed"`
	DryRun  bool               `json:"dryRun"`
	Results []importItemResult `json:"results"`
}

// importMaxBytes 限制导入文件大小，防止误传大文件把内存打满。
const importMaxBytes = 2 << 20 // 2 MiB

// ImportSources POST /api/alert/sources/import
// Body：YAML 或 JSON 文本（按内容自动识别，JSON 是 YAML 的子集，统一走 YAML 解析器）。
// Query：dryRun=true 时只校验 + 探测，不写库——用于导入前预检。
//
// 逐条独立处理：某条校验失败只标记该条，不影响其余条目写入（批量导入里
// 因为一条格式错误整批回滚，比部分成功更难排查）。
func ImportSources(w http.ResponseWriter, r *http.Request) {
	dryRun := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("dryRun")), "true")

	raw, err := io.ReadAll(io.LimitReader(r.Body, importMaxBytes))
	if err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_import_empty")
		return
	}

	var payload importPayload
	if err := yaml.Unmarshal(raw, &payload); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_import_parse_failed", err.Error())
		return
	}
	if len(payload.Sources) == 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_import_no_sources")
		return
	}

	ctx := r.Context()
	resp := importResponse{Total: len(payload.Sources), DryRun: dryRun}
	resp.Results = make([]importItemResult, 0, len(payload.Sources))

	for i, item := range payload.Sources {
		res := importOne(ctx, i, item, dryRun)
		switch res.Action {
		case "created":
			resp.Created++
		case "updated":
			resp.Updated++
		default:
			resp.Failed++
		}
		resp.Results = append(resp.Results, res)
	}

	httputil.WriteJSON(w, http.StatusOK, resp)
}

// importOne 处理单条：解析 → 校验 → 探测 → 写库（dryRun 时跳过写库）。
func importOne(ctx context.Context, idx int, item map[string]any, dryRun bool) importItemResult {
	res := importItemResult{Index: idx, Action: "failed"}

	kind := strings.TrimSpace(asString(item["kind"]))
	if kind == "" {
		res.ErrorKey = "alert_import_kind_required"
		return res
	}
	res.Kind = kind
	res.Name = strings.TrimSpace(asString(item["name"]))

	// 自定义页面(1.5.12)不是被监控数据源，不在 model.SourceKinds 里（加进去会让
	// /api/alert/sources 聚合列表把它当数据源返回，前端那边已经单独拉了一份，会重复显示），
	// 所以在通用校验之前单独分流。
	if kind == kindCustomPage {
		return importCustomPage(ctx, res, item, dryRun)
	}

	if !model.IsValidSourceKind(kind) || kind == model.KindSource {
		// KindSource(Prometheus) 是平台内置单例，不接受导入。
		res.ErrorKey = "alert_import_kind_unsupported"
		res.Detail = kind
		return res
	}
	if res.Name == "" {
		res.ErrorKey = "common_name_required"
		return res
	}

	// 凭据回填：导入文件里没写密码时，沿用库里已有的那条的密码，不要把它清空。
	// 没有这一步的话，「只想改个地址」的重复导入会静默把密码抹掉——采集随后失败，
	// 而页面上一切正常。这正是本次要根除的那类静默破坏。必须在 buildImportDoc 之前做，
	// 连接探测才能用上正确的凭据（否则会误报连不上）。
	carryOverSecrets(ctx, kind, res.Name, item)

	// YAML map → JSON → model，复用各 model 既有的 json tag，不维护第二套字段名。
	blob, err := json.Marshal(item)
	if err != nil {
		res.ErrorKey = "alert_import_parse_failed"
		res.Detail = err.Error()
		return res
	}

	doc, errKey, detail, probe := buildImportDoc(kind, blob)
	if errKey != "" {
		res.ErrorKey, res.Detail = errKey, detail
		return res
	}

	// 探测连接：不阻止导入，只如实记录。放在写库前，dryRun 也能拿到探测结果。
	if probe != nil {
		if connErr, _ := probe(); connErr != nil {
			res.ConnectError = connErr.Error()
		} else {
			res.ConnectOK = true
		}
	}

	if dryRun {
		// 预检：报告「会怎么做」，但不落库。
		if existsSourceByName(ctx, kind, res.Name) {
			res.Action = "updated"
		} else {
			res.Action = "created"
		}
		return res
	}

	action, err := upsertImported(ctx, kind, res.Name, doc)
	if err != nil {
		res.ErrorKey = "alert_import_write_failed"
		res.Detail = err.Error()
		return res
	}
	res.Action = action
	return res
}

// buildImportDoc 按 kind 反序列化 + 归一 + 校验 + 加密，返回可直接落库的文档
// 与一个连接探测闭包（nil 表示该类型不做探测）。
func buildImportDoc(kind string, blob []byte) (doc any, errKey, detail string, probe func() (error, map[string]service.CapResult)) {
	now := time.Now()

	switch kind {
	case model.KindMySQLInstance:
		inst := model.MySQLInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		if _, err := inst.MetricCollection.ExporterArgs(kind); err != nil {
			return nil, "monitor_collectors_invalid", err.Error(), nil
		}
		inst.Normalize()
		if inst.Username == "" || inst.Password == "" || inst.Database == "" {
			return nil, "alert_mysql_conn_fields_required", "", nil
		}
		if !isValidMySQLArchitecture(inst.Architecture) {
			return nil, "alert_invalid_mysql_arch", inst.Architecture, nil
		}
		if err := validateMySQLInstanceNodes(inst.Architecture, inst.Nodes); err != nil {
			return nil, err.Error(), "", nil
		}
		plain := inst.Password
		enc, err := crypto.Encrypt(inst.Password)
		if err != nil {
			return nil, "common_encrypt_password_failed", err.Error(), nil
		}
		inst.Password = enc
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		probeInst := inst
		probeInst.Password = plain
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeMySQLCapabilities(&probeInst)
		}

	case model.KindMongoInstance:
		inst := model.MongoInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		if _, err := inst.MetricCollection.ExporterArgs(kind); err != nil {
			return nil, "monitor_collectors_invalid", err.Error(), nil
		}
		if strings.TrimSpace(inst.URI) == "" {
			return nil, "alert_mongo_uri_required", "", nil
		}
		// builtin 是平台内部标记，不接受从导入文件设置——否则用户能造出
		// 一条自己也删不掉、改不了的数据源（1.4.x 的 builtin-diagnose-mongo 就是这个坑）。
		inst.Builtin = false
		plain := inst.Password
		if inst.Password != "" {
			enc, err := crypto.Encrypt(inst.Password)
			if err != nil {
				return nil, "common_encrypt_password_failed", err.Error(), nil
			}
			inst.Password = enc
		}
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		probeInst := inst
		probeInst.Password = plain
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeMongoCapabilities(&probeInst)
		}

	case model.KindRedisInstance:
		inst := model.RedisInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		if strings.TrimSpace(inst.Host) == "" {
			return nil, "alert_host_required", "", nil
		}
		if inst.Port <= 0 || inst.Port > 65535 {
			return nil, "alert_invalid_port", "", nil
		}
		plain := inst.Password
		if inst.Password != "" {
			enc, err := crypto.Encrypt(inst.Password)
			if err != nil {
				return nil, "common_encrypt_password_failed", err.Error(), nil
			}
			inst.Password = enc
		}
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		host, port, db, tlsOn, skip := inst.Host, inst.Port, inst.DB, inst.TLS, inst.SkipSSL
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeRedisCapabilities(host, port, plain, db, tlsOn, skip)
		}

	case model.KindESInstance:
		inst := model.ESInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		inst.URL = strings.TrimSpace(inst.URL)
		if inst.URL == "" {
			return nil, "alert_es_url_required", "", nil
		}
		// 与 UI 表单一致：没写 scheme 时补 http://，避免「填了地址却连不上」。
		if !strings.HasPrefix(inst.URL, "http://") && !strings.HasPrefix(inst.URL, "https://") {
			inst.URL = "http://" + inst.URL
		}
		plain := inst.Password
		if inst.Password != "" {
			enc, err := crypto.Encrypt(inst.Password)
			if err != nil {
				return nil, "common_encrypt_password_failed", err.Error(), nil
			}
			inst.Password = enc
		}
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		esURL, user, skip := inst.URL, inst.Username, inst.SkipSSL
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeESCapabilities(esURL, user, plain, skip)
		}

	case model.KindKafkaInstance:
		inst := model.KafkaInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		var brokers []string
		for _, b := range inst.Brokers {
			if b = strings.TrimSpace(b); b != "" {
				brokers = append(brokers, b)
			}
		}
		inst.Brokers = brokers
		if len(inst.Brokers) == 0 {
			return nil, "alert_kafka_brokers_required", "", nil
		}
		if inst.SASL != nil && inst.SASL.Enabled && !inst.SASL.IsValidMechanism() {
			return nil, "alert_invalid_sasl_mechanism", inst.SASL.Mechanism, nil
		}
		probeInst := inst
		if inst.SASL != nil && inst.SASL.Password != "" {
			saslCopy := *inst.SASL
			enc, err := crypto.Encrypt(inst.SASL.Password)
			if err != nil {
				return nil, "common_encrypt_password_failed", err.Error(), nil
			}
			saslCopy.Password = enc
			inst.SASL = &saslCopy
		}
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeKafkaCapabilities(&probeInst)
		}

	case model.KindHostInstance:
		inst := model.HostInstance{Enabled: true}
		if err := json.Unmarshal(blob, &inst); err != nil {
			return nil, "alert_import_parse_failed", err.Error(), nil
		}
		inst.Address = strings.TrimSpace(inst.Address)
		if inst.Address == "" {
			return nil, "alert_host_address_required", "", nil
		}
		stampInstance(&inst.ID, &inst.Kind, kind, &inst.Purposes, &inst.CreatedAt, &inst.UpdatedAt, now)
		// 主机只采指标，没有告警用途的语义，覆盖成 metrics。
		inst.Purposes = []string{"metrics"}
		addr := inst.Address
		return inst, "", "", func() (error, map[string]service.CapResult) {
			return service.ProbeHostCapabilities(addr)
		}

	}

	return nil, "alert_import_kind_unsupported", kind, nil
}

// stampInstance 补齐各实例共有的落库字段。purposes 缺省与 UI 新建表单一致（看图 + 告警）——
// 缺省为空会导致「导入成功、列表里看得到，却永远不采集」，比 enabled=false 更隐蔽。
func stampInstance(id, kindField *string, kind string, purposes *[]string, createdAt, updatedAt *time.Time, now time.Time) {
	if *id == "" {
		*id = bson.NewObjectID().Hex()
	}
	*kindField = kind
	if *purposes == nil || (kind != model.KindMongoInstance && len(*purposes) == 0) {
		*purposes = []string{"metrics", "alert"}
	}
	*createdAt, *updatedAt = now, now
}

// carryOverSecrets 把库里已有同名数据源的凭据回填进本次导入的 item（仅在 item 未提供时）。
// 涉及三处：顶层 password、Kafka 的 sasl.password、K8s 的 bearerToken。
// 库里存的是密文，这里解密成明文放回 item —— 后续 buildImportDoc 会重新加密，
// 探测闭包也需要明文。解密失败就不回填（宁可让用户重填，也不要写入一段解不开的密文）。
func carryOverSecrets(ctx context.Context, kind, name string, item map[string]any) {
	if name == "" {
		return
	}
	var existing bson.M
	if err := database.Config().FindOne(ctx, bson.M{"kind": kind, "name": name}).Decode(&existing); err != nil {
		return // 新建，无可回填
	}

	// 顶层单字段：item 里没写或写成空串时才回填
	carry := func(field string) {
		if v, ok := item[field]; ok && strings.TrimSpace(asString(v)) != "" {
			return
		}
		enc := asString(existing[field])
		if enc == "" {
			return
		}
		if plain, err := crypto.Decrypt(enc); err == nil {
			item[field] = plain
		}
	}
	carry("password")
	carry("bearerToken")

	// Kafka 的 sasl 是嵌套对象，$set 会整体覆盖，所以密码要单独补进这一层
	if kind == model.KindKafkaInstance {
		sasl, ok := item["sasl"].(map[string]any)
		if !ok {
			return
		}
		if v, exists := sasl["password"]; exists && strings.TrimSpace(asString(v)) != "" {
			return
		}
		oldSasl, ok := existing["sasl"].(bson.M)
		if !ok {
			return
		}
		enc := asString(oldSasl["password"])
		if enc == "" {
			return
		}
		if plain, err := crypto.Decrypt(enc); err == nil {
			sasl["password"] = plain
		}
	}
}

// existsSourceByName 判断同 kind 下是否已有同名数据源（dryRun 预判 created/updated 用）。
// importCustomPage 处理一条 kind: customPage。与其它类型同样的语义：
// 按 name 幂等（同名是更新不是新建）、探测但不否决、dryRun 只报告不落库。
//
// 幂等键用 name 而不是 id：id 是访问路径，用户手写导入文件时未必填。
// 已存在同名条目时保留它原本的 id —— 那是发出去的菜单链接，导一次配置不该让它失效。
func importCustomPage(ctx context.Context, res importItemResult, item map[string]any, dryRun bool) importItemResult {
	if res.Name == "" {
		res.ErrorKey = "common_name_required"
		return res
	}
	req := customPageReq{
		Name: res.Name,
		URL:  strings.TrimSpace(asString(item["url"])),
		Mode: strings.TrimSpace(asString(item["mode"])),
	}
	if v, ok := item["enabled"].(bool); ok {
		req.Enabled = &v
	}
	name, url, mode, enabled, errKey := req.normalize()
	if errKey != "" {
		res.ErrorKey = errKey
		return res
	}

	if connErr, _ := service.ProbeWebUICapabilities(url); connErr != nil {
		res.ConnectError = connErr.Error()
	} else {
		res.ConnectOK = true
	}

	existing, found := findCustomPageByName(ctx, name)
	if dryRun {
		res.Action = "created"
		if found {
			res.Action = "updated"
		}
		return res
	}

	now := time.Now()
	set := bson.M{
		"kind": kindCustomPage, "name": name, "url": url,
		"mode": mode, "enabled": enabled, "updatedAt": now,
	}
	// injectCss 只在文件里明确给了才写：导入文件没这个字段时，别把已有的样式抹掉。
	if css, ok := item["injectCss"]; ok {
		set["injectCss"] = asString(css)
	}

	if found {
		if _, err := database.Config().UpdateOne(ctx,
			bson.M{"_id": existing.ID, "kind": kindCustomPage}, bson.M{"$set": set}); err != nil {
			res.ErrorKey, res.Detail = "alert_import_write_failed", err.Error()
			return res
		}
		res.Action = "updated"
		return res
	}

	// 新建：优先用文件里给的 id（导出文件带 id，还原后访问路径不变），
	// 没给或撞了就按名称生成一个。
	id := slugify(strings.TrimSpace(asString(item["id"])))
	if id == "" || strings.HasPrefix(id, "page-") {
		id = slugify(name)
	}
	id = uniqueSlug(ctx, id)
	set["_id"] = id
	set["createdAt"] = now
	if _, err := database.Config().InsertOne(ctx, set); err != nil {
		res.ErrorKey, res.Detail = "alert_import_write_failed", err.Error()
		return res
	}
	res.Action = "created"
	return res
}

// findCustomPageByName 按名称找自定义页面（导入的幂等键）。
func findCustomPageByName(ctx context.Context, name string) (customPageDoc, bool) {
	var doc customPageDoc
	if err := database.Config().FindOne(ctx,
		bson.M{"kind": kindCustomPage, "name": name}).Decode(&doc); err != nil {
		return customPageDoc{}, false
	}
	return doc, true
}

func existsSourceByName(ctx context.Context, kind, name string) bool {
	n, err := database.Config().CountDocuments(ctx, bson.M{"kind": kind, "name": name})
	return err == nil && n > 0
}

// upsertImported 按 (kind, name) upsert：同名视为同一条，重复导入同一份文件是幂等的，
// 不会每导一次就长出一批重复数据源。返回 "created" 或 "updated"。
//
// _id 不参与 $set：更新已有条目时保留原 _id，否则 exporter 端口（按 (类型, _id 升序)
// 确定性分配）会在每次导入后漂移，Prometheus 那边看起来像是整批目标被换掉了。
func upsertImported(ctx context.Context, kind, name string, doc any) (string, error) {
	set, err := toSetDoc(doc)
	if err != nil {
		return "", err
	}
	newID, _ := set["_id"].(string)
	delete(set, "_id")
	delete(set, "createdAt") // 保留原始创建时间

	res, err := database.Config().UpdateOne(ctx,
		bson.M{"kind": kind, "name": name},
		bson.M{"$set": set, "$setOnInsert": bson.M{"_id": newID, "createdAt": time.Now()}},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return "", err
	}
	if res.UpsertedCount > 0 {
		return "created", nil
	}
	return "updated", nil
}

// toSetDoc 把 model 结构体转成 bson.M（走 bson tag，与直接 InsertOne 落库结构一致）。
func toSetDoc(doc any) (bson.M, error) {
	raw, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var m bson.M
	if err := bson.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// asString 把 YAML 解出来的任意值取成字符串（kind/name 这类标量字段用）。
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// ImportTemplate GET /api/alert/sources/import-template
// 下载 YAML 模板。带注释、每种类型给一条可直接改的示例——
// 客户本来就在写 YAML（ops.yaml/K8s 清单），没有额外学习成本；
// 而 CSV 表达不了 Kafka SASL、MySQL 多节点这类嵌套结构，会把分隔符的坑原样搬过来。
func ImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="datasources-template.yaml"`)
	_, _ = io.WriteString(w, importTemplateYAML)
}

const importTemplateYAML = `# 数据源批量导入模板
#
# 用法：删掉用不到的条目，改成自己的地址，在「数据源」页上传。
#   · 先用「仅校验」跑一遍：只做格式校验和连接探测，不写入。
#   · 按 (kind, name) 幂等：同名条目是更新而不是新建，同一份文件可以反复导入。
#   · 更新已有条目时，密码留空 = 沿用原密码（想改密码才填）。
#   · 探测不通不会阻止导入（配置可以先落地、服务稍后再起），但结果里会逐条标出来。
#
# purposes 可选值：
#   metrics  纳入指标采集（Prometheus 抓取，用于看图）
#   alert    纳入告警监控
#   diagnose 仅 MongoDB：作为慢查询诊断的目标库
# 省略 purposes 时默认 [metrics, alert]。

sources:
  # ---------------- MySQL ----------------
  - kind: mysqlInstance
    name: mysql-prod
    # architecture: standalone(单机) / replication(主从) / mgr(组复制)
    architecture: mgr
    # 每个节点一行，格式 host:port。单机模式只能填一个，主从/组复制至少两个。
    # ⚠️ 集群请填各节点的真实地址，不要填读写分离中间件（如 mysqlrouter）的入口——
    #    那样只能采到被路由到的那一个后端，成员状态采不全。
    nodes:
      - 10.0.0.1:3306
      - 10.0.0.2:3306
      - 10.0.0.3:3306
    username: monitor
    password: "改成真实密码"
    database: mysql
    purposes: [metrics, alert]
    enabled: true

  # ---------------- MongoDB ----------------
  - kind: mongoInstance
    name: mongo-prod
    # 副本集把成员都写进 URI，用逗号分隔；需要时可加 ?replicaSet=xxx
    uri: mongodb://10.0.0.1:27017,10.0.0.2:27017,10.0.0.3:27017
    username: monitor
    password: "改成真实密码"
    authDb: admin
    # 慢查询诊断的目标库要带 diagnose；账号需要 clusterMonitor 角色。
    purposes: [metrics, alert, diagnose]
    enabled: true

  # ---------------- Redis ----------------
  # 一个实例一条。哨兵/集群模式把各节点分别列出来即可，
  # exporter 逐实例采集，主从切换后角色标签会自动跟着变。
  - kind: redisInstance
    name: redis-1
    host: 10.0.0.1
    port: 6379
    password: "改成真实密码"
    db: 0
    purposes: [metrics, alert]
    enabled: true

  # ---------------- Kafka ----------------
  - kind: kafkaInstance
    name: kafka-prod
    # 同一集群的 broker 全部列出
    brokers:
      - 10.0.0.1:9092
      - 10.0.0.2:9092
      - 10.0.0.3:9092
    # 无 SASL 时整段删掉即可
    # sasl:
    #   enabled: true
    #   mechanism: SCRAM-SHA-512   # PLAIN / SCRAM-SHA-256 / SCRAM-SHA-512
    #   username: monitor
    #   password: "改成真实密码"
    #   tls: false
    purposes: [metrics, alert]
    enabled: true

  # ---------------- Elasticsearch ----------------
  # 同一集群填任一节点即可采到全集群；想避免单点就每个节点建一条。
  - kind: esInstance
    name: elasticsearch-prod
    url: http://10.0.0.1:9200
    username: elastic
    password: "改成真实密码"
    purposes: [metrics, alert]
    enabled: true

  # ---------------- 主机（node_exporter）----------------
  # ⚠️ Compose 部署机与 K8s 集群内的节点已内置 ops-nodeagent（自动监听 59100），
  #    不需要在这里重复添加，也不需要另装 node_exporter。只有额外的外部主机才需要。
  - kind: hostInstance
    name: node-01
    address: 10.0.0.10:59100
    purposes: [metrics]
    enabled: true

  # ---------------- 自定义页面（外链 Web UI）----------------
  # 把任意一个已有的 Web 界面挂进左侧「自定义」菜单，不是被监控数据源，不采指标。
  # mode 两种嵌法，选错的表现是「打开一片空白」，别凭感觉选：
  #   proxy  经平台反代，同源、带平台鉴权，目标只要平台服务器能访问就行。
  #          适合资源用相对路径的服务（Flink 就是这类）。
  #   iframe 浏览器直接加载目标地址。要求目标对浏览器可达、且没有
  #          X-Frame-Options / CSP frame-ancestors 限制。资源走绝对路径的应用
  #          （Next.js 那种 /_next/*，如 Langfuse）只能用这种。
  # id 是访问路径（/custom/<id>），省略时按名称自动生成。
  - kind: customPage
    name: Langfuse
    url: http://langfuse:3000
    mode: iframe
    enabled: true

  # ---------------- K8s 集群 ----------------
  # 每个目标写成 {alias, address}；不用的目标类型整段删掉。
  # - kind: k8sClusterInstance
  #   name: prod-cluster
  #   ksmTargets:
  #     - alias: ksm
  #       address: kube-state-metrics.kube-system:8080
  #   nodeTargets:
  #     - alias: node-01
  #       address: 10.0.0.10:9100
  #   kubeletTargets:
  #     - alias: node-01
  #       address: 10.0.0.10:10250
  #   bearerToken: "改成真实 token"
  #   enabled: true
`
