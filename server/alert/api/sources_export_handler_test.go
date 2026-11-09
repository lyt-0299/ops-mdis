package api

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// exportSamples 覆盖六类数据源，字段尽量刁钻（中文名、含 ":" 的值、多节点、SASL）——
// 导出是给用户当备份用的，格式错一个字符就是「备份看着有、恢复不回来」。
func exportSamples(includeSecrets bool) string {
	blocks := []string{
		renderMySQLExport(model.MySQLInstance{
			ID: "id-mysql", Name: "mysql:生产", Architecture: model.MySQLArchitectureMGR,
			Nodes:    []string{"10.0.0.1:3306", "10.0.0.2:3306", "10.0.0.3:3306"},
			Username: "monitor", Password: encForTest("p@ss:word"), Database: "mysql",
			Purposes: []string{"metrics", "alert"}, Enabled: true,
			MetricCollection: &model.MetricCollection{Collectors: []string{"tables", "tableIO"}, Scope: []string{"app"}},
		}, includeSecrets),
		renderMongoExport(model.MongoInstance{
			ID: "id-mongo", Name: "mongo-rs",
			URI:      "mongodb://root:secret@10.0.0.1:27017,10.0.0.2:27017/?replicaSet=rs0",
			Username: "monitor", Password: encForTest("mongo-pass"), AuthDB: "admin",
			Purposes: []string{"metrics", "alert", "diagnose"}, Enabled: true,
			MetricCollection: &model.MetricCollection{Collectors: []string{"collections", "indexes"}, Scope: []string{"app.events"}},
		}, includeSecrets),
		renderRedisExport(model.RedisInstance{
			ID: "id-redis", Name: "redis-1", Host: "10.0.0.1", Port: 6379,
			Password: encForTest("redis-pass"), DB: 2,
			Purposes: []string{"metrics"}, Enabled: false,
		}, includeSecrets),
		renderKafkaExport(model.KafkaInstance{
			ID: "id-kafka", Name: "kafka-prod",
			Brokers: []string{"10.0.0.1:9092", "10.0.0.2:9092"},
			SASL: &model.SASL{
				Enabled: true, Mechanism: "SCRAM-SHA-512", Username: "monitor",
				Password: encForTest("kafka-pass"), TLS: true,
			},
			Purposes: []string{"metrics", "alert"}, Enabled: true,
		}, includeSecrets),
		renderESExport(model.ESInstance{
			ID: "id-es", Name: "es-prod", URL: "http://10.0.0.1:9200",
			Username: "md", Password: encForTest("es-pass"), SkipSSL: true,
			Purposes: []string{"metrics"}, Enabled: true,
		}, includeSecrets),
		renderHostExport(model.HostInstance{
			ID: "id-host", Name: "node-01", Address: "10.0.0.10:59100",
			Purposes: []string{"metrics"}, Enabled: true,
		}),
		// 自定义页面：injectCss 带换行，要能原样导回去（写成单行双引号）
		renderCustomPageExport(customPageDoc{
			ID: "flink", Name: "Flink", URL: "http://flink:8081",
			Mode: customPageModeProxy, InjectCSS: ".sider {\n  color: black;\n}",
		}),
		renderCustomPageExport(customPageDoc{
			ID: "langfuse", Name: "Langfuse", URL: "http://langfuse:3000",
			Mode: customPageModeIframe, Enabled: boolPtrForTest(false),
		}),
	}
	return "sources:\n" + strings.Join(blocks, "")
}

func boolPtrForTest(v bool) *bool { return &v }

func encForTest(plain string) string {
	enc, err := crypto.Encrypt(plain)
	if err != nil {
		panic(err)
	}
	return enc
}

// TestExportRoundTripsThroughImport 是本功能的核心保证：导出的文件必须能被导入器
// 原样吃回去。备份的价值全在「恢复得回来」，格式偏差在导出那一刻是看不出来的，
// 只有真去导入才暴露——所以这条断言必须常驻。
func TestExportRoundTripsThroughImport(t *testing.T) {
	crypto.SetTestKey()

	for _, includeSecrets := range []bool{false, true} {
		var payload importPayload
		if err := yaml.Unmarshal([]byte(exportSamples(includeSecrets)), &payload); err != nil {
			t.Fatalf("includeSecrets=%v: 导出的不是合法 YAML: %v", includeSecrets, err)
		}
		if len(payload.Sources) != 8 {
			t.Fatalf("includeSecrets=%v: 导出条数 = %d，期望 8", includeSecrets, len(payload.Sources))
		}

		for i, item := range payload.Sources {
			kind := asString(item["kind"])
			// 自定义页面走自己的校验（导入侧也是单独分流的），不进 buildImportDoc
			if kind == model.KindMySQLInstance || kind == model.KindMongoInstance {
				collection, ok := item["metricCollection"].(map[string]any)
				if !ok || len(collection) != 2 {
					t.Fatalf("metric collection missing after export: %v", collection)
				}
			}
			if kind == kindCustomPage {
				req := customPageReq{
					Name: asString(item["name"]),
					URL:  asString(item["url"]),
					Mode: asString(item["mode"]),
				}
				if _, _, _, _, errKey := req.normalize(); errKey != "" {
					t.Errorf("includeSecrets=%v 第 %d 条(自定义页面)导不回去: %s",
						includeSecrets, i, errKey)
				}
				continue
			}
			blob, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("第 %d 条转 JSON 失败: %v", i, err)
			}
			// MySQL 缺密码时导入器会拒（用户名/密码/库三者必填），这正是「换环境
			// 恢复必须补密码」的设计——所以只在带密码导出时断言 MySQL 能过。
			if kind == model.KindMySQLInstance && !includeSecrets {
				continue
			}
			if _, errKey, detail, _ := buildImportDoc(kind, blob); errKey != "" {
				t.Errorf("includeSecrets=%v 第 %d 条(kind=%s)导不回去: %s %s",
					includeSecrets, i, kind, errKey, detail)
			}
		}
	}
}

// TestExportKeepsIdentityFields 导出必须带上 id / name / purposes / enabled：
// id 丢了会让还原后的 exporter 端口整体漂移；purposes 丢了导回去默认变成
// [metrics alert]，慢查询诊断这类用途会被悄悄摘掉。
func TestExportKeepsIdentityFields(t *testing.T) {
	crypto.SetTestKey()

	var payload importPayload
	if err := yaml.Unmarshal([]byte(exportSamples(true)), &payload); err != nil {
		t.Fatalf("导出的不是合法 YAML: %v", err)
	}

	byName := map[string]map[string]any{}
	for _, item := range payload.Sources {
		byName[asString(item["name"])] = item
	}

	mongo := byName["mongo-rs"]
	if mongo == nil {
		t.Fatal("导出里找不到 mongo-rs")
	}
	if asString(mongo["id"]) != "id-mongo" {
		t.Errorf("id 丢失: %v", mongo["id"])
	}
	purposes, _ := mongo["purposes"].([]any)
	if len(purposes) != 3 {
		t.Errorf("purposes 未原样导出: %v", mongo["purposes"])
	}

	redis := byName["redis-1"]
	if redis == nil {
		t.Fatal("导出里找不到 redis-1")
	}
	if enabled, ok := redis["enabled"].(bool); !ok || enabled {
		// 停用的数据源导回去必须还是停用的，否则一次恢复会把用户特意关掉的源全打开
		t.Errorf("enabled=false 未原样导出: %v", redis["enabled"])
	}
	if db, ok := redis["db"].(int); !ok || db != 2 {
		t.Errorf("db 未原样导出: %v (%T)", redis["db"], redis["db"])
	}
}

// TestExportWithoutSecretsLeaksNothing 不带密码导出时，文件里不能出现任何明文密码——
// 包括 MongoDB URI 内嵌的那段（打码或原样保留都算漏）。
func TestExportWithoutSecretsLeaksNothing(t *testing.T) {
	crypto.SetTestKey()

	out := exportSamples(false)
	for _, secret := range []string{"p@ss:word", "mongo-pass", "redis-pass", "kafka-pass", "es-pass", "secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("不带密码的导出里出现了明文 %q", secret)
		}
	}
	// 空串而不是占位串：导回同一套平台时，空 = 沿用原密码；占位串会把密码覆盖坏。
	if !strings.Contains(out, `password: ""`) {
		t.Error("密码字段应导出为空串（导入侧据此沿用原密码）")
	}
	if strings.Contains(out, model.MaskValue) {
		t.Errorf("导出里出现了脱敏占位符 %q —— 它会被当成真密码导回库里", model.MaskValue)
	}
}

// TestExportWithSecretsCarriesPlaintext 带密码导出必须是能直接用的明文，
// 否则「换机恢复」这个唯一使用场景不成立。
func TestExportWithSecretsCarriesPlaintext(t *testing.T) {
	crypto.SetTestKey()

	out := exportSamples(true)
	for _, secret := range []string{"p@ss:word", "mongo-pass", "kafka-pass"} {
		if !strings.Contains(out, secret) {
			t.Errorf("带密码导出里没有明文 %q", secret)
		}
	}
	// 内嵌凭据的 URI 原样保留（它本身就是连接串的一部分）
	if !strings.Contains(out, "mongodb://root:secret@") {
		t.Error("带密码导出应保留 URI 里的内嵌凭据")
	}
}

func TestStripMongoURICredentials(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantHad bool
	}{
		{"mongodb://root:secret@10.0.0.1:27017", "mongodb://10.0.0.1:27017", true},
		{"mongodb://10.0.0.1:27017", "mongodb://10.0.0.1:27017", false},
		// 密码里含 "@" 时按最后一个 "@" 切，否则会把 host 段截断
		{"mongodb://u:p@ss@10.0.0.1:27017", "mongodb://10.0.0.1:27017", true},
		{"mongodb+srv://u:p@cluster.example.com", "mongodb+srv://cluster.example.com", true},
		{"not-a-uri", "not-a-uri", false},
	}
	for _, c := range cases {
		got, had := stripMongoURICredentials(c.in)
		if got != c.want || had != c.wantHad {
			t.Errorf("stripMongoURICredentials(%q) = (%q,%v)，期望 (%q,%v)", c.in, got, had, c.want, c.wantHad)
		}
	}
}

// 自定义页面导出必须带上 id(=访问路径)、mode 与 injectCss：
// id 丢了还原后菜单链接会变；mode 丢了会退回默认的反代——而 Langfuse 这类
// 只能直嵌的页面一旦被改成反代，恢复后打开就是一片空白，且看不出哪里错了；
// injectCss 丢了则是样式悄悄没了（迁移过来的 Flink 靠它）。
func TestExportCustomPageKeepsModeAndCSS(t *testing.T) {
	crypto.SetTestKey()

	var payload importPayload
	if err := yaml.Unmarshal([]byte(exportSamples(false)), &payload); err != nil {
		t.Fatalf("导出的不是合法 YAML: %v", err)
	}
	byName := map[string]map[string]any{}
	for _, item := range payload.Sources {
		byName[asString(item["name"])] = item
	}

	flink := byName["Flink"]
	if flink == nil {
		t.Fatal("导出里找不到 Flink 那条自定义页面")
	}
	if asString(flink["id"]) != "flink" {
		t.Errorf("id(访问路径)未原样导出: %v", flink["id"])
	}
	if asString(flink["mode"]) != customPageModeProxy {
		t.Errorf("mode = %v，期望 proxy", flink["mode"])
	}
	if css := asString(flink["injectCss"]); !strings.Contains(css, "color: black") ||
		!strings.Contains(css, "\n") {
		t.Errorf("injectCss 未原样导出(含换行): %q", css)
	}

	lf := byName["Langfuse"]
	if lf == nil {
		t.Fatal("导出里找不到 Langfuse 那条")
	}
	if asString(lf["mode"]) != customPageModeIframe {
		t.Errorf("直嵌模式未原样导出: %v —— 恢复成反代会让页面打不开", lf["mode"])
	}
	if enabled, ok := lf["enabled"].(bool); !ok || enabled {
		t.Errorf("enabled=false 未原样导出: %v", lf["enabled"])
	}
}
