package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// ============================================================================
// 数据源配置导出（1.5.9）。导入的逆向：产出的 YAML 能被 POST /sources/import
// 原样吃回去，用于备份、换环境重建、多套环境复制同一份数据源清单。
//
// 之前只有「导入」没有「导出」，重装一次就得照着旧页面一条条手敲重填
// （真实客户反馈：「每次重启，都要一步一步导数据源进去」）。
//
// 密码默认不导出（留空），因为：
//   · 导回同一套平台时，导入侧的 carryOverSecrets 会按 (kind,name) 沿用库里
//     已存的密码 —— 留空恰好是「别动密码」的正确写法；
//   · 留空 ≠ 抹掉密码，而写占位串才会真把密码覆盖坏，所以宁可空着。
// 要做真备份（换机、库也一起没了）才用 includeSecrets=true 导出明文。
// ============================================================================

// ExportSources GET /api/alert/sources/export
// Query：includeSecrets=true 时导出明文密码（默认不导）。
//
// 输出是带注释的 YAML，字段名与导入模板 / 各数据源 API 完全一致。
// 内置 Prometheus（KindSource）不导出：它是平台单例，导入侧也不接受该 kind。
func ExportSources(w http.ResponseWriter, r *http.Request) {
	includeSecrets := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("includeSecrets")), "true")

	var (
		blocks []string
		counts = map[string]int{}
		total  int
	)
	// 顺序固定（与导入模板同序），同类内按 name 排序 —— 导出两次内容一致，
	// 可以直接 diff 出「这套环境和那套差了哪条数据源」。
	for _, kind := range []string{
		model.KindMySQLInstance,
		model.KindMongoInstance,
		model.KindRedisInstance,
		model.KindKafkaInstance,
		model.KindESInstance,
		model.KindHostInstance,
	} {
		items, err := exportKind(r, kind, includeSecrets)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
			return
		}
		if len(items) == 0 {
			continue
		}
		counts[kind] = len(items)
		total += len(items)
		blocks = append(blocks, items...)
	}

	// 自定义页面（1.5.12）：不是被监控数据源，但同样是「重装一次就得照着旧页面
	// 一条条手敲」的东西，一并纳入导出/导入。它没有密码字段，不受 includeSecrets 影响。
	pages, err := exportCustomPages(r)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	if len(pages) > 0 {
		counts[kindCustomPage] = len(pages)
		total += len(pages)
		blocks = append(blocks, pages...)
	}

	var b strings.Builder
	b.WriteString(exportHeader(total, counts, includeSecrets))
	if total == 0 {
		// 没有数据源时也给一份合法 YAML（sources: []），而不是空文件——
		// 空文件导回去会报「解析失败」，让人以为导出坏了。
		b.WriteString("sources: []\n")
	} else {
		b.WriteString("sources:\n")
		for _, blk := range blocks {
			b.WriteString(blk)
		}
	}

	filename := "datasources-" + time.Now().Format("20060102-150405") + ".yaml"
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte(b.String()))
}

// exportHeader 文件头注释。把「这份文件能不能直接导回去」讲清楚——
// 密码留空的语义是本功能唯一容易被误解的地方。
func exportHeader(total int, counts map[string]int, includeSecrets bool) string {
	var parts []string
	for kind, n := range counts {
		label := model.TypeForKind(kind)
		if kind == kindCustomPage {
			// 自定义页面不在 model 的 kind→type 表里（它不是被监控数据源）
			label = "自定义页面"
		}
		parts = append(parts, fmt.Sprintf("%s %d", label, n))
	}
	sort.Strings(parts)

	var b strings.Builder
	b.WriteString("# MDIS 数据源配置导出\n")
	b.WriteString("# 导出时间：" + time.Now().Format("2006-01-02 15:04:05 -0700") + "\n")
	fmt.Fprintf(&b, "# 共 %d 条", total)
	if len(parts) > 0 {
		b.WriteString("（" + strings.Join(parts, " / ") + "）")
	}
	b.WriteString("\n#\n")
	b.WriteString("# 用法：在「数据源」页点「批量导入」上传本文件即可还原。\n")
	b.WriteString("#       按 (kind, name) 幂等——同名是更新不是新建，可以反复导入。\n")
	b.WriteString("#\n")
	if includeSecrets {
		b.WriteString("# ⚠️ 本文件含明文密码，请妥善保管：别提交进代码仓库、别丢在共享目录。\n")
	} else {
		b.WriteString("# ⚠️ 本文件不含密码（password / sasl.password 均为空，内嵌凭据的 MongoDB URI 也已去掉密码）。\n")
		b.WriteString("#    · 导回同一套平台：留空即沿用平台里已存的密码，不用改也不会被清空。\n")
		b.WriteString("#    · 换到新平台恢复：必须把密码补进来，否则采集会因认证失败起不来。\n")
		b.WriteString("#    · 要连密码一起备份：导出时勾选「包含密码」。\n")
	}
	b.WriteString("\n")
	return b.String()
}

// exportKind 取一类数据源的所有文档，渲染成 YAML 条目块（每块含结尾换行）。
func exportKind(r *http.Request, kind string, includeSecrets bool) ([]string, error) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kind})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var out []string
	switch kind {
	case model.KindMySQLInstance:
		var docs []model.MySQLInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderMySQLExport(d, includeSecrets))
		}

	case model.KindMongoInstance:
		var docs []model.MongoInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderMongoExport(d, includeSecrets))
		}

	case model.KindRedisInstance:
		var docs []model.RedisInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderRedisExport(d, includeSecrets))
		}

	case model.KindKafkaInstance:
		var docs []model.KafkaInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderKafkaExport(d, includeSecrets))
		}

	case model.KindESInstance:
		var docs []model.ESInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderESExport(d, includeSecrets))
		}

	case model.KindHostInstance:
		var docs []model.HostInstance
		if err := cur.All(ctx, &docs); err != nil {
			return nil, err
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			out = append(out, renderHostExport(d))
		}
	}

	return out, nil
}

// ── 各类型的 YAML 渲染（与 DB 解耦，便于「导出的东西导得回去」的回归测试）──────

func renderMySQLExport(d model.MySQLInstance, includeSecrets bool) string {
	it := newExportItem(model.KindMySQLInstance, d.ID, d.Name)
	it.str("architecture", d.Architecture)
	it.list("nodes", d.Nodes)
	it.str("username", d.Username)
	it.secret("password", d.Password, includeSecrets)
	it.str("database", d.Database)
	it.boolIfTrue("tls", d.TLS)
	it.boolIfTrue("skipSSL", d.SkipSSL)
	it.metricCollection(d.MetricCollection)
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

func renderMongoExport(d model.MongoInstance, includeSecrets bool) string {
	it := newExportItem(model.KindMongoInstance, d.ID, d.Name)
	// URI 里可能内嵌 user:pass@。不导密码时把凭据段整段去掉而不是打码——
	// 打码后的串导回去会被当成真密码写进库，采集随后失败且看不出原因。
	uri := d.URI
	if !includeSecrets {
		if stripped, had := stripMongoURICredentials(uri); had {
			uri = stripped
			it.comment("URI 里原本内嵌了账号密码，导出时已去掉；换环境恢复时请补回")
		}
	}
	it.str("uri", uri)
	it.str("username", d.Username)
	it.secret("password", d.Password, includeSecrets)
	it.str("authDb", d.AuthDB)
	it.boolIfTrue("tls", d.TLS)
	it.boolIfTrue("skipSSL", d.SkipSSL)
	it.metricCollection(d.MetricCollection)
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

func renderRedisExport(d model.RedisInstance, includeSecrets bool) string {
	it := newExportItem(model.KindRedisInstance, d.ID, d.Name)
	it.str("host", d.Host)
	it.raw("port", strconv.Itoa(d.Port))
	it.secret("password", d.Password, includeSecrets)
	it.raw("db", strconv.Itoa(d.DB))
	it.boolIfTrue("tls", d.TLS)
	it.boolIfTrue("skipSSL", d.SkipSSL)
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

func renderKafkaExport(d model.KafkaInstance, includeSecrets bool) string {
	it := newExportItem(model.KindKafkaInstance, d.ID, d.Name)
	it.list("brokers", d.Brokers)
	// 没开 SASL 就整段不写：导入侧对 sasl 的校验只在 enabled 时才做机制合法性，
	// 但一段空壳 sasl 会让人以为「配了却没生效」，不如不出现。
	if d.SASL != nil && (d.SASL.Enabled || d.SASL.Username != "") {
		it.raw("sasl", "")
		it.sub("enabled", boolLiteral(d.SASL.Enabled))
		it.sub("mechanism", yamlString(d.SASL.Mechanism))
		it.sub("username", yamlString(d.SASL.Username))
		it.sub("password", yamlString(exportSecretValue(d.SASL.Password, includeSecrets)))
		it.sub("tls", boolLiteral(d.SASL.TLS))
		if d.SASL.SkipTLS {
			it.sub("skipTls", boolLiteral(true))
		}
	}
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

func renderESExport(d model.ESInstance, includeSecrets bool) string {
	it := newExportItem(model.KindESInstance, d.ID, d.Name)
	it.str("url", d.URL)
	it.str("username", d.Username)
	it.secret("password", d.Password, includeSecrets)
	it.boolIfTrue("skipSSL", d.SkipSSL)
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

func renderHostExport(d model.HostInstance) string {
	it := newExportItem(model.KindHostInstance, d.ID, d.Name)
	it.str("address", d.Address)
	it.tail(d.Purposes, d.Enabled)
	return it.String()
}

// exportCustomPages 取全部自定义页面，按 name 排序渲染。
// 没有密码字段，所以不需要 includeSecrets。
func exportCustomPages(r *http.Request) ([]string, error) {
	docs, err := listCustomPageDocs(r.Context())
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, renderCustomPageExport(d))
	}
	return out, nil
}

func renderCustomPageExport(d customPageDoc) string {
	// id 就是访问路径（/custom/<id>），一并导出，还原后菜单链接不变。
	it := newExportItem(kindCustomPage, d.ID, d.Name)
	it.str("url", strings.TrimSpace(d.URL))
	it.str("mode", d.mode())
	if d.InjectCSS != "" {
		// 迁移过来的 Flink 带一段注入样式。写成单行双引号（换行转义成 \n）——
		// 不好看，但能原样导回去；不导的话恢复后样式会悄悄丢掉。
		it.comment("injectCss 是注入到目标页面的样式，一般只有从旧版 Flink 入口迁来的那条才有")
		it.str("injectCss", d.InjectCSS)
	}
	it.line("enabled", boolLiteral(d.enabled()))
	return it.String()
}

// ── YAML 条目渲染 ──────────────────────────────────────────────────────────
//
// 手写而不是 yaml.Marshal：要的是「人能看懂、能直接改」的文件——字段顺序与
// 导入模板一致、能按条插注释说明（如 MongoDB 去掉凭据那句）。yaml.Marshal
// 出来的 map 顺序和注释都做不到。

type exportItem struct {
	b     strings.Builder
	first bool
}

func newExportItem(kind, id, name string) *exportItem {
	it := &exportItem{first: true}
	it.raw("kind", kind)
	// id 一并导出：还原时沿用原 id，exporter 端口（按 (类型, _id 升序) 分配）
	// 不会漂移，Prometheus 那边不会看起来像整批目标被换掉。
	it.str("id", id)
	it.str("name", name)
	return it
}

// line 写一行 key: value（value 已是最终 YAML 字面量；空 value 表示只写 key:）。
func (it *exportItem) line(k, v string) {
	prefix := "    "
	if it.first {
		prefix = "  - "
		it.first = false
	}
	if v == "" {
		it.b.WriteString(prefix + k + ":\n")
		return
	}
	it.b.WriteString(prefix + k + ": " + v + "\n")
}

func (it *exportItem) raw(k, v string) { it.line(k, v) }
func (it *exportItem) str(k, v string) { it.line(k, yamlString(v)) }
func (it *exportItem) sub(k, v string) { it.b.WriteString("      " + k + ": " + v + "\n") }
func (it *exportItem) comment(text string) {
	// 注释总是跟在已写出的行后面，故不参与 "- " 首行逻辑
	it.b.WriteString("    # " + text + "\n")
}

func (it *exportItem) boolIfTrue(k string, v bool) {
	if v {
		it.line(k, boolLiteral(true))
	}
}

func (it *exportItem) list(k string, vals []string) {
	if len(vals) == 0 {
		it.line(k, "[]")
		return
	}
	quoted := make([]string, 0, len(vals))
	for _, v := range vals {
		quoted = append(quoted, yamlString(v))
	}
	it.line(k, "["+strings.Join(quoted, ", ")+"]")
}

// secret 写密码字段：不含密码时写空串（= 导入侧沿用原密码）。
func (it *exportItem) metricCollection(settings *model.MetricCollection) {
	if settings != nil {
		// JSON flow mappings are valid YAML and round-trip through the source importer.
		collection, _ := json.Marshal(settings)
		it.raw("metricCollection", string(collection))
	}
}

func (it *exportItem) secret(k, stored string, includeSecrets bool) {
	it.line(k, yamlString(exportSecretValue(stored, includeSecrets)))
}

// tail 写各类型共有的收尾字段。
func (it *exportItem) tail(purposes []string, enabled bool) {
	it.list("purposes", purposes)
	it.line("enabled", boolLiteral(enabled))
}

func (it *exportItem) String() string { return it.b.String() }

// exportSecretValue 决定密码字段导出成什么：不带密码导出恒为空串；
// 带密码导出时解密，解不开（换过 ENV_ALERT_CRYPTO_KEY）也返回空串——
// 与其导出一段解不开的密文让用户以为备份成功，不如空着，导入时会沿用/提示补填。
func exportSecretValue(stored string, includeSecrets bool) string {
	if !includeSecrets || stored == "" {
		return ""
	}
	plain, err := crypto.Decrypt(stored)
	if err != nil {
		return ""
	}
	return plain
}

// stripMongoURICredentials 去掉 mongodb://user:pass@host 里的凭据段，
// 返回处理后的 URI 与「原本是否带凭据」。
func stripMongoURICredentials(uri string) (string, bool) {
	i := strings.Index(uri, "://")
	if i < 0 {
		return uri, false
	}
	rest := uri[i+3:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return uri, false
	}
	return uri[:i+3] + rest[at+1:], true
}

// yamlString 输出双引号 YAML 字符串。YAML 的双引号标量沿用 JSON 转义规则，
// 因此 strconv.Quote 的结果直接就是合法 YAML，且天然规避了 ":" "#" "*"
// 这些在裸标量里会改变语义的字符。
func yamlString(s string) string { return strconv.Quote(s) }

func boolLiteral(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
