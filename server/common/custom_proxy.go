package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// CustomPageResolver 由 main 注入：按 slug 返回该自定义页面当前生效的目标地址与要注入的 CSS。
// 地址空串 = 未配置/已停用/不是反代模式。取自「数据源」页的自定义页面配置(ops-mongo)；
// 放在 common 以避免 common import alert 造成环(同 slowquery 的桥接思路)。
var CustomPageResolver func(slug string) (string, string)

// customProxyPrefix 是网关把 ${BASE}/custom/ 重写后打到 ops-server 的前缀。
const customProxyPrefix = "/api/custom-proxy/"

// CustomProxyHandler 动态反代自定义页面：${BASE}/custom/<slug>/<rest> → 目标站 /<rest>。
// 每次请求按 CustomPageResolver 取目标(运行时可改，改完即时生效)。
// 未配置/已停用时返回 503。网关已用 auth_request 鉴权，这里再经 AuthMiddleware 双保险。
func CustomProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, customProxyPrefix)
		slug, tail, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		if slug == "" {
			http.Error(w, "custom page not specified", http.StatusNotFound)
			return
		}

		target, injectCSS := "", ""
		if CustomPageResolver != nil {
			target, injectCSS = CustomPageResolver(slug)
			target = strings.TrimSpace(target)
		}
		if target == "" {
			http.Error(w, "custom page not configured", http.StatusServiceUnavailable)
			return
		}
		u, err := url.Parse(target)
		if err != nil || u.Scheme == "" || u.Host == "" {
			http.Error(w, "custom page URL invalid", http.StatusBadGateway)
			return
		}

		r.URL.Path = "/" + tail

		proxy := httputil.NewSingleHostReverseProxy(u)
		base := proxy.Director
		proxy.Director = func(req *http.Request) {
			base(req)
			// 目标站多按 Host 路由(Flink、各类反代后的服务都是)，须改成目标 Host。
			req.Host = u.Host
			if injectCSS != "" {
				// 要改写 HTML 就不能让上游压缩：压过的响应体没法直接做字符串替换。
				req.Header.Del("Accept-Encoding")
			}
		}
		if injectCSS != "" {
			proxy.ModifyResponse = injectCSSIntoHTML(injectCSS)
		}
		proxy.ServeHTTP(w, r)
	})
}

// injectCSSIntoHTML 往 HTML 响应的 </head> 前插一段 <style>。
// 1.5.12 前这件事由网关 sub_filter 做，且只对 Flink 一个写死的 location 生效；
// 挪进来之后每条自定义页面都能带自己的样式，网关不必为每个页面加一条 location。
func injectCSSIntoHTML(css string) func(*http.Response) error {
	style := []byte("<style>" + css + "</style></head>")
	return func(resp *http.Response) error {
		if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		// 只替第一个 </head>，与原 sub_filter_once on 的行为一致。
		if idx := bytes.Index(bytes.ToLower(body), []byte("</head>")); idx >= 0 {
			body = append(body[:idx:idx], append(style, body[idx+len("</head>"):]...)...)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
}
