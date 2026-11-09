package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// CapResult 单个能力探测结果。
// ReasonKey 是英文机器键（文案归前端，见 CLAUDE.md 的约定）；Reason 为尚未 key 化的
// 兜底中文/底层原文，前端取值优先 ReasonKey。新增判定一律走 ReasonKey。
type CapResult struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	ReasonKey string `json:"reasonKey,omitempty"`
	Warn      string `json:"warn,omitempty"`
}

// isStandaloneErr 判断是否为单节点（非副本集）错误。
func isStandaloneErr(msg string) bool {
	return strings.Contains(msg, "not running with --replSet") ||
		strings.Contains(msg, "NoReplicationEnabled")
}

// diagnoseReasonFromErr 把 system.profile 读取错误转成用户可读原因。
// nil → 空字符串（无错误）。
func diagnoseReasonFromErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "not authorized") ||
		strings.Contains(msg, "Unauthorized") {
		return "账号无权读取 system.profile（需 clusterMonitor 或目标库读权限）"
	}
	return "无法读取 system.profile：" + msg
}

// mongoDiagnoseDB 从 URI 提取库名，缺省 "mdwsrows"。
func mongoDiagnoseDB(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return "mdwsrows"
	}
	db := strings.TrimPrefix(u.Path, "/")
	db = strings.TrimSpace(db)
	if db == "" {
		return "mdwsrows"
	}
	return db
}

// ProbeMongoCapabilities 一次性连接并探测 MongoDB 数据源各用途需要的命令权限。
// 返回 (connectErr, caps)；connectErr != nil 时 caps 为 nil。
func ProbeMongoCapabilities(inst *model.MongoInstance) (error, map[string]CapResult) {
	client, err := buildMongoClient(inst) // 已做 Connect + Ping
	if err != nil {
		return err, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	defer client.Disconnect(ctx) //nolint:errcheck

	caps := map[string]CapResult{}

	// metrics：能连上并 Ping 即可（exporter 自采）
	caps["metrics"] = CapResult{Available: true}

	// alert：需副本集，执行 replSetGetStatus
	if e := client.Database("admin").
		RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Err(); e != nil {
		if isStandaloneErr(e.Error()) {
			// MongoDB 告警只有副本集状态 / Oplog 延迟 / Oplog 窗口三项，单节点确实都不适用；
			// 但「实例挂了要收到告警」是另一回事，走「端口」监控即可，故文案里一并指路
			caps["alert"] = CapResult{Available: false, ReasonKey: "alert_mongo_standalone_no_replset"}
		} else {
			caps["alert"] = CapResult{Available: false, Reason: "无法执行 replSetGetStatus：" + e.Error()}
		}
	} else {
		caps["alert"] = CapResult{Available: true}
	}

	// diagnose：试读 <dbName>.system.profile 一条（空集合 ErrNoDocuments 不算失败）
	dbName := mongoDiagnoseDB(inst.URI)
	findOpts := options.FindOne()
	findErr := client.Database(dbName).Collection("system.profile").
		FindOne(ctx, bson.D{}, findOpts).Err()
	if findErr != nil && findErr != mongo.ErrNoDocuments {
		caps["diagnose"] = CapResult{Available: false, Reason: diagnoseReasonFromErr(findErr)}
	} else {
		caps["diagnose"] = CapResult{Available: true}
	}

	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "getLog", Value: "global"}}).Err(); err != nil {
		caps[model.MongoLogPurpose] = CapResult{ReasonKey: "alert_mongo_getlog_unavailable"}
	} else {
		caps[model.MongoLogPurpose] = CapResult{Available: true}
	}

	return nil, caps
}

// MongoURIHasHAPMetadata 报告该 URI 指向的 MongoDB 是否含 HAP「基础数据」
// (工作表/工作流元数据)，判据：mdworksheet 库存在 worksheet 集合。
// 慢查询的字段/工作表/工作流翻译依赖这套元数据，故由它自动识别「基础数据源」：
// 含元数据的 diagnose 源作翻译源，纯 mdwsrows 的测试/分片库被排除在翻译源外。
// URI 已内嵌凭据(由 buildDiagnoseURI 还原)。连接/超时/无权限一律返回 false(回退)。
func MongoURIHasHAPMetadata(uri string) bool {
	opts := options.Client().ApplyURI(uri).
		SetConnectTimeout(5 * time.Second).
		SetServerSelectionTimeout(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	client, err := mongo.Connect(opts)
	if err != nil {
		return false
	}
	defer client.Disconnect(ctx) //nolint:errcheck
	names, err := client.Database("mdworksheet").
		ListCollectionNames(ctx, bson.D{{Key: "name", Value: "worksheet"}})
	if err != nil {
		return false
	}
	return len(names) > 0
}

// ProbeKafkaCapabilities 探测 Kafka 连通性：连通则 metrics+alert 均可用。
func ProbeKafkaCapabilities(inst *model.KafkaInstance) (error, map[string]CapResult) {
	if err := TestKafkaConnection(inst); err != nil {
		return err, nil
	}
	return nil, map[string]CapResult{
		"metrics": {Available: true},
		"alert":   {Available: true},
	}
}

// ProbeMySQLCapabilities 探测 MySQL 连通性：连通则 metrics+alert 均可用。
func ProbeMySQLCapabilities(inst *model.MySQLInstance) (error, map[string]CapResult) {
	if err := TestMySQLConnection(inst); err != nil {
		return err, nil
	}
	return nil, map[string]CapResult{
		"metrics": {Available: true},
		"alert":   {Available: true},
	}
}

// ProbeRedisCapabilities 探测 Redis 连通性：连通则 metrics 可用。
func ProbeRedisCapabilities(host string, port int, pwd string, db int, tlsEnabled, skipVerify bool) (error, map[string]CapResult) {
	if err := TestRedisConnection(host, port, pwd, db, tlsEnabled, skipVerify); err != nil {
		return err, nil
	}
	return nil, map[string]CapResult{
		"metrics": {Available: true},
	}
}

// ProbeESCapabilities 探测 Elasticsearch 连通性：连通则 metrics 可用。
func ProbeESCapabilities(esURL, username, password string, skipVerify bool) (error, map[string]CapResult) {
	if err := TestESConnection(esURL, username, password, skipVerify); err != nil {
		return err, nil
	}
	return nil, map[string]CapResult{
		"metrics": {Available: true},
	}
}

// ProbeHostCapabilities 探测 node_exporter：HTTP GET /metrics，2xx 即 metrics 可用。
func ProbeHostCapabilities(address string) (error, map[string]CapResult) {
	if err := probeHostExporter(address); err != nil {
		return err, nil
	}
	return nil, map[string]CapResult{
		"metrics": {Available: true},
	}
}

// probeHostExporter HTTP GET http://<address>/metrics，5s 超时，非 2xx 或不可达即 error。
func probeHostExporter(address string) error {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — host exporter 通常无 TLS，跳过验证
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	targetURL := fmt.Sprintf("http://%s/metrics", address)
	resp, err := client.Get(targetURL)
	if err != nil {
		return fmt.Errorf("连接 host exporter %s 失败: %w", address, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("host exporter 返回非 2xx 状态码: %d", resp.StatusCode)
	}
	return nil
}

// ProbeWebUICapabilities 探测一个 Web UI 地址是否活着 —— 自定义页面(含迁移后的 Flink)
// 都走这条：GET <url>，跟随跳转，2xx/3xx 算通；401/403 同样算通 —— 目标自带登录页
// (Langfuse 这类首页直接 401)说明服务本身是好的，判成「连不上」会把人往错方向带。
//
// 外链页面不采指标，所以只回一个 ui 能力位，不返回 metrics。
//
// 1.5.12 前这里是 Flink 专属探测(GET /config 并检查返回体里有没有 flink-version)。
// 自定义页面能挂任意 Web UI，那种「像不像 Flink」的判据对别的目标只会误报，故去掉。
func ProbeWebUICapabilities(rawURL string) (error, map[string]CapResult) {
	u := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if u == "" {
		return fmt.Errorf("地址为空"), nil
	}

	// 记下最后一跳：这类服务常把自己的「对外地址」写死在重定向里（Grafana 配了
	// root_url 就会 301 到那个地址），而那个地址从平台这边未必连得上。此时报错原样
	// 说「<你填的地址> connection refused」会让人去查一个其实好好的地址——实测
	// http://ops-grafana:3000 就是这样：它 301 到 http://localhost:3000/...，
	// localhost 在 ops-server 容器里当然没有 3000。所以失败时要把跳转目标说出来。
	lastHop := ""
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — 内网服务常用自签证书
			DialContext:     (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
		// 这类 UI 常挂在反代后面，跟随跳转但别无限跳
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			lastHop = req.URL.String()
			return nil
		},
	}

	resp, err := client.Get(u)
	if err != nil {
		if lastHop != "" {
			return fmt.Errorf("连接 %s 失败: %s —— 它跳转到了 %s，而这个地址从平台这边连不上（多半是该服务配置里写死的对外地址，与平台能访问到的地址不是同一个；填能直达的地址即可）",
				u, shortErr(err), lastHop), nil
		}
		return fmt.Errorf("连接 %s 失败: %s", u, shortErr(err)), nil
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// 目标自己要求登录/鉴权 —— 服务是活的，能不能嵌进来由用户自己看
	default:
		return fmt.Errorf("地址能连通但返回 %d —— 确认填的是这个 Web UI 的首页地址", resp.StatusCode), nil
	}

	return nil, map[string]CapResult{
		"ui": {Available: true},
	}
}

func shortErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "端口不通(connection refused)"
	case strings.Contains(msg, "no such host"):
		return "主机名解析不了"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "context deadline exceeded"):
		return "连接超时"
	case strings.Contains(msg, "no route to host"):
		return "路由不可达"
	}
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		return msg[i+2:]
	}
	return msg
}

// HumanizeConnErr 把驱动/客户端吐的英文错误压成一句能直接照做的中文。
//
// 探测失败的原文是给开发看的，例如 MongoDB 驱动会吐
//
//	server selection error: context deadline exceeded, current topology:
//	{ Type: Unknown, Servers: [{ Addr: 10.50.101.4:27018, Type: Unknown }, ] }
//
// 原样丢给用户等于没说——这是「出问题时用户能自己闭环」的必修项。
// typ 用于挂类型相关的补充提示（不传也能用）。
func HumanizeConnErr(err error, typ string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "connection refused"):
		return "端口不通，或对端服务没起"
	case strings.Contains(low, "no such host"), strings.Contains(low, "name resolution"):
		return "主机名解析不了：确认地址拼写；若对方不在同一 compose 网络，要填宿主机 IP + 映射端口，不能填容器名"
	case strings.Contains(low, "i/o timeout"),
		strings.Contains(low, "context deadline exceeded"),
		strings.Contains(low, "server selection error"):
		return "连接超时：地址或端口不对，也可能被网络/防火墙拦住"
	case strings.Contains(low, "no route to host"), strings.Contains(low, "network is unreachable"):
		return "路由不可达：确认网段与安全组"
	case strings.Contains(low, "unable to authenticate"),
		strings.Contains(low, "authentication failed"),
		strings.Contains(low, "auth failed"),
		strings.Contains(low, "access denied"),
		strings.Contains(low, "invalid password"):
		if typ == "elasticsearch" {
			return "账号或密码不对：明道云私有部署自带 ES 的用户名是 md，不是 elastic"
		}
		return "账号或密码不对"
	case strings.Contains(low, "not authorized"),
		strings.Contains(low, "unauthorized"),
		strings.Contains(low, "permission denied"):
		if typ == "mongodb" {
			return "账号权限不足：采集指标需要 clusterMonitor 角色"
		}
		return "账号权限不足"
	case strings.Contains(low, "x509"), strings.Contains(low, "certificate"):
		return "TLS 证书校验不过：自签证书请勾选「跳过证书校验」"
	}
	return msg
}
