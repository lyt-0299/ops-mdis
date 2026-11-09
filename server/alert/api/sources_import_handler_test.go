package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// TestImportTemplateIsSelfConsistent 保证内置模板自己能被导入器解析并通过校验。
// 模板是用户拿到的第一份样例，它要是自己都导不进去，用户第一次导入就会失败。
// 只做解析 + 校验，不触发 probe 闭包（那会真的去连网络）。
func TestImportTemplateIsSelfConsistent(t *testing.T) {
	crypto.SetTestKey()

	var payload importPayload
	if err := yaml.Unmarshal([]byte(importTemplateYAML), &payload); err != nil {
		t.Fatalf("模板不是合法 YAML: %v", err)
	}
	if len(payload.Sources) == 0 {
		t.Fatal("模板里一条 sources 都没有")
	}

	seenKinds := map[string]bool{}
	for i, item := range payload.Sources {
		kind := asString(item["kind"])
		name := asString(item["name"])
		if kind == "" || name == "" {
			t.Fatalf("第 %d 条缺 kind 或 name: kind=%q name=%q", i, kind, name)
		}
		// 自定义页面不是被监控数据源，走自己的校验（导入侧同样是单独分流的）
		if kind == kindCustomPage {
			req := customPageReq{
				Name: name,
				URL:  asString(item["url"]),
				Mode: asString(item["mode"]),
			}
			if _, _, _, _, errKey := req.normalize(); errKey != "" {
				t.Errorf("第 %d 条(自定义页面 %s)校验未通过: %s", i, name, errKey)
			}
			seenKinds[kind] = true
			continue
		}
		if !model.IsValidSourceKind(kind) {
			t.Fatalf("第 %d 条 kind=%q 不是合法数据源类型", i, kind)
		}
		blob, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("第 %d 条转 JSON 失败: %v", i, err)
		}
		_, errKey, detail, probe := buildImportDoc(kind, blob)
		if errKey != "" {
			t.Errorf("第 %d 条(kind=%s name=%s)校验未通过: %s %s", i, kind, name, errKey, detail)
			continue
		}
		if probe == nil {
			t.Errorf("第 %d 条(kind=%s)没有连接探测闭包——导入即验证是本功能的核心", i, kind)
		}
		seenKinds[kind] = true
	}

	// 模板要覆盖用户实际会用到的类型，缺一种就会有人不知道该怎么写。
	for _, want := range []string{
		model.KindMySQLInstance, model.KindMongoInstance, model.KindRedisInstance,
		model.KindKafkaInstance, model.KindESInstance, model.KindHostInstance,
		kindCustomPage,
	} {
		if !seenKinds[want] {
			t.Errorf("模板缺少 %s 的示例", want)
		}
	}
}

// TestBuildImportDocRejectsBadInput 覆盖几类真实踩过的错误输入。
func TestBuildImportDocRejectsBadInput(t *testing.T) {
	crypto.SetTestKey()

	cases := []struct {
		desc   string
		kind   string
		body   string
		wantKV string
	}{
		{
			// 这正是 1.4.x ENV 播种踩的坑：host 和 port 各自是逗号串，
			// 拼出 "h1,h2,h3:3306,3307,3308" 一个假地址。导入器必须挡住。
			desc:   "MySQL 单机模式填多个节点",
			kind:   model.KindMySQLInstance,
			body:   `{"name":"m","architecture":"standalone","nodes":["10.0.0.1:3306","10.0.0.2:3306"],"username":"u","password":"p","database":"mysql"}`,
			wantKV: "alert_",
		},
		{
			desc:   "MySQL 缺连接凭据",
			kind:   model.KindMySQLInstance,
			body:   `{"name":"m","architecture":"standalone","nodes":["10.0.0.1:3306"]}`,
			wantKV: "alert_mysql_conn_fields_required",
		},
		{
			desc:   "Mongo 缺 URI",
			kind:   model.KindMongoInstance,
			body:   `{"name":"m"}`,
			wantKV: "alert_mongo_uri_required",
		},
		{
			desc:   "Kafka 没有 broker",
			kind:   model.KindKafkaInstance,
			body:   `{"name":"k","brokers":[]}`,
			wantKV: "alert_kafka_brokers_required",
		},
		{
			desc:   "Redis 端口非法",
			kind:   model.KindRedisInstance,
			body:   `{"name":"r","host":"10.0.0.1","port":0}`,
			wantKV: "alert_invalid_port",
		},
		{
			desc:   "主机缺地址",
			kind:   model.KindHostInstance,
			body:   `{"name":"h"}`,
			wantKV: "alert_host_address_required",
		},
		{
			desc:   "不支持的类型",
			kind:   "somethingElse",
			body:   `{"name":"x"}`,
			wantKV: "alert_import_kind_unsupported",
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			_, errKey, _, _ := buildImportDoc(c.kind, []byte(c.body))
			if errKey == "" {
				t.Fatalf("应当被拒绝，却通过了校验")
			}
			if !strings.HasPrefix(errKey, c.wantKV) && errKey != c.wantKV {
				t.Fatalf("错误键 = %q，期望 %q", errKey, c.wantKV)
			}
		})
	}
}

// TestImportNeverAcceptsBuiltinFlag 导入文件不能把自己标成 builtin：
// builtin 在 UI 上没有编辑按钮，用户一旦造出来就改不掉也删不掉
// （1.4.x 的 builtin-diagnose-mongo 就是这么把 MongoDB 地址锁死的）。
func TestImportNeverAcceptsBuiltinFlag(t *testing.T) {
	crypto.SetTestKey()

	body := `{"name":"m","uri":"mongodb://10.0.0.1:27017","builtin":true}`
	doc, errKey, _, _ := buildImportDoc(model.KindMongoInstance, []byte(body))
	if errKey != "" {
		t.Fatalf("不该校验失败: %s", errKey)
	}
	inst, ok := doc.(model.MongoInstance)
	if !ok {
		t.Fatalf("返回类型不是 MongoInstance: %T", doc)
	}
	if inst.Builtin {
		t.Fatal("导入的数据源被标成了 builtin —— 用户将无法编辑/删除它")
	}
}

// TestImportDefaultsAreUsable 缺省值必须让数据源「导进去就能用」：
// enabled 缺省为 true、purposes 缺省含 metrics。二者任一为空都会导致
// 「导入成功、列表里看得到、却永远不采集」——比报错更难排查。
func TestImportDefaultsAreUsable(t *testing.T) {
	crypto.SetTestKey()

	body := `{"name":"r","host":"10.0.0.1","port":6379}`
	doc, errKey, _, _ := buildImportDoc(model.KindRedisInstance, []byte(body))
	if errKey != "" {
		t.Fatalf("不该校验失败: %s", errKey)
	}
	inst := doc.(model.RedisInstance)
	if !inst.Enabled {
		t.Error("enabled 缺省应为 true")
	}
	hasMetrics := false
	for _, p := range inst.Purposes {
		if p == "metrics" {
			hasMetrics = true
		}
	}
	if !hasMetrics {
		t.Errorf("purposes 缺省应含 metrics，实际 %v", inst.Purposes)
	}
	if inst.Kind != model.KindRedisInstance {
		t.Errorf("kind 未落库: %q", inst.Kind)
	}
	if inst.ID == "" {
		t.Error("ID 未生成")
	}
}
