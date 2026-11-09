package common

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// 反代要按 ${BASE}/custom/<slug>/<rest> 里的 slug 找目标，并且只把 <rest> 转发下去。
// slug 解析错了的后果是「配了 A 打开的是 B」或整页 404，而两者在 UI 上都只表现为空白页。
func TestCustomProxyRoutesBySlug(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var askedSlug string
	CustomPageResolver = func(slug string) (string, string) {
		askedSlug = slug
		if slug == "langfuse" {
			return upstream.URL, ""
		}
		return "", ""
	}
	defer func() { CustomPageResolver = nil }()

	rec := httptest.NewRecorder()
	CustomProxyHandler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/custom-proxy/langfuse/project/traces?x=1", nil))

	if askedSlug != "langfuse" {
		t.Fatalf("解析出的 slug = %q，期望 langfuse", askedSlug)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if gotPath != "/project/traces" {
		t.Fatalf("转发给目标的路径 = %q，期望 /project/traces（slug 段要剥掉）", gotPath)
	}
}

// 未配置/已停用/直嵌模式的 slug 都由 resolver 返回空地址 —— 这里必须是 503，
// 不能把请求打给一个空目标（那会变成 502 或更难懂的错误）。
func TestCustomProxyUnconfigured(t *testing.T) {
	CustomPageResolver = func(string) (string, string) { return "", "" }
	defer func() { CustomPageResolver = nil }()

	rec := httptest.NewRecorder()
	CustomProxyHandler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/custom-proxy/nope/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", rec.Code)
	}

	// 连 slug 都没有(直接打 /api/custom-proxy/)时是 404，与「配置缺失」区分开
	rec = httptest.NewRecorder()
	CustomProxyHandler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/custom-proxy/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("无 slug 时状态码 = %d，期望 404", rec.Code)
	}
}

// CSS 注入接替了原先网关的 sub_filter。Content-Length 不跟着改的话，
// 客户端会按旧长度截断——页面看着「加载了一半」，比不注入更难查。
func TestInjectCSSIntoHTML(t *testing.T) {
	body := "<html><head><title>x</title></head><body>hi</body></html>"
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))

	if err := injectCSSIntoHTML("body{color:red}")(resp); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(out), "<style>body{color:red}</style></head>") {
		t.Fatalf("没在 </head> 前注入 style，实际: %s", out)
	}
	if resp.Header.Get("Content-Length") != strconv.Itoa(len(out)) {
		t.Fatalf("Content-Length = %s，期望 %d", resp.Header.Get("Content-Length"), len(out))
	}
	if resp.ContentLength != int64(len(out)) {
		t.Fatalf("ContentLength 字段 = %d，期望 %d", resp.ContentLength, len(out))
	}
}

// 非 HTML 响应(JS/CSS/JSON/图片)不能碰：往 JS 里插一段 <style> 会直接把脚本弄坏。
func TestInjectCSSSkipsNonHTML(t *testing.T) {
	body := "console.log('</head>')"
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/javascript"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	if err := injectCSSIntoHTML("body{color:red}")(resp); err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	out, _ := io.ReadAll(resp.Body)
	if string(out) != body {
		t.Fatalf("非 HTML 响应被改动了: %s", out)
	}
}
