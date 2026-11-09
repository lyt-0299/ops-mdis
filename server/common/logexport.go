package common

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lokiDefaultURL    = "http://ops-loki:3100"
	logExportMaxLines = 50000 // 单次导出行数上限，超出截断并提示
	lokiPageLimit     = 5000  // Loki 单次 query_range 返回上限
	lokiMaxPages      = 40    // 分页次数上限(40*5000=200000，配合行数上限兜底)
)

// allDetectedLevels 与 service-logs 看板 detected_level 变量一致。
var allDetectedLevels = []string{"error", "warning", "warn", "info", "debug", "trace", "fatal"}

type logLine struct {
	TsNs   int64             `json:"-"`
	Time   string            `json:"time"`
	Labels map[string]string `json:"labels,omitempty"` // Loki stream 标签(container/pod/service_name/level 等)
	Line   string            `json:"line"`
}

// LogExportHandler 导出 Loki 日志为可下载文件。GET /api/log/export
//
// 参数:
//
//	type            service|container          (默认 service)
//	service_name    服务日志 service_name 过滤  (逗号/竖线分隔多值;空/all=全部)
//	detected_level  服务日志级别               (逗号分隔;空/全选=不限级别)
//	container       容器日志 container 过滤     (空/all=全部)
//	search          关键字(不区分大小写)
//	from,to         时间范围(epoch ms 或 Grafana 相对值 now / now-14d;默认近 14 天)
//	format          txt|json                   (默认 txt)
//
// 与服务日志/容器日志看板(service-logs.json / container-console.json)的 LogQL 对齐，
// 由前端读取嵌入 Grafana 的当前筛选透传，导出与页面所见一致的日志。
func LogExportHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	logType := q.Get("type")
	if logType == "" {
		logType = "service"
	}
	format := q.Get("format")
	if format != "json" {
		format = "txt"
	}

	logql, err := buildLogExportQL(logType, q)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "log_unsupported_type", err.Error())
		return
	}

	lokiBase := strings.TrimRight(os.Getenv("ENV_LOKI_URL"), "/")
	if lokiBase == "" {
		lokiBase = lokiDefaultURL
	}

	startNs, endNs := resolveLokiRange(q.Get("from"), q.Get("to"))

	lines, capped, err := lokiFetchAll(lokiBase, logql, startNs, endNs)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "log_loki_query_failed", err.Error())
		return
	}

	fname := fmt.Sprintf("%s-logs-%s.%s", logType, time.Now().Format("20060102-150405"), format)
	if q.Get("inline") == "1" {
		// inline 模式：返回纯数据、不带 attachment 下载头，供前端 fetch 取回内容后用
		// showSaveFilePicker 本地另存。绕开浏览器「下载」环节——某些网络代理会拦截下载并
		// 把文件名改写成 UUID，而本地另存代理碰不到。
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		// 直接下载模式：octet-stream + 简单 filename=（去掉易被代理剥掉的 filename*）。
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fname))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}

	// 先把整个 body 缓冲好，设置 Content-Length（避免 Transfer-Encoding: chunked——
	// 部分代理对 chunked 下载会缓冲重发、过程中丢掉 Content-Disposition 文件名，导致
	// 浏览器把文件存成 UUID；带 Content-Length 的普通下载代理不会动）。
	var body []byte
	if format == "json" {
		body, _ = json.Marshal(map[string]interface{}{
			"query":  logql,
			"count":  len(lines),
			"capped": capped,
			"lines":  lines,
		})
	} else {
		var bw strings.Builder
		for _, ln := range lines {
			bw.WriteString(ln.Time)
			bw.WriteByte(' ')
			if p := labelPrefix(logType, ln.Labels); p != "" {
				bw.WriteString(p)
			}
			bw.WriteString(ln.Line)
			// 行尾追加前缀未展示的其余 label(action/extras/clientip 等)，保证导出字段
			// 与「stream labels expanded」面板一致、不丢字段。
			if s := labelSuffix(logType, ln.Labels); s != "" {
				bw.WriteByte(' ')
				bw.WriteString(s)
			}
			bw.WriteByte('\n')
		}
		if capped {
			bw.WriteString(fmt.Sprintf("\n# 已达导出上限 %d 行，更早日志请缩小时间范围或加筛选\n", logExportMaxLines))
		}
		body = []byte(bw.String())
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// buildLogExportQL 按日志类型构造 LogQL，与对应看板对齐。
func buildLogExportQL(logType string, q url.Values) (string, error) {
	search := buildSearchFilter(q.Get("search"))
	switch logType {
	case "service":
		sn := joinValuesRegex(q.Get("service_name"))
		if sn == "" {
			sn = ".+"
		}
		levelFilter := ""
		if lv := levelsRegex(q.Get("detected_level")); lv != "" {
			levelFilter = " |~ `\"level\":\"(" + lv + ")\"`"
		}
		return fmt.Sprintf("{hostname=~\".+\", service_name=~\"%s\"}%s%s", sn, levelFilter, search), nil
	case "container":
		c := joinValuesRegex(q.Get("container"))
		if c == "" {
			c = ".+"
		}
		return fmt.Sprintf("{log_source=~\"docker|k8s\", container=~\"%s\"}%s", c, search), nil
	default:
		return "", fmt.Errorf("unsupported log type: %s", logType)
	}
}

// labelPrefix 按日志类型把关键 stream 标签拼成 txt 行前缀(json 格式直接带全量 labels，
// 不走此函数)。容器日志补 namespace/pod/container/级别——否则纯文本行看不出来源；
// 服务日志补 service_name/hostname/级别。返回形如 "[ns/pod container warn] "。
func labelPrefix(logType string, labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	level := labels["detected_level"]
	if level == "" {
		level = labels["level"]
	}
	var parts []string
	if logType == "container" {
		ns, pod, c := labels["namespace"], labels["pod"], labels["container"]
		loc := c
		if pod != "" {
			loc = pod
			if c != "" {
				loc += "/" + c
			}
		}
		if ns != "" && loc != "" {
			loc = ns + "/" + loc
		}
		if loc != "" {
			parts = append(parts, loc)
		}
	} else {
		if sn := labels["service_name"]; sn != "" {
			parts = append(parts, sn)
		}
		if h := labels["hostname"]; h != "" {
			parts = append(parts, h)
		}
	}
	if level != "" {
		parts = append(parts, level)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "] "
}

// isEmptyLabelValue 判断 label 值是否为"空值"——空串、零、空数组/对象，
// 这类(如 elapsedmilliseconds=0、extras=[])几乎每行都有且无信息量，导出时跳过。
func isEmptyLabelValue(v string) bool {
	switch strings.TrimSpace(v) {
	case "", "0", "[]", "{}":
		return true
	}
	return false
}

// labelSuffix 把 labelPrefix 没展示的其余 stream 标签拼成行尾 {k=v k=v}，
// 保证导出字段与「stream labels expanded」面板一致(不丢 action/extras/clientip 等)。
// 排除：已在前缀展示的(service_name/hostname/namespace/pod/container/level)，
// 以及噪音标签 timeon(怪格式的重复时间戳)。值为空的标签跳过。
func labelSuffix(logType string, labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	skip := map[string]bool{"detected_level": true, "level": true, "timeon": true}
	if logType == "container" {
		skip["namespace"], skip["pod"], skip["container"] = true, true, true
	} else {
		skip["service_name"], skip["hostname"] = true, true
	}
	keys := make([]string, 0, len(labels))
	for k, v := range labels {
		if skip[k] || isEmptyLabelValue(v) {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	b.WriteByte('}')
	return b.String()
}

// buildSearchFilter 关键字过滤(不区分大小写)。去掉反引号(LogQL 反引号串不可转义)。
func buildSearchFilter(search string) string {
	s := strings.ReplaceAll(strings.TrimSpace(search), "`", "")
	if s == "" {
		return ""
	}
	return " |~ `(?i)" + s + "`"
}

// joinValuesRegex 把逗号/竖线分隔的多值合成正则候选(a|b)；all/$__all/空 → 空串(调用方默认 .+)。
func joinValuesRegex(raw string) string {
	parts := splitMulti(raw)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

// levelsRegex 处理级别多值：全选(含全部 7 个)或空 → 空串(不加级别过滤，导出全部)；子集 → a|b。
func levelsRegex(raw string) string {
	parts := splitMulti(raw)
	if len(parts) == 0 || len(parts) >= len(allDetectedLevels) {
		return ""
	}
	return strings.Join(parts, "|")
}

// splitMulti 拆分逗号/竖线分隔值，剔除空、All、$__all。
func splitMulti(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '|' }) {
		p = strings.TrimSpace(p)
		if p == "" || strings.EqualFold(p, "all") || p == "$__all" {
			return nil // 命中 All → 视为全部
		}
		out = append(out, p)
	}
	return out
}

// resolveLokiRange 解析 from/to → 纳秒时间戳。支持 epoch ms、Grafana 相对值(now / now-14d / now-1h / now-30m)。
// 缺省近 14 天(与看板默认 now-14d 一致)。
func resolveLokiRange(from, to string) (int64, int64) {
	now := time.Now()
	end := parseTimePoint(to, now)
	start := parseTimePoint(from, now.Add(-14*24*time.Hour))
	if start >= end {
		start = end - int64(14*24*time.Hour) // 兜底
	}
	return start, end
}

// parseTimePoint 解析单个时间点为纳秒；无法解析时返回 fallback。
func parseTimePoint(v string, fallback time.Time) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback.UnixNano()
	}
	if v == "now" {
		return time.Now().UnixNano()
	}
	if strings.HasPrefix(v, "now-") {
		if d, ok := parseGrafanaDur(strings.TrimPrefix(v, "now-")); ok {
			return time.Now().Add(-d).UnixNano()
		}
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return ms * int64(time.Millisecond) // epoch ms → ns
	}
	return fallback.UnixNano()
}

// parseGrafanaDur 解析 14d/1h/30m/45s 形式。
func parseGrafanaDur(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, false
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'm':
		return time.Duration(n) * time.Minute, true
	case 's':
		return time.Duration(n) * time.Second, true
	}
	return 0, false
}

// lokiFetchAll 分页拉取 [startNs,endNs] 内全部日志(direction=backward，逐页降低 end)，
// 行数到上限或页数到上限即停。返回按时间升序的结果。
func lokiFetchAll(base, logql string, startNs, endNs int64) ([]logLine, bool, error) {
	var all []logLine
	capped := false
	curEnd := endNs

	for page := 0; page < lokiMaxPages; page++ {
		if curEnd <= startNs {
			break
		}
		vals, minTs, n, err := lokiQueryRange(base, logql, startNs, curEnd)
		if err != nil {
			return nil, false, err
		}
		all = append(all, vals...)
		if len(all) >= logExportMaxLines {
			all = all[:logExportMaxLines]
			capped = true
			break
		}
		if n < lokiPageLimit {
			break // 该范围已取完
		}
		curEnd = minTs - 1 // 下一页取更早的
	}

	sort.Slice(all, func(i, j int) bool { return all[i].TsNs < all[j].TsNs })
	return all, capped, nil
}

// lokiQueryRange 调一次 Loki query_range，返回该页日志、最小时间戳、条数。
func lokiQueryRange(base, logql string, startNs, endNs int64) ([]logLine, int64, int, error) {
	u := base + "/loki/api/v1/query_range?" + url.Values{
		"query":     {logql},
		"start":     {strconv.FormatInt(startNs, 10)},
		"end":       {strconv.FormatInt(endNs, 10)},
		"limit":     {strconv.Itoa(lokiPageLimit)},
		"direction": {"backward"},
	}.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, 0, 0, fmt.Errorf("Loki %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var lr struct {
		Data struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][2]string       `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, 0, 0, err
	}

	var out []logLine
	var minTs int64 = endNs
	for _, st := range lr.Data.Result {
		for _, v := range st.Values {
			ts, _ := strconv.ParseInt(v[0], 10, 64)
			if ts < minTs {
				minTs = ts
			}
			out = append(out, logLine{
				TsNs:   ts,
				Time:   time.Unix(0, ts).Format("2006-01-02 15:04:05.000"),
				Labels: st.Stream,
				Line:   strings.TrimRight(v[1], "\r\n"),
			})
		}
	}
	return out, minTs, len(out), nil
}
