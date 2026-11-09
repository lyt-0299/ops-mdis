package api

import (
	"strings"
	"testing"
)

// slug 就是自定义页面的访问路径(${BASE}/custom/<slug>/)，必须始终是能进 URL 的一段。
// 名字里带中文、空格、斜杠的场景比英文名更常见，这里生成坏了会直接生成一条打不开的菜单。
func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Langfuse", "langfuse"},
		{"Flink", "flink"},
		{"My Dashboard", "my-dashboard"},
		{"a//b", "a-b"},
		{"  Trim Me  ", "trim-me"},
		{"Grafana 2", "grafana-2"},
		{"---", ""}, // 全是分隔符 → 走随机兜底，单独判
	}
	for _, c := range cases {
		got := slugify(c.in)
		if c.want == "" {
			if !strings.HasPrefix(got, "page-") {
				t.Errorf("slugify(%q) = %q，期望回退成 page-xxxxxx", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("slugify(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 纯中文名取不出任何 ascii —— 不能返回空串(那会让 /custom// 这种路径出现)，
// 必须回退成一个随机但合法的 slug。
func TestSlugifyNonASCII(t *testing.T) {
	got := slugify("链路平台")
	if !strings.HasPrefix(got, "page-") {
		t.Fatalf("纯中文名 slug = %q，期望 page- 前缀的随机值", got)
	}
	if got == slugify("链路平台") {
		// 两次调用应各自随机，撞上的概率是 1/16M，撞了说明没在随机
		t.Log("两次随机值相同（概率极低，非致命）")
	}
}

// slug 超长会让菜单链接难看且不好排查，截断后不能留下尾部的连字符。
func TestSlugifyLongName(t *testing.T) {
	got := slugify(strings.Repeat("ab-", 40))
	if len(got) > 32 {
		t.Fatalf("slug 长度 = %d，期望 ≤32", len(got))
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("slug = %q，截断后不该以 - 结尾", got)
	}
}

// 请求体校验：地址必须是 http(s)，嵌法只有两种。
// 校验松了的代价是存下一条永远打不开的页面，而 UI 上看不出任何异常。
func TestCustomPageReqNormalize(t *testing.T) {
	truth := true
	cases := []struct {
		name   string
		req    customPageReq
		errKey string
		mode   string
	}{
		{"缺名称", customPageReq{URL: "http://x"}, "alert_custom_page_name_required", ""},
		{"非 http 地址", customPageReq{Name: "x", URL: "ftp://x"}, "alert_custom_page_url_invalid", ""},
		{"空地址", customPageReq{Name: "x"}, "alert_custom_page_url_invalid", ""},
		{"未知嵌法", customPageReq{Name: "x", URL: "http://x", Mode: "magic"}, "alert_custom_page_mode_invalid", ""},
		{"默认反代", customPageReq{Name: "x", URL: "http://x"}, "", "proxy"},
		{"直嵌", customPageReq{Name: "x", URL: "https://x", Mode: "iframe", Enabled: &truth}, "", "iframe"},
	}
	for _, c := range cases {
		_, _, mode, _, errKey := c.req.normalize()
		if errKey != c.errKey {
			t.Errorf("%s: errKey = %q，期望 %q", c.name, errKey, c.errKey)
		}
		if c.errKey == "" && mode != c.mode {
			t.Errorf("%s: mode = %q，期望 %q", c.name, mode, c.mode)
		}
	}
}

// 存储文档 → 视图对象：反代模式要带出平台内访问路径，直嵌模式没有这个路径；
// 旧文档没有 enabled 字段时视为启用(与迁移前 Flink 单例的口径一致，别让升级把已配好的页面关掉)。
func TestCustomPageDocView(t *testing.T) {
	off := false
	proxy := customPageDoc{ID: "flink", Name: "Flink", URL: " http://f:8081 "}.view()
	if proxy.Mode != customPageModeProxy || proxy.Path != "/custom/flink" {
		t.Fatalf("反代视图 = %+v，期望 mode=proxy path=/custom/flink", proxy)
	}
	if !proxy.Enabled {
		t.Fatal("无 enabled 字段的旧文档应视为启用")
	}
	if proxy.URL != "http://f:8081" {
		t.Fatalf("URL = %q，期望去掉首尾空白", proxy.URL)
	}

	direct := customPageDoc{ID: "lf", Name: "Langfuse", URL: "http://lf:3000",
		Mode: customPageModeIframe, Enabled: &off}.view()
	if direct.Mode != customPageModeIframe || direct.Path != "" {
		t.Fatalf("直嵌视图 = %+v，期望 mode=iframe 且无 path", direct)
	}
	if direct.Enabled {
		t.Fatal("enabled=false 的文档不该报成启用")
	}
}
