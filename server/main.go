package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	alertapi "sourcecode.mingdao.net/private/MDIS/server/alert/api"
	"sourcecode.mingdao.net/private/MDIS/server/alert/collname"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	alertdb "sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	alertsvc "sourcecode.mingdao.net/private/MDIS/server/alert/service"
	"sourcecode.mingdao.net/private/MDIS/server/common"
)

// 1.5.0：慢查询目标不再有 env 回退。目标 MongoDB 一律来自数据源页里
// 勾了「诊断」用途的实例——一个入口，配了就生效，没配就明确说没配。

// metaOverridesFromEnv 解析 ENV_MONGODB_META_OVERRIDES：手动指定 HAP 基础库
// (工作表/工作流元数据)各自所在 mongo。JSON map，key ∈ {mdworksheet,mdapps,mdworkflow}，
// value = mongo URI。用于「拆库」场景(三库分散在不同 mongo)；为空则全自动探测。
func metaOverridesFromEnv() map[string]string {
	raw := strings.TrimSpace(os.Getenv("ENV_MONGODB_META_OVERRIDES"))
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		log.Printf("[slowquery] ENV_MONGODB_META_OVERRIDES 解析失败，忽略(转全自动探测): %v", err)
		return nil
	}
	return m
}

// resolveSlowQueryConfig 解析慢查询目标 MongoDB 配置：
// 优先查数据源注册中心(kind=mongoInstance, enabled=true, purposes 含 "diagnose")。
// 找到 1 个 → 作为默认目标；找到多个(HAP 分片场景) → 第一个作默认、其余作为
// 扩展分片(各自扫描 mdwsrows.system.profile)。
// 1.5.0 起这是唯一来源，没有 env 回退：查不到就是没配，如实说没配。
// 由 main.go 持有(已 import alert/database 与 common)，避免 common import alert 造成环。
func resolveSlowQueryConfig() common.SlowQueryMongoConfig {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"kind":     model.KindMongoInstance,
		"enabled":  true,
		"purposes": "diagnose", // 数组字段相等匹配 = 包含该元素
	}
	cur, err := alertdb.Config().Find(ctx, filter)
	if err != nil {
		log.Printf("[slowquery] 查询 diagnose MongoDB 实例失败，慢查询暂不可用: %v", err)
		return common.SlowQueryMongoConfig{}
	}
	var insts []model.MongoInstance
	if err := cur.All(ctx, &insts); err != nil {
		log.Printf("[slowquery] 读取 diagnose MongoDB 实例失败，慢查询暂不可用: %v", err)
		return common.SlowQueryMongoConfig{}
	}

	// 按 _id 排序，保证「默认实例」的选取在多次启动间稳定。
	sort.Slice(insts, func(i, j int) bool { return insts[i].ID < insts[j].ID })

	type namedURI struct{ name, uri string }
	var valid []namedURI
	for _, inst := range insts {
		if uri := buildDiagnoseURI(inst); uri != "" {
			valid = append(valid, namedURI{inst.Name, uri})
		}
	}
	if len(valid) == 0 {
		log.Printf("[slowquery] 无可用 diagnose 源，慢查询暂不可用：请在 UI「数据源」页添加 MongoDB 数据源并勾选「诊断」用途")
		return common.SlowQueryMongoConfig{}
	}

	// 自动识别「基础数据源」:慢查询的字段/工作表/工作流翻译全靠 HAP 元数据
	// (mdworksheet/mdapps/mdworkflow),只有含这套元数据的 mongo 才能当翻译源。
	// 在 diagnose 源里挑出含 mdworksheet 的那台提到 valid[0](=cfg.URI→clients["default"]),
	// 其余(纯 mdwsrows 的分片/测试库)只作扫描分片。找不到任何含元数据的源则回退首个
	// (原「按 _id 取第一」行为),并日志告警(应用/工作表/工作流会解析为空)。
	baseIdx := -1
	for i, v := range valid {
		if alertsvc.MongoURIHasHAPMetadata(v.uri) {
			baseIdx = i
			break
		}
	}
	switch {
	case baseIdx > 0:
		valid[0], valid[baseIdx] = valid[baseIdx], valid[0]
		log.Printf("[slowquery] 自动识别基础数据源(含 mdworksheet 元数据)=%q，作为字段/工作表/工作流翻译源", valid[0].name)
	case baseIdx == 0:
		log.Printf("[slowquery] 基础数据源(含 mdworksheet 元数据)=%q", valid[0].name)
	default:
		log.Printf("[slowquery] 未在 diagnose 源中探测到 HAP 元数据(mdworksheet)，回退首个源 %q 作翻译源(应用/工作表/工作流可能解析为空)", valid[0].name)
	}

	cfg := common.SlowQueryMongoConfig{URI: valid[0].uri, MetaOverrides: metaOverridesFromEnv(), DefaultSourceName: valid[0].name}
	if len(valid) > 1 {
		// 多个 diagnose 实例 = HAP 分片：其余实例作为扩展分片，
		// 各自连接并扫描 mdwsrows.system.profile(ExtendedURI 要求 URI 带库名)。
		extMap := make(map[string]string, len(valid)-1)
		for _, v := range valid[1:] {
			extMap[v.name] = ensureMongoDB(v.uri, "mdwsrows")
		}
		if b, err := json.Marshal(extMap); err == nil {
			cfg.ExtendedURI = string(b)
		}
		log.Printf("[slowquery] 采用数据源注册中心 %d 个 diagnose 实例(默认=%q + %d 个扩展分片)",
			len(valid), valid[0].name, len(valid)-1)
	} else {
		log.Printf("[slowquery] 采用数据源注册中心的 diagnose 实例 %q 作为慢查询目标", valid[0].name)
	}
	return cfg
}

// ensureMongoDB 确保 mongo URI 的 path 段带有库名(供 ExtendedURI 解析出 DBName)。
// 已带库名则原样返回；以 '/' 结尾则补 db；无 path 则插入 '/db'。查询串(?opts)保留。
func ensureMongoDB(uri, db string) string {
	query := ""
	if i := strings.Index(uri, "?"); i >= 0 {
		query = uri[i:]
		uri = uri[:i]
	}
	schemeEnd := strings.Index(uri, "://")
	if schemeEnd < 0 {
		return uri + query // 非标准 URI，不处理
	}
	authority := uri[schemeEnd+3:]
	if slash := strings.Index(authority, "/"); slash >= 0 {
		if authority[slash+1:] == "" {
			uri += db // 以 '/' 结尾，补库名
		}
		// 否则已有库名，保持不动
	} else {
		uri += "/" + db
	}
	return uri + query
}

// buildDiagnoseURI 从 MongoInstance 还原可用的连接 URI。
// URI 内若已内嵌凭据则原样使用；否则在有独立 username/password 时
// (password 经 AES 解密)注入到 mongodb://user:pass@ 形式。
func buildDiagnoseURI(inst model.MongoInstance) string {
	uri := inst.URI
	if uri == "" {
		return ""
	}
	// URI 已内嵌凭据(含 '@')则直接用。
	if hasEmbeddedCredentials(uri) {
		return uri
	}
	if inst.Username == "" {
		return uri
	}
	pass := inst.Password
	if dec, err := crypto.Decrypt(inst.Password); err == nil {
		pass = dec
	} else {
		log.Printf("[slowquery] diagnose 实例 %q 密码解密失败，按原值使用: %v", inst.Name, err)
	}
	// 注入 scheme://user:pass@rest
	for _, scheme := range []string{"mongodb+srv://", "mongodb://"} {
		if len(uri) >= len(scheme) && uri[:len(scheme)] == scheme {
			return scheme + inst.Username + ":" + pass + "@" + uri[len(scheme):]
		}
	}
	return uri
}

// hasEmbeddedCredentials 判断 mongo URI 的 host 段之前是否已有 user:pass@。
func hasEmbeddedCredentials(uri string) bool {
	for _, scheme := range []string{"mongodb+srv://", "mongodb://"} {
		if len(uri) >= len(scheme) && uri[:len(scheme)] == scheme {
			rest := uri[len(scheme):]
			for i := 0; i < len(rest); i++ {
				switch rest[i] {
				case '@':
					return true
				case '/', '?':
					return false
				}
			}
			return false
		}
	}
	return false
}

// mountAlertSubsystem 在 ops-mongo 连上后挂载告警子系统：内置 Prometheus upsert、
// /api/alert 与 /api/sd 路由、各监控调度器。连接成功后只调一次(首启成功或后台重试成功)。
//
// 1.5.0：这里曾有四处 env 播种(中间件/k8s/flink/诊断 mongo)，全部删除。
// 被监控对象只从 UI「数据源」页来，没有第二个入口、没有一次性种子、没有漂移对账。
func mountAlertSubsystem() {
	// 内置 Prometheus 数据源单例(upsert),失败仅打日志不致命。
	// 这是唯一保留 builtin 身份的数据源——它是平台自带的基础设施，不是被监控对象。
	if err := alertdb.EnsureBuiltinPrometheus(); err != nil {
		log.Printf("[alert] 内置 Prometheus 数据源初始化失败: %v", err)
	}

	// 1.5.11 及以前的 Flink 单例 → 自定义页面(一次性，幂等)。放在挂路由前，
	// 免得升级后第一次打开「数据源」页时 Flink 那条还没出现。
	alertapi.MigrateFlinkConfig()

	alertMux := http.NewServeMux()
	alertapi.RegisterRoutes(alertMux)
	http.Handle("/api/alert/", common.AuthMiddlewareHandler(alertMux))
	log.Println("[alert] 告警子系统已挂载 /api/alert/")

	// 服务发现端点(/api/sd/*)：给 ops-agent 拉起 exporter、给 ops-prometheus http_sd 抓取。
	// 不经 atoken 中间件，自带 ?token 校验(ENV_OPS_SD_TOKEN)；与告警共享 ops-mongo 数据源。
	sdMux := http.NewServeMux()
	alertsvc.RegisterSDRoutes(sdMux)
	http.Handle("/api/sd/", sdMux)
	log.Println("[sd] 数据源服务发现已挂载 /api/sd/")

	// 告警标题由引擎统一提供，七个调度器发通知前都要取它；必须早于调度器启动，
	// 否则第一次告警就撞上 nil 引擎（GetAlertEngine 另有兜底，此处是双保险）。
	// 传空串即用默认标题，不引入新的环境变量。
	alertsvc.InitAlertEngine("")

	go alertsvc.StartMonitor(10) // kafka lag 监控，worker pool size 10
	go alertsvc.StartAlertScheduler()
	go alertsvc.StartPortMonitorScheduler()
	go alertsvc.StartSslMonitorScheduler()
	go alertsvc.StartMongoMonitorScheduler()
	go alertsvc.StartMySQLMonitorScheduler()
	go alertsvc.StartKafkaRebalanceMonitorScheduler()
	go alertsvc.StartPauseResumeScheduler()
	go alertsvc.StartAlertSilenceScheduler()
	go alertsvc.StartAlertHistoryCleanup()
	log.Println("[alert] 调度器已全部启动")
}

func main() {
	// 获取环境变量 ENV_OPS_TOKEN 的值，如果不存在则设置为默认值 "admin"
	envToken := os.Getenv("ENV_OPS_TOKEN")
	if envToken == "" {
		// 设置环境变量
		envToken = "admin"
		err := os.Setenv("ENV_OPS_TOKEN", envToken)
		if err != nil {
			log.Fatalf("Failed to set ENV_OPS_TOKEN: %v", err)
		}
	}
	log.Printf("Starting server with ENV_OPS_TOKEN: %s\n", envToken)
	logConfigSummary()

	// 认证接口
	http.HandleFunc("/api/auth", common.AuthHandler)
	http.HandleFunc("/api/logout", common.LogoutHandler)
	// 网关 nginx auth_request 子请求校验签名会话 Cookie（各 iframe 代理共用）
	http.HandleFunc("/api/session/check", common.AuthMiddleware(common.SessionCheckHandler))

	// 1.0.0 认证后可以访问系统信息和 iframe URL 接口
	http.HandleFunc("/api/iframe-urls", common.AuthMiddleware(common.GetIframeUrls)) // 添加 getIframeUrls 接口需要认证

	// 1.1.0 慢查询接口
	http.HandleFunc("/api/slow-query/profiling-status", common.AuthMiddleware(common.ProfilingStatusHandler))
	http.HandleFunc("/api/index/create", common.AuthMiddleware(common.CreateIndexHandler))
	http.HandleFunc("/api/slow-query/list", common.AuthMiddleware(common.GetSlowOps))
	http.HandleFunc("/api/slow-query/analyze", common.AuthMiddleware(common.ExecuteTaskHandler))
	http.HandleFunc("/api/global-args", common.AuthMiddleware(common.GetGlobalArgsHandler))
	http.HandleFunc("/api/log/export", common.AuthMiddleware(common.LogExportHandler))  // Loki 日志导出(服务/容器)
	http.HandleFunc("/api/log/export/", common.AuthMiddleware(common.LogExportHandler)) // 子路径形式 /api/log/export/<文件名> —— 文件名进 URL 末段，浏览器必用此名下载

	// 1.4.0 告警子系统(/api/alert/*)：连专用 ops-mongo,与被监控目标解耦。
	// 初始化做成"非致命":凭据密钥未配 / ops-mongo 连不上时,仅禁用告警子系统,
	// 不影响 gateway 其余功能(前端/慢查询/索引)正常启动。
	var alertDBReady atomic.Bool
	if err := crypto.InitKeyFromEnv(); err != nil {
		log.Printf("[alert] 凭据密钥未配置(ENV_ALERT_CRYPTO_KEY),告警子系统禁用(不重试): %v", err)
	} else if os.Getenv("ENV_OPS_MONGODB_URI") == "" {
		log.Printf("[alert] ENV_OPS_MONGODB_URI 未设置,告警子系统禁用(不重试)")
	} else if err := alertdb.Init(); err != nil {
		// ops-mongo 是告警/数据源/服务发现的存储,属必要组件。首次连不上(镜像慢拉、
		// mongo 后起)不再永久禁用,转后台退避重试(3s→30s 封顶,无限)直到连上再挂载。
		log.Printf("[alert] ops-mongo 首次连接失败,转后台重试: %v", err)
		go func() {
			backoff := 3 * time.Second
			for attempt := 2; ; attempt++ {
				time.Sleep(backoff)
				if err := alertdb.Init(); err != nil {
					log.Printf("[alert] ops-mongo 重连失败(第 %d 次, %s 后再试): %v", attempt, backoff, err)
					if backoff < 30*time.Second {
						backoff *= 2
						if backoff > 30*time.Second {
							backoff = 30 * time.Second
						}
					}
					continue
				}
				log.Printf("[alert] ops-mongo 已连上(第 %d 次),挂载告警子系统", attempt)
				mountAlertSubsystem()
				alertDBReady.Store(true)
				return
			}
		}()
	} else {
		mountAlertSubsystem()
		alertDBReady.Store(true)
	}

	// 慢查询/索引目标 MongoDB：启动后懒连接、非致命。
	// alert 数据源可用时优先读注册中心(diagnose 用途)，否则回退 env;
	// 连不上或未配置都只打日志，不影响 gateway 启动。
	// 自定义页面动态反代：网关把 ${BASE}/custom/<slug>/ 重写成 /api/custom-proxy/<slug>/
	// 打到这里，目标地址运行时从「数据源」页的自定义页面配置解析（空=未配置/已停用/直嵌模式，
	// 这三种都不该走反代）。1.5.12 前这里是写死的 Flink 单例。
	common.CustomPageResolver = func(slug string) (string, string) {
		if !alertDBReady.Load() {
			return "", ""
		}
		return alertapi.ResolveCustomPage(slug)
	}
	http.Handle("/api/custom-proxy/", common.AuthMiddlewareHandler(common.CustomProxyHandler()))

	// /api/iframe-urls 的「自定义」分组：反代模式给平台内路径(同源、带鉴权)，
	// 直嵌模式原样给目标地址(前端 iframe 直接指过去，不经平台)。
	common.CustomPagesProvider = func(pathPrefix string) []common.CustomPageEntry {
		if !alertDBReady.Load() {
			return nil
		}
		pages := alertapi.EnabledCustomPages()
		out := make([]common.CustomPageEntry, 0, len(pages))
		for _, p := range pages {
			url := p.URL
			if p.Mode != "iframe" {
				url = pathPrefix + "/custom/" + p.ID + "/"
			}
			out = append(out, common.CustomPageEntry{
				Key:  "custom/" + p.ID,
				Name: p.Name,
				URL:  url,
			})
		}
		return out
	}

	// 慢查询目标的建立/重建。抽成函数是为了让「数据源」页改动 MongoDB 实例后能原地重来一遍：
	// 早先只在启动时跑一次，于是勾上「诊断」用途后接口依然报未配置，必须重启 gateway，
	// 而 UI 上没有任何提示——客户实测就卡在这里(源已含 diagnose，慢查询页仍显示未开启)。
	setupSlowQueryTargets := func() {
		var sqCfg common.SlowQueryMongoConfig
		if alertDBReady.Load() {
			sqCfg = resolveSlowQueryConfig()
		} else {
			// 1.5.0：数据源只存在 ops-mongo 里，ops-mongo 连不上就真的没有目标可用，
			// 不再有 env 兜底。如实报，别让人以为配了却查不到数据。
			log.Printf("[slowquery] ops-mongo 不可用，读不到 diagnose 数据源，慢查询暂不可用")
		}
		// 慢查询历史落 MDIS 自有的 ops-mongo(不写被监控目标库)。common 包不 import
		// alert 包，故集合句柄在此注入；ops-mongo 不可用时留空，慢查询历史随之不可用。
		if alertDBReady.Load() {
			if db := alertdb.DB(); db != nil {
				sqCfg.HistoryColl = db.Collection(collname.SlowQueryHistory())
			}
		} else {
			log.Printf("[slowquery] ops-mongo 不可用，慢查询历史(落库/查询)将不可用")
		}
		if err := common.InitSlowQueryMongo(sqCfg); err != nil {
			log.Printf("[slowquery] 慢查询目标 MongoDB 初始化失败(慢查询/索引功能暂不可用): %v", err)
		}
	}
	setupSlowQueryTargets()
	// 交给 alert 侧：MongoDB 数据源增删改/启停后回调，勾完即生效、不用重启。
	alertapi.SetSlowQueryReloader(setupSlowQueryTargets)

	// 获取环境变量 ENV_OPS_PORT，如果不存在则使用默认值 "8081"
	envPort := os.Getenv("ENV_OPS_PORT")
	if envPort == "" {
		envPort = "8081"
	}
	port := ":" + envPort
	log.Printf("Server is running on port %s...\n", port)
	// 启动服务器并检查错误
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
