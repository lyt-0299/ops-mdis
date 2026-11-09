package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// historyColl 慢查询历史落库集合，由 main.go 经 SlowQueryMongoConfig 注入
// (指向 MDIS 自有的 ops-mongo)。common 包不 import alert 包，保持解耦。
//
// 用 atomic 而非裸变量：1.5.8 起数据源增删改会触发热重建(reloadSlowQueryTargets →
// InitSlowQueryMongo)在独立 goroutine 里重写它，而采集 ticker 与 /api/slow-query/list
// 的请求协程同时在 slowOpsColl() 里读，构成数据竞争(-race 可稳定复现)。
var historyColl atomic.Pointer[mongo.Collection]

// socketTimeoutMs 同上：热重建会重写，采集与查询路径并发读。取值恒等(同一个 env
// 解析结果)故无实际后果，但仍是 -race 会报的竞争，一并收敛。
var socketTimeoutMs atomic.Int64

// slowOpsColl 返回慢查询历史集合句柄。
//
// 历史落在 MDIS 自有的 ops-mongo(ENV_OPS_MONGODB_URI)上，不再写被监控目标库：
// 被监控库对 MDIS 只应是只读观测对象，往客户生产库建索引/写历史既违背"只读"承诺，
// 又有可靠性悖论(目标 mongo 挂掉时，最需要的诊断历史也跟着挂)。
// 读 system.profile 的采集侧仍连被监控目标(clients["default"])，只读，不变。
func slowOpsColl() (*mongo.Collection, error) {
	coll := historyColl.Load()
	if coll == nil {
		return nil, errors.New("慢查询历史集合未注入(ops-mongo 不可用)")
	}
	return coll, nil
}

// controlInfo 结构体用于返回控件和工作表信息
type controlInfo struct {
	ControlName   string    `json:"controlName"`
	ControlID     string    `json:"controlID"`
	ControlType   int       `json:"controlType"`
	WorksheetName string    `json:"worksheetName"`
	WorksheetID   string    `json:"worksheetID"`
	GroupName     string    `json:"groupName"`
	GroupID       string    `json:"groupID"`
	AppName       string    `json:"appName"`
	AppID         string    `json:"appID"`
	ProjectID     string    `json:"projectID"`
	ProcessList   []Process `json:"processList"`
}

type ProfileEntry struct {
	Ns           string `bson:"ns"`
	Query        bson.M `bson:"query"`
	Command      bson.M `bson:"command"`
	DocsExamined int    `bson:"docsExamined"`
	Op           string `bson:"op"`
	PlanSummary  string `bson:"planSummary"`
	OpCommand    bson.M `bson:"opCommand"`
}

type Result struct {
	DbTag        string `json:"dbTag"`
	Ns           string `json:"ns"`
	DocsExamined int    `json:"docsExamined"`
	OpCommand    bson.M `json:"opCommand,omitempty"`
	Op           string `json:"op,omitempty"`
	PlanSummary  string `json:"planSummary,omitempty"`
	Count        int64  `json:"count"`
	GroupKey     string `json:"groupKey,omitempty"`
}

type FlowNode struct {
	NodeId  string `bson:"_id"`
	Type    int    `bson:"type"`
	Name    string `bson:"name"`
	Filters bson.A `bson:"filters" json:"filters,omitempty"`
}

type Process struct {
	ProcessID   string     `json:"processId"`
	ProcessName string     `json:"processName"`
	FlowNodes   []FlowNode `json:"flowNodes"`
}

// GlobalArgsStruct 结构体定义
type GlobalArgsStruct struct {
	Limit             int64                    `json:"limit"`
	DocsExamined      int64                    `json:"docsExamined"`
	Count             int64                    `json:"count"`
	CaptureIntervalMS int64                    `json:"captureIntervalMS"`
	ProfileSlowMS     int64                    `json:"captureSlowMS"`
	LogExpireMS       int64                    `json:"logExpireMS"`
	ProfileStatus     map[string]ProfileDetail `json:"profileStatus"`
	// DefaultSourceName 基础数据源(MD_WSServiceDefault 对应的那台)的真实数据源名，
	// 供前端把「默认」实例显示成真名(如 mongodb_1)。env 模式无名时为空，前端回退「默认」。
	DefaultSourceName string `json:"defaultSourceName"`
}

// ProfileDetail 用于存储 profile 状态和慢查询时间
type ProfileDetail struct {
	Status string `json:"status"` // profile 状态
	SlowMS int64  `json:"slowms"` // 慢查询时间
}

// GlobalArgs 全局变量
var GlobalArgs = GlobalArgsStruct{
	Limit:             2000,                           // 从 system.profile 表拿数据的 limit 参数
	DocsExamined:      100000,                         // 扫描文档数量超过时捕获
	Count:             50,                             // 归组超过此数量时记录数据库
	CaptureIntervalMS: 10 * 60 * 1000,                 // 定时捕获间隔单位毫秒
	ProfileSlowMS:     100,                            // 设置system.profile的慢查询捕获条件
	LogExpireMS:       7 * 24 * 60 * 60 * 1000,        // slowops表数据保留时间单位毫秒
	ProfileStatus:     make(map[string]ProfileDetail), // 初始化 profile 状态映射
}

// MongoClientInfo 结构体定义
type MongoClientInfo struct {
	Client *mongo.Client
	DBName string
	Type   string
}

// clients / metaClients 由 InitSlowQueryMongo 整体替换。1.5.8 起该函数可在运行中
// 被重复调用(数据源页改动 MongoDB 实例即热重建)，而各 handler 正在并发读这两个 map，
// 故一律经 clientsMu 保护——无锁替换会触发 fatal error: concurrent map read and map write。
var (
	clientsMu sync.RWMutex
	clients   map[string]*MongoClientInfo
)

// retireClients 关闭上一轮里已不再被新连接集合引用的 *mongo.Client。
//
// 延迟关闭而非立即断开：发布新 map 的那一刻，可能还有请求握着旧句柄在查询
// (snapshotClients 取到的是指针)，立刻 Disconnect 会让那些查询报错。等一个宽限期
// 再断，比让用户在改数据源的瞬间看到一次莫名其妙的查询失败要好。
// 首次初始化时 old 为空，此函数直接返回，不产生任何协程。
func retireClients(oldClients, oldMeta, newClients, newMeta map[string]*MongoClientInfo) {
	inUse := make(map[*mongo.Client]bool)
	for _, m := range []map[string]*MongoClientInfo{newClients, newMeta} {
		for _, c := range m {
			if c != nil && c.Client != nil {
				inUse[c.Client] = true
			}
		}
	}
	stale := make(map[*mongo.Client]bool)
	for _, m := range []map[string]*MongoClientInfo{oldClients, oldMeta} {
		for _, c := range m {
			if c != nil && c.Client != nil && !inUse[c.Client] {
				stale[c.Client] = true
			}
		}
	}
	if len(stale) == 0 {
		return
	}
	go func() {
		time.Sleep(30 * time.Second)
		for c := range stale {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.Disconnect(ctx); err != nil {
				log.Printf("[slowquery] 关闭已失效的目标库连接失败(可忽略): %v", err)
			}
			cancel()
		}
		log.Printf("[slowquery] 已回收 %d 个不再使用的目标库连接", len(stale))
	}()
}

// snapshotClients 返回当前 clients 的浅拷贝，供只读遍历使用。
// 拷贝的是指针，取到后即便发生重建，手上这批 *mongo.Client 仍可安全用完
// (旧连接由 InitSlowQueryMongo 延迟关闭，见 retireClients)。
func snapshotClients() map[string]*MongoClientInfo {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	out := make(map[string]*MongoClientInfo, len(clients))
	for k, v := range clients {
		out[k] = v
	}
	return out
}

// metaClients 承载各 HAP 基础库(工作表/工作流元数据)的 mongo 客户端，按库名索引。
// 支持「拆库」:mdworksheet/mdapps/mdworkflow 可分散在不同 mongo。由 buildMetaClients
// 在 InitSlowQueryMongo 末尾填充(手动 override 优先，否则自动探测)；未命中的库由
// metaDB 回退到 clients["default"]。
var metaClients map[string]*MongoClientInfo

// metaSignatureColl 各 HAP 基础库的标志性集合，用于自动探测某 mongo 是否承载该库。
var metaSignatureColl = map[string]string{
	"mdworksheet": "worksheet",  // 工作表/字段元数据(worksheet、wscontrols)
	"mdapps":      "apk",        // 应用/分组元数据(apk、appentity)
	"mdworkflow":  "wf_process", // 工作流元数据
}

// metaDB 返回承载指定 HAP 基础库的 *mongo.Database：优先用 metaClients(手动指定/
// 自动探测的拆库位置)，否则回退 clients["default"]。慢查询元数据 join 全程经它取库，
// 不再硬绑 clients["default"]。
func metaDB(dbName string) *mongo.Database {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	if c, ok := metaClients[dbName]; ok && c != nil && c.Client != nil {
		return c.Client.Database(dbName)
	}
	if c, ok := clients["default"]; ok && c != nil && c.Client != nil {
		return c.Client.Database(dbName)
	}
	return nil
}

// LoadFromEnv 读取环境变量覆盖默认值
func (g *GlobalArgsStruct) LoadFromEnv() {
	if val := getEnvAsInt64("ENV_GATEWAY_DOCS_EXAMINED"); val != nil {
		g.DocsExamined = *val
	}
	if val := getEnvAsInt64("ENV_GATEWAY_CAPTURE_INTERVAL_MS"); val != nil {
		g.CaptureIntervalMS = *val
	}
	if val := getEnvAsInt64("ENV_GATEWAY_PROFILE_SLOW_MS"); val != nil {
		g.ProfileSlowMS = *val
	}
	if val := getEnvAsInt64("ENV_GATEWAY_LOG_EXPIRE_MS"); val != nil {
		g.LogExpireMS = *val
	}
}

// getEnvAsInt 辅助方法：读取 int 类型环境变量
func getEnvAsInt64(key string) *int64 {
	valStr := os.Getenv(key)
	if valStr == "" {
		return nil
	}
	if valInt, err := strconv.ParseInt(valStr, 10, 64); err == nil {
		return &valInt
	}
	return nil
}

// getProfileStatus 读取目标库的 profiling 状态；关着就尝试开启。
// 返回 (状态, slowms)，状态取值 slow / all / unknown / denied。
//
// ⚠️ 开启失败必须如实返回 denied：这里曾经无论 runCommand 成功与否都 return "slow"，
// 还照打一行 "Profiling enabled successfully"。于是数据源账号没有 dbAdmin 权限时，
// 页面显示「已开启（slowms 100 ms）」，实际一条 system.profile 都不会产生 ——
// 用户只会看到「明明说开了却永远没数据」，且日志里那句 success 还会把排查带偏。
func getProfileStatus(db *mongo.Database) (string, int64) {
	var result struct {
		Was    int   `bson:"was"`    // MongoDB 4.4+ 返回字段为 "was"
		SlowMS int64 `bson:"slowms"` // 慢查询时间阈值（仅当 profiling 级别为 1 时有效）
	}

	// 使用 profile: -1 命令代替 getProfilingStatus
	err := db.RunCommand(context.TODO(), bson.D{{Key: "profile", Value: -1}}).Decode(&result)
	if err != nil {
		log.Printf("[slowquery] 读取 %s 的 profiling 状态失败(账号可能缺 dbAdmin/clusterMonitor 权限): %v", db.Name(), err)
		return "unknown", 0
	}

	// enable 尝试把 profiling 开到 level 1，失败按 denied 返回，绝不谎报成功。
	enable := func() (string, int64) {
		if err := db.RunCommand(context.TODO(),
			bson.D{{Key: "profile", Value: 1}, {Key: "slowms", Value: GlobalArgs.ProfileSlowMS}}).Err(); err != nil {
			log.Printf("[slowquery] 为 %s 开启 profiling 失败(账号需要 dbAdmin 权限，或到目标库手动执行 db.setProfilingLevel(1, %d)): %v",
				db.Name(), GlobalArgs.ProfileSlowMS, err)
			return "denied", 0
		}
		log.Printf("[slowquery] 已为 %s 开启 profiling，slowms=%d", db.Name(), GlobalArgs.ProfileSlowMS)
		return "slow", GlobalArgs.ProfileSlowMS
	}

	// 根据 "was" 字段的值判断 profiling 状态，并处理慢查询时间
	switch result.Was {
	case 0:
		return enable() // profiling 关着，尝试开启
	case 1:
		return "slow", result.SlowMS // 只记录慢查询，并返回 slowms 值
	case 2:
		return "all", 0 // 记录所有查询
	default:
		return enable() // 非 1/2 的异常级别，同样尝试拨回 1
	}
}

// globalArgsMu 保护 GlobalArgs 里会被并发写的部分（ProfileStatus 这个 map 与
// DefaultSourceName）。有四条路径会同时碰它：/api/global-args 的每个 HTTP 请求、
// ExecuteTaskHandler、10 分钟一次的采集 ticker，以及 1.5.8 起数据源增删改触发的
// 热重建（reloadSlowQueryTargets → InitSlowQueryMongo）。任意两条撞上就是
// fatal error: concurrent map writes —— 它不可 recover，整个 ops-server 连同
// 网关一起退出（实测：在数据源页删一条 MongoDB 源即可触发，48881 随之全挂）。
var globalArgsMu sync.RWMutex

// setProfileStatus 整体替换 profile 状态表。
// 整体替换而非逐 key 写入：数据源被删掉后，它的状态随之消失，不会一直留在接口里。
func setProfileStatus(next map[string]ProfileDetail) {
	globalArgsMu.Lock()
	defer globalArgsMu.Unlock()
	GlobalArgs.ProfileStatus = next
}

// setDefaultSourceName 见 InitSlowQueryMongo：热重建时会改它，而 HTTP 侧正在序列化。
func setDefaultSourceName(name string) {
	globalArgsMu.Lock()
	defer globalArgsMu.Unlock()
	GlobalArgs.DefaultSourceName = name
}

// snapshotGlobalArgs 取一份可脱离锁安全使用的副本（ProfileStatus 深拷贝）。
// 序列化与 executeTask 都走它，避免持锁做 IO。
func snapshotGlobalArgs() GlobalArgsStruct {
	globalArgsMu.RLock()
	defer globalArgsMu.RUnlock()
	snap := GlobalArgs
	snap.ProfileStatus = make(map[string]ProfileDetail, len(GlobalArgs.ProfileStatus))
	for k, v := range GlobalArgs.ProfileStatus {
		snap.ProfileStatus[k] = v
	}
	return snap
}

// 更新 GlobalArgsStruct 中的 profile 状态
func updateGlobalArgs() {
	// getProfileStatus 要跟目标库通信（连不上时会等到超时，实测数秒），全程持锁会把
	// 所有 HTTP 请求一起堵住，所以先在锁外收进局部 map，最后一次性替换。
	next := make(map[string]ProfileDetail)
	for key, clientInfo := range snapshotClients() {
		// 仅处理 Type 为 mdwsrows 的客户端
		if clientInfo.Type != "mdwsrows" {
			continue
		}

		db := clientInfo.Client.Database(clientInfo.DBName)

		// 获取 profile 状态和慢查询时间
		profileStatus, slowMS := getProfileStatus(db)

		if profileStatus == "unknown" {
			log.Printf("Failed to get profile status for service %s", key)
		}

		next[key] = ProfileDetail{
			Status: profileStatus,
			SlowMS: slowMS,
		}
	}
	setProfileStatus(next)
}

// GetGlobalArgsHandler 处理 HTTP 请求
func GetGlobalArgsHandler(w http.ResponseWriter, r *http.Request) {
	if !hasSlowQueryClients() {
		writeJSONError(w, http.StatusServiceUnavailable, "slowquery_target_unconfigured")
		return
	}
	updateGlobalArgs()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshotGlobalArgs()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}


// SlowQueryMongoConfig 是慢查询/索引功能连接目标 MongoDB 的配置。
// 三个字段分别对应原来的 ENV_MONGODB_URI / ENV_MONGODB_URI_WSROWS /
// ENV_MONGODB_WSROWS_EXTEND_URI；现由调用方(main.go)从数据源注册中心或
// env 取得后注入，common 包不再自行读这些 env、也不再 import alert 包。
type SlowQueryMongoConfig struct {
	URI         string // 默认连接(原 ENV_MONGODB_URI)，库默认 mdwsrows
	WsrowsURI   string // mdwsrows 库连接(原 ENV_MONGODB_URI_WSROWS)
	ExtendedURI string // JSON map: service→URI(原 ENV_MONGODB_WSROWS_EXTEND_URI)
	// MetaOverrides 手动指定 HAP 基础库(工作表/工作流元数据)各自所在的 mongo，
	// key ∈ {mdworksheet, mdapps, mdworkflow}，value = mongo URI。
	// 留空则自动探测(在已连接源中找承载该库的那台)。支持「拆库」——三个库分散在
	// 不同 mongo 时分别指定。
	MetaOverrides map[string]string
	// DefaultSourceName 基础数据源(valid[0])的真实数据源名，供前端「默认」实例显示真名。
	DefaultSourceName string
	// HistoryColl 慢查询历史的落库集合，指向 MDIS 自有的 ops-mongo(而非被监控目标)。
	// 由 main.go 注入，common 包因此不必 import alert 包。为 nil 时慢查询历史不可用
	// (采集与查询接口都会报错返回，但不影响其余功能)。
	HistoryColl *mongo.Collection
}

func init() {
	// 仅做与 mongo 连接无关的初始化；目标 MongoDB 改由 main.go 在启动后
	// 调用 InitSlowQueryMongo 懒连接，连不上不致命。
	GlobalArgs.LoadFromEnv()

	socketTimeoutMs.Store(int64(resolveTimeoutMs("SOCKET_TIMEOUT_MS")))
}

// resolveTimeoutMs 读取超时 env，缺省/非法时回退 5000ms。
func resolveTimeoutMs(key string) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v == 0 {
		return 5000
	}
	return v
}

// InitSlowQueryMongo 构建慢查询/索引功能使用的目标 MongoDB 客户端集合。
// 由 main.go 在 alert 子系统初始化之后调用：优先用数据源注册中心配置，
// 回退 env。所有失败均返回 error / 打印警告，绝不 log.Fatal，保证 gateway
// 其余功能(前端/告警)不受目标库不可达影响。三个字段均空时不建任何 client，
// 仅打印提示并返回 nil(慢查询功能暂不可用)。
//
// mongo-driver v2 的 mongo.Connect 是懒连接(不立即拨号)，此处不做 Ping，
// 实际连接错误延迟到首个慢查询请求时由 handler 返回，而非启动崩溃。
func InitSlowQueryMongo(cfg SlowQueryMongoConfig) error {
	mongoURI := cfg.URI
	mongodbWsrowsURI := cfg.WsrowsURI
	extendedURI := cfg.ExtendedURI
	mongoOptions := os.Getenv("ENV_MONGODB_OPTIONS")

	// 基础数据源真名，供前端把「默认」实例显示成真实数据源名。
	// 走 setter：热重建(数据源增删改)会重入这里，而 HTTP 侧可能正在序列化 GlobalArgs。
	setDefaultSourceName(cfg.DefaultSourceName)

	// 慢查询历史的落库集合(ops-mongo)，由调用方注入。
	historyColl.Store(cfg.HistoryColl)

	// 三者皆空：未配置目标库，慢查询功能暂不可用，但不影响启动。
	// 走与 :548 同构的加锁发布——早前这里是裸写 clients，有三个问题：
	//   ① 不加锁：热重建在独立 goroutine 里跑，与持 RLock 的读者(snapshotClients /
	//      getClient / metaDB)构成数据竞争，-race 可稳定复现；
	//   ② 只换 clients 不换 metaClients：删掉最后一条诊断源后，metaDB 仍会返回已被
	//      移除数据源的库句柄；
	//   ③ 不回收旧连接：那批 *mongo.Client 再没人 Disconnect，每删一轮泄漏一批。
	if mongoURI == "" && mongodbWsrowsURI == "" && extendedURI == "" {
		clientsMu.Lock()
		oldClients, oldMeta := clients, metaClients
		clients, metaClients = make(map[string]*MongoClientInfo), nil
		clientsMu.Unlock()
		retireClients(oldClients, oldMeta, nil, nil)
		log.Printf("[slowquery] 慢查询目标 MongoDB 未配置：请在「数据源」页添加 MongoDB 数据源并勾选「诊断」用途")
		return nil
	}

	connectTimeoutMs := resolveTimeoutMs("CONNECT_TIMEOUT_MS")
	serverSelectionTimeoutMs := resolveTimeoutMs("SERVER_SELECTION_TIMEOUT_MS")
	socketTimeoutMs.Store(int64(resolveTimeoutMs("SOCKET_TIMEOUT_MS")))

	buildOptions := func(uri string) *options.ClientOptions {
		return options.Client().ApplyURI(uri).
			SetConnectTimeout(time.Duration(connectTimeoutMs) * time.Millisecond).
			SetServerSelectionTimeout(time.Duration(serverSelectionTimeoutMs) * time.Millisecond).
			SetTimeout(time.Duration(socketTimeoutMs.Load()) * time.Millisecond).
			// 嵌套文档统一解码为 bson.M：慢查询解析器(extractFromQuery/分组逻辑)全程
			// 断言 OpCommand["filter"] 等嵌套文档为 bson.M。mongo-driver v2 默认把 bson.M
			// 内的嵌套文档解码为 bson.D，会导致 system.profile 解析全部失败(报 "Invalid
			// filter format in OpCommand")。设置 DefaultDocumentM 恢复 v1 的解码行为。
			//
			// ObjectIDAsHexString：工作表/分组/工作流元数据查询(getWorksheetNameAndID 等)
			// 把 mongo 的 ObjectId _id 解码进 Go 的 string 字段。mongo-driver v2 默认禁止
			// ObjectID→string 解码(报 "decoding an object ID into a string is not
			// supported")，导致 getWorksheetNameAndID/getMatchingFlowNodes 报错、
			// worksheetID 被清空并连锁使 应用/工作表/字段路径/关联工作流 全部解析失败
			// (前端显示 undefined-undefined)。开启此项按 hex 字符串解码，恢复 v1 行为。
			SetBSONOptions(&options.BSONOptions{DefaultDocumentM: true, ObjectIDAsHexString: true})
	}

	newClients := make(map[string]*MongoClientInfo)

	// default 客户端：慢查询历史(mdservicedata.slowops)、工作表元数据等读写均走它。
	var defaultClient *mongo.Client
	if mongoURI != "" {
		validURI, err := ensureValidMongoURI(mongoURI)
		if err != nil {
			return fmt.Errorf("default URI 非法: %w", err)
		}
		if mongoOptions != "" {
			validURI += mongoOptions
		}
		defaultClient, err = mongo.Connect(buildOptions(validURI))
		if err != nil {
			return fmt.Errorf("连接 default MongoDB 失败: %w", err)
		}
		newClients["default"] = &MongoClientInfo{
			Client: defaultClient,
			DBName: "mdwsrows",
		}
	} else {
		log.Printf("[slowquery] default 连接(「数据源」页的 diagnose 源)未配置，依赖它的慢查询历史/工作表元数据接口将不可用")
	}

	// 创建 MD_WSServiceDefault 客户端
	if mongodbWsrowsURI != "" {
		parsedURI, err := url.Parse(mongodbWsrowsURI)
		if err != nil {
			log.Printf("[slowquery] 解析 WsrowsURI 失败，跳过 MD_WSServiceDefault: %v", err)
		} else {
			dbName := strings.TrimPrefix(parsedURI.Path, "/")
			if dbName == "" {
				log.Printf("[slowquery] WsrowsURI 未带库名，跳过 MD_WSServiceDefault: %s", mongodbWsrowsURI)
			} else if client, err := mongo.Connect(buildOptions(mongodbWsrowsURI)); err != nil {
				log.Printf("[slowquery] 连接 MD_WSServiceDefault 失败，跳过: %v", err)
			} else {
				log.Printf("[slowquery] 已连接 MD_WSServiceDefault")
				newClients["MD_WSServiceDefault"] = &MongoClientInfo{
					Client: client,
					DBName: dbName,
					Type:   "mdwsrows",
				}
			}
		}
	} else if defaultClient != nil {
		// 未单独配置 wsrows 时复用 default(保持原行为)。
		newClients["MD_WSServiceDefault"] = &MongoClientInfo{
			Client: defaultClient,
			DBName: "mdwsrows",
			Type:   "mdwsrows",
		}
	}

	// 扩展 MongoDB 实例连接
	if extendedURI != "" {
		var extendedMap map[string]string
		if err := json.Unmarshal([]byte(extendedURI), &extendedMap); err != nil {
			log.Printf("[slowquery] 解析 ExtendedURI 失败，跳过扩展实例: %v", err)
		} else {
			for service, uri := range extendedMap {
				parsedURI, err := url.Parse(uri)
				if err != nil {
					log.Printf("[slowquery] 解析扩展实例 %s 的 URI 失败，跳过: %v", service, err)
					continue
				}
				dbName := strings.TrimPrefix(parsedURI.Path, "/")
				if dbName == "" {
					log.Printf("[slowquery] 扩展实例 %s 的 URI 未带库名，跳过: %s", service, uri)
					continue
				}
				client, err := mongo.Connect(buildOptions(uri))
				if err != nil {
					log.Printf("[slowquery] 连接扩展实例 %s 失败，跳过: %v", service, err)
					continue
				}
				log.Printf("[slowquery] 已连接扩展实例 %s", service)
				newClients[service] = &MongoClientInfo{
					Client: client,
					DBName: dbName,
					Type:   "mdwsrows",
				}
			}
		}
	}

	// 解析 HAP 基础库(工作表/工作流元数据)各自所在的 mongo：手动 override 优先连接，
	// 否则在本轮新建的源中自动探测。支持拆库(三库分散在不同 mongo)。
	// 在发布前做：探测要用新连接，且两个 map 必须同时生效。
	newMetaClients := buildMetaClients(cfg.MetaOverrides, buildOptions, newClients)

	// 发布：一把写锁内同时换掉 clients 与 metaClients，并回收上一轮的连接。
	clientsMu.Lock()
	oldClients, oldMeta := clients, metaClients
	clients, metaClients = newClients, newMetaClients
	clientsMu.Unlock()
	retireClients(oldClients, oldMeta, newClients, newMetaClients)

	// 慢查询历史落 MDIS 自有的 ops-mongo，索引建立与历史清理都不依赖被监控目标，
	// 故先于 default 判空执行(没有 diagnose 源时，历史查询接口依然要能用)。
	if err := createTextIndexIfNotExists(); err != nil {
		log.Printf("[slowquery] 创建文本索引失败(慢查询历史检索可能受影响): %v", err)
	}
	if err := createExpireIndexIfNotExists(); err != nil {
		log.Printf("[slowquery] 创建过期索引失败: %v", err)
	}
	if err := createSlowQueryGroupKeyIndexIfNotExists(); err != nil {
		log.Printf("[slowquery] 创建 groupKey 索引失败: %v", err)
	}

	// 清理 dbtag 已失效(数据源改名/移除)的历史，使「来源」下拉与当前数据源对齐。
	pruneStaleSlowOps()

	// default 是被监控目标(system.profile 的读取源)。没有它就无慢查询可采集，
	// 不启动定时采集；但已落在 ops-mongo 的历史仍可查询。
	if defaultClient == nil {
		log.Printf("[slowquery] 无 default 客户端(未配 diagnose 源)，不启动慢查询采集；已有历史仍可查询")
		return nil
	}

	startScheduledTask()
	return nil
}

// pruneStaleSlowOps 删除 slowops 中 dbtag 已不属于任何当前活跃慢查询源的历史记录。
// 背景:dbtag 存的是慢查询源的键(注册中心模式下 = 数据源名)。数据源改名后旧名记录
// 残留，使「来源」下拉出现已不存在的旧名(数据源 3 个、下拉却 4 条)。启动重建 clients
// 后清理一次，下拉只反映当前活跃源；以后改名重启即自愈。
func pruneStaleSlowOps() {
	// 当前活跃慢查询源(会产生慢查询历史的客户端，Type=mdwsrows)的键集合。
	live := bson.A{}
	for key, c := range snapshotClients() {
		if c != nil && c.Type == "mdwsrows" {
			live = append(live, key)
		}
	}
	if len(live) == 0 {
		return // 无活跃源，避免把历史全删
	}
	coll, err := slowOpsColl()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	res, err := coll.DeleteMany(ctx, bson.M{"slowQuery.dbtag": bson.M{"$nin": live}})
	if err != nil {
		log.Printf("[slowquery] 清理失效来源历史失败: %v", err)
		return
	}
	if res.DeletedCount > 0 {
		log.Printf("[slowquery] 已清理 %d 条失效来源(数据源改名/移除)的慢查询历史，「来源」下拉与当前数据源对齐", res.DeletedCount)
	}
}

// buildMetaClients 解析各 HAP 基础库(mdworksheet/mdapps/mdworkflow)的承载 mongo，
// 返回结果供调用方发布到 metaClients。overrides[db] 指定了 URI 则连它(支持拆库到独立
// mongo，相同 URI 去重复用一个连接)；否则在 pool(本轮新建的连接)中按标志集合自动探测；
// 都没有则不设(metaDB 运行时回退 clients["default"])。
//
// 返回而非直接赋值全局：调用方要在同一把写锁内一起发布 clients 与 metaClients，
// 避免热重建的中途被请求读到「新 clients + 旧 metaClients」的错配组合。
func buildMetaClients(overrides map[string]string, buildOpts func(string) *options.ClientOptions,
	pool map[string]*MongoClientInfo) map[string]*MongoClientInfo {
	out := make(map[string]*MongoClientInfo)
	overrideConns := make(map[string]*MongoClientInfo) // URI → client，override 去重

	for db, sigColl := range metaSignatureColl {
		// 1) 手动 override 优先
		if uri := strings.TrimSpace(overrides[db]); uri != "" {
			mc, ok := overrideConns[uri]
			if !ok {
				if client, err := mongo.Connect(buildOpts(uri)); err != nil {
					log.Printf("[slowquery] 基础库 %s 手动指定连接失败(%v)，转自动探测", db, err)
				} else {
					mc = &MongoClientInfo{Client: client, DBName: db}
					overrideConns[uri] = mc
				}
			}
			if mc != nil {
				out[db] = mc
				log.Printf("[slowquery] 基础库 %s = 手动指定承载源", db)
				continue
			}
		}
		// 2) 自动探测：在本轮新建的源中找承载该库的那台
		if mc := detectMetaHost(db, sigColl, pool); mc != nil {
			out[db] = mc
			log.Printf("[slowquery] 基础库 %s = 自动探测到承载源", db)
			continue
		}
		log.Printf("[slowquery] 基础库 %s 未找到承载源，回退默认翻译源 clients[\"default\"]", db)
	}
	return out
}

// detectMetaHost 在给定的连接池中找出承载指定基础库的那台：
// 该库存在标志集合(sigColl)即认定承载，返回首个命中。
//
// pool 由调用方传入(而非直接读全局 clients)：探测发生在 InitSlowQueryMongo 内、
// 新连接尚未发布到全局的阶段，读全局会拿到上一轮的旧连接。
func detectMetaHost(db, sigColl string, pool map[string]*MongoClientInfo) *MongoClientInfo {
	for _, c := range pool {
		if c == nil || c.Client == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		names, err := c.Client.Database(db).ListCollectionNames(ctx, bson.M{"name": sigColl})
		cancel()
		if err == nil && len(names) > 0 {
			return c
		}
	}
	return nil
}

// hasSlowQueryClients 报告慢查询目标 MongoDB 是否已初始化且至少有一个客户端。
func hasSlowQueryClients() bool {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	return len(clients) > 0
}

// getClient 安全取用 clients 中的客户端，缺失时返回 error 而非 panic。
// 慢查询目标未配置(clients 为空)或指定 dbTag 不存在时由它兜底。
func getClient(dbTag string) (*MongoClientInfo, error) {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	if len(clients) == 0 {
		return nil, fmt.Errorf("慢查询目标 MongoDB 未配置，慢查询功能暂不可用")
	}
	c, ok := clients[dbTag]
	if !ok || c == nil || c.Client == nil {
		return nil, fmt.Errorf("数据源 %q 不存在或未连接", dbTag)
	}
	return c, nil
}

// writeJSONError 以 JSON 形式返回错误: {key, detail}。
//   key    英文 snake 机器键,前端按 key 映射成文案(如 "slowquery_target_unconfigured");⚠️ 只发英文 key,不发中文。
//   detail 可选底层兜底文本(通常 err.Error())。
func writeJSONError(w http.ResponseWriter, status int, key string, detail ...string) {
	msg := ""
	if len(detail) > 0 {
		msg = detail[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"key": key, "detail": msg})
}

// 确保 MongoDB URI 格式合法
func ensureValidMongoURI(uri string) (string, error) {
	// 检查协议部分
	if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
		return "", fmt.Errorf("invalid MongoDB URI: missing required protocol (mongodb:// or mongodb+srv://) in %s", uri)
	}

	// 解析 URI
	parsedURI, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse MongoDB URI: %w", err)
	}

	// 如果路径为空，补全为 "/"
	if parsedURI.Path == "" {
		uri += "/"
	}

	return uri, nil
}

// 创建文本索引，如果不存在则创建
func createTextIndexIfNotExists() error {
	collection, err := slowOpsColl()
	if err != nil {
		return err
	}

	// 定义文本索引模型
	textIndexModel := mongo.IndexModel{
		Keys: bson.D{
			{"slowQuery.ns", "text"},
			{"slowQuery.opcommand.sort.utime", "text"},
			{"slowQuery.opcommand.find", "text"},
			{"slowQuery.op", "text"},
			{"slowQuery.plansummary", "text"},
			{"controlInfo.controlname", "text"},
			{"controlInfo.controlid", "text"},
			{"controlInfo.worksheetname", "text"},
			{"controlInfo.worksheetid", "text"},
			{"controlInfo.groupname", "text"},
			{"controlInfo.groupid", "text"},
			{"controlInfo.appname", "text"},
			{"controlInfo.appid", "text"},
			{"controlInfo.projectid", "text"},
			{"controlInfo.processlist.processid", "text"},
			{"controlInfo.processlist.processname", "text"},
			{"controlInfo.processlist.flownodes._id", "text"},
			{"controlInfo.processlist.flownodes.type", "text"},
			{"controlInfo.processlist.flownodes.name", "text"},
		},
		Options: options.Index().SetName("all_fields_text_index"),
	}

	// 检查文本索引是否存在
	indexes, err := collection.Indexes().List(context.TODO())
	if err != nil {
		return err
	}

	indexExists := false
	var existingTextIndex bson.M

	for indexes.Next(context.TODO()) {
		var index bson.M
		if err := indexes.Decode(&index); err == nil {
			if index["name"] == "all_fields_text_index" {
				indexExists = true
				existingTextIndex = index
				break
			}
		}
	}

	// 创建或更新文本索引
	if indexExists {
		if isTextIndex(existingTextIndex) {
			// 获取现有索引的权重字段
			existingWeights := existingTextIndex["weights"].(bson.M)

			// 提取新的索引权重字段
			newWeights := extractWeights(textIndexModel.Keys.(bson.D))

			if !compareBsonM(existingWeights, newWeights) {
				err = collection.Indexes().DropOne(context.TODO(), existingTextIndex["name"].(string))
				if err != nil {
					return err
				}
				log.Println("索引 'all_fields_text_index' 被删除，准备创建新索引")
			} else {
				log.Println("索引 'all_fields_text_index' 已存在，字段一致")
				return nil
			}
		} else {
			// 如果不是全文索引，直接删除重建
			err = collection.Indexes().DropOne(context.TODO(), existingTextIndex["name"].(string))
			if err != nil {
				return err
			}
			log.Println("索引 'all_fields_text_index' 被删除，准备创建新索引")
		}
	}

	// 创建文本索引
	_, err = collection.Indexes().CreateOne(context.TODO(), textIndexModel)
	if err != nil {
		return err
	}
	log.Println("索引 'all_fields_text_index' 创建成功")

	return nil
}

// createExpireIndexIfNotExists 创建过期索引，如果不存在则创建
func createExpireIndexIfNotExists() error {
	collection, err := slowOpsColl()
	if err != nil {
		return err
	}

	// 定义过期索引模型
	expireIndexModel := mongo.IndexModel{
		Keys: bson.D{
			{"timestamp", 1}, // 将 _id 替换为 timestamp
		},
		Options: options.Index().SetName("expire_index").SetExpireAfterSeconds(604800), // 604800 秒 = 7 天
	}

	// 检查过期索引是否存在
	indexes, err := collection.Indexes().List(context.TODO())
	if err != nil {
		return err
	}

	var existingExpireIndex bson.M

	for indexes.Next(context.TODO()) {
		var index bson.M
		if err := indexes.Decode(&index); err == nil {
			if index["name"] == "expire_index" {
				existingExpireIndex = index
				break
			}
		}
	}

	// 检查并创建过期索引
	if existingExpireIndex != nil {
		// 如果过期索引存在，检查过期时间是否一致
		if expireSeconds, ok := existingExpireIndex["expireAfterSeconds"].(int32); ok {
			expireMS := int64(expireSeconds * 1000)
			if expireMS != GlobalArgs.LogExpireMS {
				err = collection.Indexes().DropOne(context.TODO(), existingExpireIndex["name"].(string))
				if err != nil {
					return err
				}
				log.Println("索引 'expire_index' 被删除，准备创建新索引")
			} else {
				log.Println("索引 'expire_index' 已存在，过期时间一致")
				return nil
			}
		}
	}

	// 创建过期索引
	_, err = collection.Indexes().CreateOne(context.TODO(), expireIndexModel)
	if err != nil {
		return err
	}
	log.Println("索引 'expire_index' 创建成功")

	return nil
}

// createSlowQueryGroupKeyIndexIfNotExists 创建 slowQuery.groupkey 索引，如果不存在则创建
func createSlowQueryGroupKeyIndexIfNotExists() error {
	collection, err := slowOpsColl()
	if err != nil {
		return err
	}

	// 定义 slowQuery.groupkey 索引模型
	slowQueryGroupKeyIndexModel := mongo.IndexModel{
		Keys: bson.D{
			{"slowQuery.groupkey", 1}, // 创建 slowQuery.groupkey 索引
		},
		Options: options.Index().SetName("slowQuery_groupkey_index"), // 设置索引名称
	}

	// 检查 slowQuery.groupkey 索引是否存在
	indexes, err := collection.Indexes().List(context.TODO())
	if err != nil {
		return err
	}

	var existingGroupKeyIndex bson.M

	for indexes.Next(context.TODO()) {
		var index bson.M
		if err := indexes.Decode(&index); err == nil {
			if index["name"] == "slowQuery_groupkey_index" {
				existingGroupKeyIndex = index
				break
			}
		}
	}

	// 检查并创建 slowQuery.groupkey 索引
	if existingGroupKeyIndex != nil {
		log.Println("索引 'slowQuery_groupkey_index' 已存在")
		return nil
	}

	// 创建 slowQuery.groupkey 索引
	_, err = collection.Indexes().CreateOne(context.TODO(), slowQueryGroupKeyIndexModel)
	if err != nil {
		return err
	}
	log.Println("索引 'slowQuery_groupkey_index' 创建成功")

	return nil
}

// isTextIndex 判断是否为全文索引
func isTextIndex(index bson.M) bool {
	key, ok := index["key"].(bson.M)
	if !ok {
		return false
	}
	_, isText := key["_fts"]
	return isText
}

// extractWeights 从 bson.D 提取权重字段
func extractWeights(keys bson.D) bson.M {
	weights := bson.M{}
	for _, elem := range keys {
		if elem.Value == "text" {
			weights[elem.Key] = 1
		}
	}
	return weights
}

// compareBsonM 比较两个 bson.M 是否相等，忽略顺序
func compareBsonM(a, b bson.M) bool {
	if len(a) != len(b) {
		return false
	}

	// 将 a 和 b 转换为排序后的切片
	toSortedSlice := func(m bson.M) [][2]string {
		var sorted [][2]string
		for key, value := range m {
			sorted = append(sorted, [2]string{key, fmt.Sprintf("%v", value)})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i][0] < sorted[j][0]
		})
		return sorted
	}

	sortedA := toSortedSlice(a)
	sortedB := toSortedSlice(b)

	// 比较排序后的切片
	return reflect.DeepEqual(sortedA, sortedB)
}

// startScheduledTask 定时任务执行函数
func startScheduledTask() {
	// 先手动执行一次任务。热重建(数据源页刚勾上「诊断」)时同样跑这一次，
	// 用户不必等满一个采集周期(默认 10 分钟)才看到慢查询。
	updateGlobalArgs()
	executeTask(snapshotGlobalArgs())

	// 定时循环只启一份。1.5.8 起 InitSlowQueryMongo 可被重复调用，若不加 Once，
	// 每改一次 MongoDB 数据源就多一个永不退出的采集协程 —— 采集频率成倍上涨、
	// 同一批慢查询被重复落库，且只能靠重启收拾。
	// 循环体每轮都经 snapshotClients 读当前连接，换了目标自然生效，无需重启定时器；
	// 采集间隔来自 env、不随数据源变，因此 ticker 也不必重建。
	scheduledTaskOnce.Do(func() {
		ticker := time.NewTicker(time.Duration(snapshotGlobalArgs().CaptureIntervalMS) * time.Millisecond)
		go func() {
			for range ticker.C {
				updateGlobalArgs()
				executeTask(snapshotGlobalArgs())
			}
		}()
	})
}

// scheduledTaskOnce 保证慢查询采集的定时循环全进程只有一份，见 startScheduledTask。
var scheduledTaskOnce sync.Once

// executeTask 执行任务的核心逻辑，提取到独立的函数
func executeTask(request GlobalArgsStruct) {
	// 执行核心逻辑
	combinedInfo, err := extractAndGetInfoLogic(request, false)
	if err != nil {
		log.Printf("Error extracting info: %v", err)
		return
	}

	// 只在真采到东西时打。空结果不打：这个函数由采集 ticker 每轮调用，而绝大多数轮次
	// 都是空的（慢查询本就稀疏），照打就是一轮一行无信息量的日志。客户环境实测把
	// ENV_GATEWAY_CAPTURE_INTERVAL_MS 调到 100ms 后，这一句每秒刷 10 行、占掉全部
	// 容器日志的 75%（5 分钟 2996 行里 2996 行是它），真正有用的 reload / profiling /
	// 错误行全被挤出日志环，出问题时反而查不到。
	if len(combinedInfo) > 0 {
		log.Printf("[slowquery] 本轮采集入库 %d 条: %+v", len(combinedInfo), combinedInfo)
	}

	// 向 MongoDB 插入日志
	err = logScheduledTask(combinedInfo)
	if err != nil {
		log.Printf("Error logging scheduled task: %v", err)
	}
}

// ExecuteTaskHandler 接口处理函数，直接执行 executeTask
func ExecuteTaskHandler(w http.ResponseWriter, r *http.Request) {
	if !hasSlowQueryClients() {
		writeJSONError(w, http.StatusServiceUnavailable, "slowquery_target_unconfigured")
		return
	}
	updateGlobalArgs()
	executeTask(snapshotGlobalArgs())

	// 创建简单的响应数据
	response := map[string]string{
		"message": "Task executed successfully.",
	}

	// 设置响应头为 JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // 设置HTTP状态码为200

	// 将响应数据编码为JSON并写入响应体
	json.NewEncoder(w).Encode(response)
}

// 向 MongoDB 写入日志
func logScheduledTask(combinedInfo []map[string]interface{}) error {
	// 如果 combinedInfo 为空数组，直接返回
	if len(combinedInfo) == 0 {
		// log.Println("No data to log, skipping insertion.")
		return nil
	}

	// 慢查询历史写 MDIS 自有的 ops-mongo(不写被监控目标库)。
	collection, err := slowOpsColl()
	if err != nil {
		return err
	}

	// 遍历 combinedInfo 数组，逐条处理
	for _, info := range combinedInfo {
		// 从 info 中获取 slowQuery 并进行类型断言
		if slowQuery, ok := info["slowQuery"].(Result); ok {
			if controlInfo, ok := info["controlInfo"].(controlInfo); ok {
				// 删除与 controlID 与 groupkey 相同的文档
				deleteResult, err := collection.DeleteMany(context.TODO(), bson.D{
					{"slowQuery.groupkey", slowQuery.GroupKey},
					{"controlInfo.controlid", controlInfo.ControlID},
				})
				if err != nil {
					log.Printf("Failed to remove existing logs with groupkey '%v' and controlID '%v': %v", slowQuery.GroupKey, controlInfo.ControlID, err)
					// 继续处理其他条目，不影响后续
				} else {
					log.Printf("Successfully removed %v logs with groupkey '%v' and controlID '%v'", deleteResult.DeletedCount, slowQuery.GroupKey, controlInfo.ControlID)
				}
			}
		} else {
			log.Printf("slowQuery is not of type Result: %v", info["slowQuery"])
		}

		// 将单条文档插入 MongoDB
		_, err := collection.InsertOne(context.TODO(), info)
		if err != nil {
			log.Printf("Failed to insert log: %v", err)
			// 继续插入其他条目，不影响后续
		}
	}

	return nil
}

// 递归清理 NaN 值，确保输入和输出均为 []map[string]interface{}
func sanitizeData(data []map[string]interface{}) []map[string]interface{} {
	for _, record := range data {
		cleanMap(record) // 递归清理每个 map
	}
	return data
}

// 递归处理 map 结构，替换 NaN
func cleanMap(m map[string]interface{}) {
	for key, value := range m {
		switch v := value.(type) {
		case float64:
			if math.IsNaN(v) {
				m[key] = nil // 替换 NaN 为 nil
			}
		case map[string]interface{}:
			cleanMap(v) // 递归处理嵌套 map
		case []interface{}:
			cleanSlice(v) // 递归处理 slice
		case bson.A: // 处理 bson.A（相当于 []interface{}）
			cleanPrimitiveA(v)
		}
	}
}

// 递归处理 slice，替换 NaN
func cleanSlice(s []interface{}) {
	for i, value := range s {
		switch v := value.(type) {
		case float64:
			if math.IsNaN(v) {
				s[i] = nil // 替换 NaN 为 nil
			}
		case map[string]interface{}:
			cleanMap(v) // 递归处理 map
		case []interface{}:
			cleanSlice(v) // 递归处理多层 slice
		case bson.A: // 处理 bson.A
			cleanPrimitiveA(v)
		}
	}
}

// 递归处理 bson.A（MongoDB BSON 数组）
func cleanPrimitiveA(a bson.A) {
	for i, value := range a {
		switch v := value.(type) {
		case float64:
			if math.IsNaN(v) {
				a[i] = nil // 替换 NaN 为 nil
			}
		case map[string]interface{}:
			cleanMap(v) // 递归处理 map
		case []interface{}:
			cleanSlice(v) // 递归处理 slice
		case bson.A:
			cleanPrimitiveA(v) // 递归处理 bson.A
		}
	}
}

// GetSlowOps 查询慢查询日志的接口，支持分页和排序
func GetSlowOps(w http.ResponseWriter, r *http.Request) {
	// 仅处理 POST 请求
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid GlobalArgs method", http.StatusMethodNotAllowed)
		return
	}

	// 定义接收的请求体结构
	var request struct {
		Page       int    `json:"page"`       // 当前页码
		PageSize   int    `json:"pageSize"`   // 每页记录数
		SortBy     string `json:"sortBy"`     // 排序字段
		SortOrder  string `json:"sortOrder"`  // 排序方式（asc 或 desc）
		SearchText string `json:"searchText"` // 搜索文本
	}

	// 解析请求体
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 验证 Page 和 PageSize 是否有效
	if request.Page <= 0 || request.PageSize <= 0 {
		http.Error(w, "Page and PageSize must be greater than 0", http.StatusBadRequest)
		return
	}

	// 设置默认值
	if request.SortBy == "" {
		request.SortBy = "_id" // 默认排序字段为 _id
	}
	if request.SortOrder == "" {
		request.SortOrder = "desc" // 默认排序方式为 desc
	} else if request.SortOrder != "asc" && request.SortOrder != "desc" {
		http.Error(w, "SortOrder must be 'asc' or 'desc'", http.StatusBadRequest)
		return
	}

	// 调用从 MongoDB 查询的函数，传入 searchText
	ops, err := fetchSlowOpsFromDB(request.Page, request.PageSize, request.SortBy, request.SortOrder, request.SearchText)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching slow ops: %v", err), http.StatusInternalServerError)
		return
	}

	// 获取集合的总记录数，传入 SearchText
	totalCount, err := getTotalCountFromDB(request.SearchText)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching total count: %v", err), http.StatusInternalServerError)
		return
	}

	// 构造响应数据，确保 Data 为 [] 而不是 null
	response := struct {
		Count int64                    `json:"count"` // 记录总数
		Data  []map[string]interface{} `json:"data"`  // 存储 ops 的内容
	}{
		Count: totalCount,
		Data:  sanitizeData(ops), // ops 为空时将自动返回 [] 而非 null
	}

	// 如果 ops 为空，确保 Data 字段是空数组
	if ops == nil {
		response.Data = []map[string]interface{}{}
	}

	// 返回查询结果作为 JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// 从 MongoDB 中查询慢查询日志，支持分页和排序
func fetchSlowOpsFromDB(page, pageSize int, sortBy, sortOrder, searchText string) ([]map[string]interface{}, error) {
	// 慢查询历史读自 MDIS 自有的 ops-mongo。
	collection, err := slowOpsColl()
	if err != nil {
		return nil, err
	}

	// 构造查询条件，支持文本搜索
	filter := bson.M{}
	if searchText != "" {
		filter["$text"] = bson.M{"$search": searchText} // 添加 $text 查询
	}

	// 计算跳过的记录数
	skip := (page - 1) * pageSize

	// 构造排序
	order := 1 // 默认升序
	if sortOrder == "desc" {
		order = -1 // 如果是降序，设置为 -1
	}
	sortOption := bson.D{{Key: sortBy, Value: order}} // 排序字段和排序方式

	// 查询并限制结果数
	cursor, err := collection.Find(context.TODO(), filter, options.Find().SetLimit(int64(pageSize)).SetSkip(int64(skip)).SetSort(sortOption))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch slow ops: %v", err)
	}
	defer cursor.Close(context.TODO())

	// 解析查询结果
	var slowOps []map[string]interface{}
	for cursor.Next(context.TODO()) {
		var slowOp map[string]interface{}
		if err := cursor.Decode(&slowOp); err != nil {
			return nil, fmt.Errorf("failed to decode slow op: %v", err)
		}
		slowOps = append(slowOps, slowOp)
	}

	// 检查是否遇到迭代器错误
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %v", err)
	}

	return slowOps, nil
}

// 获取整个表的总记录数，支持搜索条件
func getTotalCountFromDB(searchText string) (int64, error) {
	// 慢查询历史读自 MDIS 自有的 ops-mongo。
	collection, err := slowOpsColl()
	if err != nil {
		return 0, err
	}

	// 构造查询条件，支持文本搜索
	filter := bson.M{}
	if searchText != "" {
		filter["$text"] = bson.M{"$search": searchText} // 添加 $text 查询
	}

	// 获取满足条件的文档数量
	count, err := collection.CountDocuments(context.TODO(), filter)
	if err != nil {
		return 0, fmt.Errorf("failed to count documents: %v", err)
	}
	return count, nil
}

func ProfilingStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid GlobalArgs method", http.StatusMethodNotAllowed)
		return
	}

	// 从请求体中解析dbname参数
	var requestBody struct {
		DbTag  string `json:"dbTag"`
		DbName string `json:"dbname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "Invalid GlobalArgs body", http.StatusBadRequest)
		return
	}

	if requestBody.DbTag == "" {
		requestBody.DbTag = "default"
	}

	if requestBody.DbName == "" {
		http.Error(w, "dbname parameter is required", http.StatusBadRequest)
		return
	}

	clientInfo, err := getClient(requestBody.DbTag)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "common_internal_error", err.Error())
		return
	}
	db := clientInfo.Client.Database(requestBody.DbName)
	var result bson.M
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	if err := db.RunCommand(ctx, bson.D{{"profile", -1}}).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	jsonOutput, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonOutput)
}

func fetchSlowQueries(limit int64, docsExamined int64, count int64, includeAll bool) ([]Result, error) {
	var allResults []Result

	// 遍历 clients 中的每个客户端
	for dbTag, client := range snapshotClients() {
		// 只处理 Type 为 mdwsrows 的客户端
		if client.Type != "mdwsrows" {
			continue
		}

		db := client.Client.Database(client.DBName)
		collection := db.Collection("system.profile")

		// 定义查询条件
		filter := bson.M{
			"docsExamined": bson.M{"$gte": docsExamined},
		}

		// 如果 includeAll 为 false，添加 ts 时间过滤条件
		if !includeAll {
			filter["ts"] = bson.M{"$gte": time.Now().Add(-120 * time.Minute)}
		}

		// 设置查询选项，限制返回条数
		findOptions := options.Find().SetLimit(limit)

		// 查询并处理结果
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
		defer cancel()

		cursor, err := collection.Find(ctx, filter, findOptions)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)

		// 分组结果
		profileResults := make(map[string]*Result)
		for cursor.Next(ctx) {
			var entry ProfileEntry
			if err := cursor.Decode(&entry); err != nil {
				return nil, err
			}

			// 设置 OpCommand 字段
			if entry.Query != nil {
				entry.OpCommand = entry.Query
			} else if entry.Command != nil {
				entry.OpCommand = entry.Command
			}

			var opCommandValue string
			if entry.Op == "query" {
				if filterVal, ok := entry.OpCommand["filter"]; ok {
					if filterMap, ok := filterVal.(bson.M); ok {
						processedFilter := removeValues(filterMap)
						opCommandValue = fmt.Sprintf("%v", processedFilter)
					}
				}
			} else if entry.Op == "command" {
				if aggregateVal, ok := entry.OpCommand["aggregate"]; ok {
					opCommandValue = fmt.Sprintf("%v", aggregateVal)
				}
			}

			key := entry.Ns + entry.Op + entry.PlanSummary + opCommandValue

			if _, exists := profileResults[key]; !exists {
				profileResults[key] = &Result{
					Ns:           entry.Ns,
					DocsExamined: entry.DocsExamined,
					OpCommand:    entry.OpCommand,
					Op:           entry.Op,
					PlanSummary:  entry.PlanSummary,
					Count:        0,
					DbTag:        dbTag,
					GroupKey:     key,
				}
			}
			profileResults[key].Count++
		}

		for _, result := range profileResults {
			if result.Count >= count {
				allResults = append(allResults, *result)
			}
		}
	}

	return allResults, nil
}

func removeValues(filter bson.M) bson.M {
	// 创建一个新的 bson.M 用于存储处理后的结果
	processedFilter := bson.M{}

	// MongoDB 基本操作符集合
	mongoOperators := map[string]bool{
		"$or":  true,
		"$and": true,
		"$nin": true,
		"$not": true,
		"$in":  true,
		"$eq":  true,
		"$ne":  true,
		"$lt":  true,
		"$lte": true,
		"$gt":  true,
		"$gte": true,
	}

	// 遍历 filter 中的每个字段
	for key, value := range filter {
		switch v := value.(type) {
		case bson.M: // 如果值是一个嵌套的 bson.M，则递归调用
			processedFilter[key] = removeValues(v)
		case bson.A: // 如果值是一个 bson.A（数组），处理数组中的每个元素
			processedArray := make(bson.A, len(v))
			for i, elem := range v {
				if elemMap, ok := elem.(bson.M); ok { // 如果元素是 bson.M，则递归处理
					processedArray[i] = removeValues(elemMap)
				} else {
					processedArray[i] = "..." // 非 bson.M 的元素替换为省略号
				}
			}
			processedFilter[key] = processedArray
		default:
			// 如果 key 是操作符，则保留操作符，但替换值为 "..."
			if mongoOperators[key] {
				processedFilter[key] = "..."
			} else {
				processedFilter[key] = "..." // 非操作符的字段值替换为 "..."
			}
		}
	}

	return processedFilter
}

// fetchControlAndWorksheetInfo 处理逻辑部分，获取控件和工作表信息
func fetchControlAndWorksheetInfo(worksheetID, originControlID string) (controlInfo, error) {
	var matchingNodes []Process

	if worksheetID == "" {
		return controlInfo{}, fmt.Errorf("worksheetID不能为空")
	}

	controlName, controlType, err := getControlNameAndID(originControlID)
	if err != nil {
		log.Println("getControlNameAndID: ", err)
	}

	worksheetName, worksheetID, projectID, err := getWorksheetNameAndID(worksheetID)
	if err != nil {
		log.Println("getWorksheetNameAndID: ", err)
	}

	groupName, groupID, err := getGroupIDAndNameByWorksheetID(worksheetID)
	if err != nil {
		log.Println("getGroupIDAndNameByWorksheetID: ", err)
	}

	appName, appID, err := getAppNameAndIDByGroupID(groupID)
	if err != nil {
		log.Println("getAppNameAndIDByGroupID: ", err)
	}

	if originControlID == "" {
		log.Printf("controlID为空")
	} else if len(originControlID) < 22 {
		switch originControlID {
		//case "status":
		//	controlName = "[工作表系统字段]-标记行是否删除"
		//	log.Printf("controlID为行状态: originControlID='%s'", originControlID)
		case "rowid":
			controlName = "[系统字段-行记录ID]"
			log.Printf("controlID为行记录ID: originControlID='%s'", originControlID)
		default:
			log.Printf("controlID无法检索流程: originControlID='%s'", originControlID)
		}
	} else {
		matchingNodes, err = getMatchingFlowNodes(worksheetID, originControlID, false)
		if err != nil {
			log.Println("getMatchingFlowNodes: ", err)
		}
	}

	response := controlInfo{
		ControlName:   controlName,
		ControlID:     originControlID,
		ControlType:   controlType,
		WorksheetName: worksheetName,
		WorksheetID:   worksheetID,
		GroupName:     groupName,
		GroupID:       groupID,
		AppName:       appName,
		AppID:         appID,
		ProjectID:     projectID,
		ProcessList:   matchingNodes,
	}

	return response, nil
}

func extractAndGetInfoLogic(globalArgs GlobalArgsStruct, includeAll bool) ([]map[string]interface{}, error) {
	slowQueryResults, err := fetchSlowQueries(globalArgs.Limit, globalArgs.DocsExamined, globalArgs.Count, includeAll)
	if err != nil {
		return nil, err
	}

	var combinedInfo []map[string]interface{}
	if len(slowQueryResults) == 0 {
		return combinedInfo, nil
	}

	for _, result := range slowQueryResults {
		worksheetID, err := getWorksheetID(result)
		if err != nil {
			return nil, err
		}

		controlIDs, err := extractControlID(result)
		if err != nil {
			return nil, err
		}

		// 如果 controlIDs 为空，也需要写入
		if len(controlIDs) == 0 {
			combinedInfo = append(combinedInfo, map[string]interface{}{
				"timestamp":   time.Now().UTC(),
				"slowQuery":   result,
				"controlInfo": nil,
			})
			continue
		}

		for _, controlID := range controlIDs {
			// 跳过值为 "status" 的 controlID
			if controlID == "status" {
				continue
			}

			info, err := fetchControlAndWorksheetInfo(worksheetID, controlID)
			if err != nil {
				return nil, err
			}

			combinedInfo = append(combinedInfo, map[string]interface{}{
				"timestamp":   time.Now().UTC(),
				"slowQuery":   result,
				"controlInfo": info,
			})
		}
	}

	return combinedInfo, nil
}

func getWorksheetID(result Result) (string, error) {
	nsParts := strings.Split(result.Ns, ".")
	if len(nsParts) > 1 {
		return strings.TrimPrefix(nsParts[1], "ws"), nil
	}
	return "", fmt.Errorf("Invalid Ns format in Result")
}

func extractControlID(result Result) ([]string, error) {
	const minLength = 4

	if result.Op == "query" {
		return extractFromQuery(result.OpCommand["filter"], minLength)
	} else if result.Op == "command" {
		if pipeline, ok := result.OpCommand["pipeline"].(bson.A); ok && len(pipeline) > 0 {
			if firstMap, ok := pipeline[0].(bson.M); ok {
				if match, exists := firstMap["$match"]; exists {
					return extractFromMatch(match, minLength)
				}
			}
		}
		return nil, fmt.Errorf("Invalid pipeline format in OpCommand")
	}

	return nil, fmt.Errorf("Unsupported operation type: %s", result.Op)
}

func extractFromQuery(filter interface{}, minLength int) ([]string, error) {
	var controlIDs []string

	// 处理 filter 是否为 bson.M 类型
	if filterMap, valid := filter.(bson.M); valid {
		for key, value := range filterMap {
			// 如果是 $or 或 $and 关键字，递归处理嵌套条件
			if key == "$or" || key == "$and" {
				if conditions, ok := value.(bson.A); ok {
					for _, condition := range conditions {
						if conditionMap, valid := condition.(bson.M); valid {
							// 递归调用处理嵌套
							nestedControlIDs, err := extractFromQuery(conditionMap, minLength)
							if err != nil {
								return nil, err
							}
							controlIDs = append(controlIDs, nestedControlIDs...)
						} else {
							return nil, fmt.Errorf("Invalid format for $or/$and conditions")
						}
					}
				} else {
					return nil, fmt.Errorf("$or/$and conditions are empty or invalid")
				}
			} else if len(key) > minLength {
				// 如果字段名的长度大于 minLength，加入结果集
				controlIDs = append(controlIDs, key)
			}
		}
	} else {
		return nil, fmt.Errorf("Invalid filter format in OpCommand")
	}

	return controlIDs, nil
}

func extractFromMatch(match interface{}, minLength int) ([]string, error) {
	var controlIDs []string

	if matchMap, ok := match.(bson.M); ok {
		for key, value := range matchMap {
			if key == "$or" || key == "$and" {
				if conditions, ok := value.(bson.A); ok {
					for _, condition := range conditions {
						if conditionMap, valid := condition.(bson.M); valid {
							controlIDs = append(controlIDs, extractKeys(conditionMap, minLength)...)
						} else {
							return nil, fmt.Errorf("Invalid format for $or/$and conditions in $match")
						}
					}
				} else {
					return nil, fmt.Errorf("$or/$and conditions in $match are empty or invalid")
				}
			} else if len(key) > minLength {
				controlIDs = append(controlIDs, key)
			}
		}
	} else {
		return nil, fmt.Errorf("Invalid format for $match in OpCommand")
	}

	return controlIDs, nil
}

func extractKeys(conditionMap bson.M, minLength int) []string {
	var keys []string
	for nestedKey := range conditionMap {
		if len(nestedKey) > minLength {
			keys = append(keys, nestedKey)
		}
	}
	return keys
}

func getControlNameAndID(cid string) (string, int, error) {
	db := metaDB("mdworksheet")
	if db == nil {
		return "", 0, fmt.Errorf("mdworksheet 元数据源未就绪")
	}
	collection := db.Collection("wscontrols")

	filter := bson.M{"cid": cid}

	var result struct {
		//ID   string `bson:"_id"`
		Name string `bson:"name"`
		Type int    `bson:"type"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	err := collection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", 0, fmt.Errorf("no document found")
		}
		return "", 0, err
	}

	return result.Name, result.Type, nil
}

func getWorksheetNameAndID(id string) (string, string, string, error) {
	db := metaDB("mdworksheet")
	if db == nil {
		return "", "", "", fmt.Errorf("mdworksheet 元数据源未就绪")
	}
	collection := db.Collection("worksheet")

	objectId, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return "", "", "", err
	}

	filter := bson.M{"_id": objectId}

	var result struct {
		ID        string `bson:"_id"`
		Name      string `bson:"name"`
		Projectid string `bson:"projectid"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	err = collection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", "", "", fmt.Errorf("no document found")
		}
		return "", "", "", err
	}

	return result.Name, result.ID, result.Projectid, nil
}

func getGroupIDAndNameByWorksheetID(wsid string) (string, string, error) {
	db := metaDB("mdapps")
	if db == nil {
		return "", "", fmt.Errorf("mdapps 元数据源未就绪")
	}
	collection := db.Collection("appentity")

	filter := bson.M{"cfgs.wsid": wsid}

	var result struct {
		ID     string `bson:"_id"`
		Name   string `bson:"apName"`
		RootID string `bson:"rootId"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	err := collection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", "", fmt.Errorf("no document found")
		}
		return "", "", err
	}

	// 如果 rootId 存在且不为空，则替换 ID
	if result.RootID != "" {
		result.ID = result.RootID
	}

	return result.Name, result.ID, nil
}

func getAppNameAndIDByGroupID(groupID string) (string, string, error) {
	db := metaDB("mdapps")
	if db == nil {
		return "", "", fmt.Errorf("mdapps 元数据源未就绪")
	}
	collection := db.Collection("apk")

	filter := bson.M{"apps": groupID}

	var result struct {
		ID      string `bson:"apkid"`
		ApkName string `bson:"apkName"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	err := collection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", "", fmt.Errorf("no document found")
		}
		return "", "", err
	}

	return result.ApkName, result.ID, nil
}

func getMatchingFlowNodes(worksheetID, controlID string, verbose bool) ([]Process, error) {
	db := metaDB("mdworkflow")
	if db == nil {
		return nil, fmt.Errorf("mdworkflow 元数据源未就绪")
	}
	collection := db.Collection("wf_process")

	pipeline := mongo.Pipeline{
		{{"$match", bson.M{"flowNodes.appId": worksheetID, "flowNodes.type": bson.M{"$in": []int{7, 13}}, "parentId": bson.M{"$ne": nil}}}},
		{{"$sort", bson.D{{"parentId", 1}, {"version", -1}}}},
		{{"$group", bson.M{
			"_id": bson.M{"parentId": "$parentId"},
			"doc": bson.M{"$first": "$$ROOT"},
		}}},
		{{"$replaceRoot", bson.M{"newRoot": "$doc"}}},
	}

	var matchingNodes []Process
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(socketTimeoutMs.Load())*time.Millisecond)
	defer cancel()
	cursor, err := collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var process struct {
			ID        string     `bson:"_id"`
			Name      string     `bson:"name"`
			FlowNodes []FlowNode `bson:"flowNodes"`
		}
		if err := cursor.Decode(&process); err != nil {
			return nil, err
		}

		var matchedFlowNodes []FlowNode
		for _, node := range process.FlowNodes {
			nodeText := fmt.Sprintf("%v", node)
			if strings.Contains(nodeText, controlID) {
				if !verbose {
					node.Filters = nil
				}
				matchedFlowNodes = append(matchedFlowNodes, node)
			}
		}

		if len(matchedFlowNodes) > 0 {
			matchingNodes = append(matchingNodes, Process{
				ProcessID:   process.ID,
				ProcessName: process.Name,
				FlowNodes:   matchedFlowNodes,
			})
		}
	}

	if err := cursor.Err(); err != nil {
		return nil, err
	}

	return matchingNodes, nil
}
