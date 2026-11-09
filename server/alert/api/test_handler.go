// Package api 实现告警子系统的 HTTP 处理器(net/http + mongo-driver，无 gin 依赖)。
package api

import (
	"net/http"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// builtinPrometheusID 内置 Prometheus 数据源的固定 _id。
const builtinPrometheusID = "builtin-prometheus"

// decryptDSSecrets 把数据源密文字段(Password/Token)解密为明文，
// 供 NewPrometheusClient 使用(库里存的是密文，客户端需要明文)。
func decryptDSSecrets(ds *model.DataSource) error {
	if ds.Password != "" {
		dec, err := crypto.Decrypt(ds.Password)
		if err != nil {
			return err
		}
		ds.Password = dec
	}
	if ds.Token != "" {
		dec, err := crypto.Decrypt(ds.Token)
		if err != nil {
			return err
		}
		ds.Token = dec
	}
	return nil
}

// testReq 连接测试请求体(各类型字段的并集,camelCase)。
type testReq struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	URL      string `json:"url"`      // prometheus / elasticsearch
	AuthType string `json:"authType"` // prometheus
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
	SkipSSL  bool   `json:"skipSSL"`
	TLS      bool   `json:"tls"` // mongodb / mysql / redis

	Brokers []string    `json:"brokers"` // kafka
	SASL    *model.SASL `json:"sasl"`    // kafka

	URI    string `json:"uri"`    // mongodb
	AuthDB string `json:"authDb"` // mongodb

	Nodes    []string `json:"nodes"`    // mysql
	Database string   `json:"database"` // mysql

	Host    string `json:"host"`    // redis
	Address string `json:"address"` // host
	Port    int    `json:"port"`    // redis / elasticsearch
	DB      int    `json:"db"`      // redis
}

// decryptOr 解密密文密码;crypto.Decrypt 对明文/空串安全返回原值。
func decryptOr(v string) string {
	dec, err := crypto.Decrypt(v)
	if err != nil {
		return v
	}
	return dec
}

// testOK 连接成功。scrape 是「Prometheus 里到底有没有这条源的指标」的核验结果
// （见 scrape_check.go）——只报「连接成功」曾被客户当场质疑没有意义：连得上
// 不等于面板上有数据，两者之间还隔着 SD 下发 / exporter / 抓取三道。
func testOK(w http.ResponseWriter, scrape map[string]any) {
	body := map[string]any{"ok": true, "key": "common_connection_success"}
	if len(scrape) > 0 {
		body["scrape"] = scrape
	}
	httputil.WriteJSON(w, http.StatusOK, body)
}

func testFail(w http.ResponseWriter, key string, detail ...string) {
	body := map[string]any{"ok": false, "key": key}
	if len(detail) > 0 && detail[0] != "" {
		body["detail"] = detail[0]
	}
	httputil.WriteJSON(w, http.StatusOK, body)
}

func testConnectFail(key string, detail ...string) map[string]any {
	body := map[string]any{"ok": false, "key": key}
	if len(detail) > 0 && detail[0] != "" {
		body["detail"] = detail[0]
	}
	return body
}

// resolveMongoInst 从请求构造 MongoInstance，如果带 id 则回填库中字段并恢复脱敏密码。
// 供 TestDataSource 和 ProbeDataSource 共用，避免重复逻辑。
// 返回 nil 表示缺少必要连接地址（调用方应返回错误）。
func resolveMongoInst(r *http.Request, req *testReq) *model.MongoInstance {
	inst := &model.MongoInstance{
		URI: req.URI, Username: req.Username, Password: req.Password,
		AuthDB: req.AuthDB, TLS: req.TLS, SkipSSL: req.SkipSSL,
	}
	if req.ID != "" {
		var stored model.MongoInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindMongoInstance}).
			Decode(&stored); err == nil {
			// URI 含脱敏占位符(编辑态展示的 URI 把内嵌密码脱敏成 ********，
			// 未改动直接回传)→ 用库里真实 URI，避免拿 "********" 当密码连接失败。
			if strings.Contains(inst.URI, model.MaskValue) {
				inst.URI = stored.URI
			}
			if inst.URI == "" {
				inst.URI = stored.URI
			}
			if inst.Username == "" {
				inst.Username = stored.Username
			}
			if inst.AuthDB == "" {
				inst.AuthDB = stored.AuthDB
			}
			if inst.Password == "" {
				inst.Password = stored.Password
			}
		}
	}
	inst.Password = decryptOr(inst.Password)
	return inst
}

// resolveKafkaInst 从请求构造 KafkaInstance，如果带 id 则回填库中字段并恢复脱敏密码。
func resolveKafkaInst(r *http.Request, req *testReq) *model.KafkaInstance {
	inst := &model.KafkaInstance{Brokers: req.Brokers, SASL: req.SASL}
	if req.ID != "" {
		var stored model.KafkaInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindKafkaInstance}).
			Decode(&stored); err == nil {
			if len(inst.Brokers) == 0 {
				inst.Brokers = stored.Brokers
			}
			if inst.SASL == nil {
				inst.SASL = stored.SASL
			} else if inst.SASL.Password == "" || inst.SASL.Password == model.MaskValue {
				if stored.SASL != nil {
					inst.SASL.Password = stored.SASL.Password
				}
			}
		}
	}
	if inst.SASL != nil {
		inst.SASL.Password = decryptOr(inst.SASL.Password)
	}
	return inst
}

// resolveMySQLInst 从请求构造 MySQLInstance，如果带 id 则回填库中字段并恢复脱敏密码。
func resolveMySQLInst(r *http.Request, req *testReq) *model.MySQLInstance {
	inst := &model.MySQLInstance{
		Nodes: req.Nodes, Username: req.Username, Password: req.Password,
		Database: req.Database, TLS: req.TLS, SkipSSL: req.SkipSSL,
	}
	if req.ID != "" {
		var stored model.MySQLInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindMySQLInstance}).
			Decode(&stored); err == nil {
			if len(inst.Nodes) == 0 {
				inst.Nodes = stored.Nodes
			}
			if inst.Username == "" {
				inst.Username = stored.Username
			}
			if inst.Database == "" {
				inst.Database = stored.Database
			}
			if inst.Password == "" {
				inst.Password = stored.Password
			}
		}
	}
	inst.Password = decryptOr(inst.Password)
	return inst
}

// resolveRedisParams 从请求提取 Redis 连接参数，如果带 id 则回填库中字段并恢复脱敏密码。
// 返回 (host, port, pwd, db, tls, skipSSL)。
func resolveRedisParams(r *http.Request, req *testReq) (host string, port int, pwd string, db int, tlsEnabled bool, skipSSL bool) {
	host, port, pwd, db = req.Host, req.Port, req.Password, req.DB
	tlsEnabled, skipSSL = req.TLS, req.SkipSSL
	if req.ID != "" {
		var stored model.RedisInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindRedisInstance}).
			Decode(&stored); err == nil {
			if host == "" {
				host = stored.Host
			}
			if port == 0 {
				port = stored.Port
			}
			if pwd == "" {
				pwd = stored.Password
			}
			if !tlsEnabled {
				tlsEnabled = stored.TLS
			}
		}
	}
	if port == 0 {
		port = 6379
	}
	pwd = decryptOr(pwd)
	return
}

// resolveESParams 从请求提取 ES 连接参数，如果带 id 则回填库中字段并恢复脱敏密码。
// 返回 (rawURL, user, pwd)。
func resolveESParams(r *http.Request, req *testReq) (rawURL, user, pwd string) {
	rawURL, user, pwd = req.URL, req.Username, req.Password
	if req.ID != "" {
		var stored model.ESInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindESInstance}).
			Decode(&stored); err == nil {
			if rawURL == "" {
				rawURL = stored.URL
			}
			if user == "" {
				user = stored.Username
			}
			if pwd == "" {
				pwd = stored.Password
			}
		}
	}
	pwd = decryptOr(pwd)
	return
}

// resolveHostAddress 从请求提取 host exporter 地址，如果带 id 则回填库中字段。
// 优先读 req.Address（前端 host case 发送的字段），兼容旧 req.Host，再回填库中值。
func resolveHostAddress(r *http.Request, req *testReq) string {
	address := req.Address
	if address == "" {
		address = req.Host
	}
	if req.ID != "" {
		var stored model.HostInstance
		if err := database.Config().
			FindOne(r.Context(), bson.M{"_id": req.ID, "kind": kindHostInstance}).
			Decode(&stored); err == nil {
			if address == "" {
				address = stored.Address
			}
		}
	}
	return address
}

// TestDataSource POST /api/alert/datasources/test
// 连接测试。Prometheus 走完整查询;其余类型走 TCP 端口可达性检测(不校验认证)。
// 成败均返回 HTTP 200，用 body 的 ok 区分。
func TestDataSource(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		testFail(w, "common_invalid_param", err.Error())
		return
	}
	typ := req.Type
	if typ == "" {
		typ = "prometheus"
	}

	switch typ {
	case "prometheus":
		ds := model.DataSource{
			ID: req.ID, URL: req.URL, Type: "prometheus",
			AuthType: req.AuthType, Username: req.Username,
			Password: req.Password, Token: req.Token, SkipSSL: req.SkipSSL,
		}
		// 带 id:回填请求未提供的字段(测内置/已存数据源时表单可能只传 id)。
		if ds.ID != "" {
			var existing model.DataSource
			if err := database.Config().
				FindOne(r.Context(), bson.M{"_id": ds.ID, "kind": kindDataSource}).
				Decode(&existing); err == nil {
				if ds.URL == "" {
					ds.URL = existing.URL
				}
				if ds.AuthType == "" {
					ds.AuthType = existing.AuthType
				}
				if ds.Username == "" {
					ds.Username = existing.Username
				}
				if ds.Password == "" {
					ds.Password = existing.Password
				}
				if ds.Token == "" {
					ds.Token = existing.Token
				}
			}
		}
		if err := decryptDSSecrets(&ds); err != nil {
			testFail(w, "common_decrypt_failed", err.Error())
			return
		}
		client := service.NewEphemeralPrometheusClient(&ds)
		if _, err := client.Query("vector(1)"); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "kafka":
		inst := resolveKafkaInst(r, &req)
		if err := service.TestKafkaConnection(inst); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "mongodb":
		inst := resolveMongoInst(r, &req)
		if inst.URI == "" {
			testFail(w, "alert_mongo_uri_required")
			return
		}
		if err := service.TestMongoConnection(inst); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "mysql":
		inst := resolveMySQLInst(r, &req)
		if err := service.TestMySQLConnection(inst); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "redis":
		host, port, pwd, db, tlsEnabled, skipSSL := resolveRedisParams(r, &req)
		if host == "" {
			testFail(w, "alert_host_required")
			return
		}
		if err := service.TestRedisConnection(host, port, pwd, db, tlsEnabled, skipSSL); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "elasticsearch":
		rawURL, user, pwd := resolveESParams(r, &req)
		if rawURL == "" {
			testFail(w, "common_url_required")
			return
		}
		if err := service.TestESConnection(rawURL, user, pwd, req.SkipSSL); err != nil {
			testFail(w, "common_connection_failed", service.HumanizeConnErr(err, typ))
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	case "host":
		address := resolveHostAddress(r, &req)
		if address == "" {
			testFail(w, "alert_host_address_required")
			return
		}
		connErr, _ := service.ProbeHostCapabilities(address)
		if connErr != nil {
			testFail(w, "common_connection_failed", connErr.Error())
			return
		}
		testOK(w, scrapeCheck(r.Context(), typ, req.ID))

	default:
		testFail(w, "alert_invalid_datasource_type", typ)
	}
}

// writeProbe 将能力探测结果序列化为结构化 JSON 响应。
// connErr != nil 时返回连通失败；否则返回 ok=true + per-purpose capabilities。
// scrape 是采集实况核验（见 scrape_check.go），连不通时也带上——「连不通」与
// 「Prometheus 里已经多久没数据了」是两条独立线索，一起给才好判断是刚坏还是一直坏。
func writeProbe(w http.ResponseWriter, connErr error, caps map[string]service.CapResult, typ string, scrape map[string]any) {
	body := map[string]any{}
	if len(scrape) > 0 {
		body["scrape"] = scrape
	}
	if connErr != nil {
		body["ok"] = false
		// 人话转换后再回前端：原文是 Go 驱动写给开发看的，直接展示等于没给原因
		body["connect"] = testConnectFail("common_connection_failed", service.HumanizeConnErr(connErr, typ))
		httputil.WriteJSON(w, http.StatusOK, body)
		return
	}
	body["ok"] = true
	body["connect"] = map[string]any{"ok": true}
	body["purposes"] = caps
	httputil.WriteJSON(w, http.StatusOK, body)
}

// ProbeDataSource POST /api/alert/datasources/probe
// 返回结构化 per-purpose 能力（连通性 + 各用途可用性）。
// 请求体同 testReq（带 id 或完整连接字段）。
// 响应示例（连通成功）：
//
//	{"ok":true,"connect":{"ok":true},"purposes":{"metrics":{"available":true},"alert":{"available":false,"reasonKey":"alert_mongo_standalone_no_replset"}}}
//
// 响应示例（连通失败）：
//
//	{"ok":false,"connect":{"ok":false,"key":"common_connection_failed","detail":"..."}}
func ProbeDataSource(w http.ResponseWriter, r *http.Request) {
	var req testReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":      false,
			"connect": testConnectFail("common_invalid_param", err.Error()),
		})
		return
	}

	switch req.Type {
	case "mongodb":
		inst := resolveMongoInst(r, &req)
		if inst.URI == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"ok":      false,
				"connect": testConnectFail("alert_mongo_uri_required"),
			})
			return
		}
		connErr, caps := service.ProbeMongoCapabilities(inst)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "kafka":
		inst := resolveKafkaInst(r, &req)
		connErr, caps := service.ProbeKafkaCapabilities(inst)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "mysql":
		inst := resolveMySQLInst(r, &req)
		connErr, caps := service.ProbeMySQLCapabilities(inst)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "redis":
		host, port, pwd, db, tlsEnabled, skipSSL := resolveRedisParams(r, &req)
		if host == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"ok":      false,
				"connect": testConnectFail("alert_host_required"),
			})
			return
		}
		connErr, caps := service.ProbeRedisCapabilities(host, port, pwd, db, tlsEnabled, skipSSL)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "elasticsearch":
		rawURL, user, pwd := resolveESParams(r, &req)
		if rawURL == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"ok":      false,
				"connect": testConnectFail("common_url_required"),
			})
			return
		}
		connErr, caps := service.ProbeESCapabilities(rawURL, user, pwd, req.SkipSSL)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "host":
		address := resolveHostAddress(r, &req)
		if address == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"ok":      false,
				"connect": testConnectFail("alert_host_address_required"),
			})
			return
		}
		connErr, caps := service.ProbeHostCapabilities(address)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	case "custom":
		// 自定义页面(外链，非 kind 数据源)：请求体一般不带 url，按 id 从配置里取当前值。
		// 1.5.7 起 Flink 有这一列，1.5.12 自定义页面继承它 —— 六类数据源都有连通状态，
		// 外链配错了在页面上完全看不出来，只有点进菜单看到空白页才知道。
		rawURL := strings.TrimSpace(req.URL)
		if rawURL == "" {
			if doc, ok := findCustomPageDoc(r.Context(), strings.TrimSpace(req.ID)); ok {
				rawURL = strings.TrimSpace(doc.URL)
			}
		}
		if rawURL == "" {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"ok":      false,
				"connect": testConnectFail("common_url_required"),
			})
			return
		}
		connErr, caps := service.ProbeWebUICapabilities(rawURL)
		writeProbe(w, connErr, caps, req.Type, scrapeCheck(r.Context(), req.Type, req.ID))

	default:
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":      false,
			"connect": testConnectFail("alert_invalid_datasource_type", req.Type),
		})
	}
}

// previewPromQLReq PromQL 预览请求体(camelCase)。
type previewPromQLReq struct {
	DataSourceID string `json:"dataSourceId"`
	PromQL       string `json:"promql"`
}

// PreviewPromQL POST /api/alert/datasources/test-query
// PromQL 预览。按 dataSourceId(默认内置 Prometheus)查数据源 → 解密 →
// NewPrometheusClient → GetVectorValues。语法/查询错误返回 HTTP 200 + ok=false。
func PreviewPromQL(w http.ResponseWriter, r *http.Request) {
	var req previewPromQLReq
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"key":    "common_invalid_param",
			"detail": err.Error(),
		})
		return
	}
	if req.PromQL == "" {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":  false,
			"key": "alert_promql_required",
		})
		return
	}

	id := req.DataSourceID
	if id == "" {
		id = builtinPrometheusID
	}

	ctx := r.Context()
	var ds model.DataSource
	err := database.Config().
		FindOne(ctx, bson.M{"_id": id, "kind": kindDataSource}).
		Decode(&ds)
	if err != nil {
		key := "alert_datasource_not_found"
		if req.DataSourceID == "" {
			key = "alert_datasource_required"
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":  false,
			"key": key,
		})
		return
	}

	if err := decryptDSSecrets(&ds); err != nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"key":    "common_decrypt_failed",
			"detail": err.Error(),
		})
		return
	}

	client := service.NewEphemeralPrometheusClient(&ds)
	values, err := client.GetVectorValues(req.PromQL)
	if err != nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"key":    "common_query_failed",
			"detail": err.Error(),
		})
		return
	}

	const maxSeries = 100
	series := make([]map[string]any, 0, len(values))
	for i, v := range values {
		if i >= maxSeries {
			break
		}
		series = append(series, map[string]any{
			"metric":   v.Metric,
			"value":    v.Value,
			"valueStr": strconv.FormatFloat(v.Value, 'f', -1, 64),
		})
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"resultType": "vector",
		"count":      len(values),
		"series":     series,
	})
}
