package common

import (
	"encoding/json"
	"net/http"
	"os"
)

// IframeURLs 结构体用于存储各个服务的 iframe URL
type IframeURLs struct {
	Host          string `json:"host"`
	MySQL         string `json:"mysql"`
	MongoDB       string `json:"mongodb"`
	Redis         string `json:"redis"`
	Kafka         string `json:"kafka"`
	Elasticsearch string `json:"elasticsearch"`
	K8s           string `json:"k8s,omitempty"`
}

// CustomPageEntry 是「自定义页面」在菜单里的一项。
// key 同时是前端路由(/custom/<id>)与 iframeUrls 的键，url 已按嵌法算好：
// 反代模式给平台内路径(${BASE}/custom/<id>/)，直嵌模式给目标站原始地址。
type CustomPageEntry struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// CustomPagesProvider 由 main 注入：返回已启用的自定义页面(未配置时返回空)。
// 与 CustomPageResolver 同理放在 common，避免 common import alert 成环。
var CustomPagesProvider func(pathPrefix string) []CustomPageEntry

// LogURLs 存储日志相关的 iframe URL
type LogURLs struct {
	ContainerConsole string `json:"containerConsole"`
	ServiceLogs      string `json:"serviceLogs"`
}

// TraceURLs 存储链路追踪相关的 iframe URL
type TraceURLs struct {
	Tempo string `json:"tempo"`
}

// 获取 iframe URL 的处理函数
func GetIframeUrls(w http.ResponseWriter, r *http.Request) {
	// 获取子路径前缀（从 X-Forwarded-Path header 或默认为空）
	pathPrefix := r.Header.Get("X-Forwarded-Path")

	// 自定义页面：一条已启用的配置出一个菜单项；一条都没有时前端不显示「自定义」分组。
	var customPages []CustomPageEntry
	if CustomPagesProvider != nil {
		customPages = CustomPagesProvider(pathPrefix)
	}

	// K8s 仅在 incluster 模式下返回入口；off 时返回空串，前端据此隐藏菜单。
	// 1.5.3 前无论是否配置都返回，点进去是永远空的面板。
	k8sURL := ""
	if os.Getenv("ENV_K8S_MONITOR_MODE") == "incluster" {
		k8sURL = pathPrefix + "/prometheus/grafana/d/PwMJtdvnz/kubernetes-dashboard?orgId=1&kiosk"
	}

	// iframe 嵌入时统一加 &kiosk，隐藏 Grafana 内置 sidebar/topnav，仅展示内容
	iframeUrls := IframeURLs{
		Host:          pathPrefix + "/prometheus/grafana/d/hap-node-exporter/server-resource-monitoring?orgId=1&kiosk",
		MySQL:         pathPrefix + "/prometheus/grafana/d/MQWgroiiz/mysql-dashboard?orgId=1&kiosk",
		MongoDB:       pathPrefix + "/prometheus/grafana/d/fwPKIsnih/mongodb-dashboard?orgId=1&kiosk",
		Redis:         pathPrefix + "/prometheus/grafana/d/xDLNRKUWz/redis-dashboard?orgId=1&kiosk",
		Kafka:         pathPrefix + "/prometheus/grafana/d/jwPKIsniz/kafka-dashboard?orgId=1&kiosk",
		Elasticsearch: pathPrefix + "/prometheus/grafana/d/4yyL6dBMk/elasticsearch-dashboard?orgId=1&kiosk",
		K8s:           k8sURL,
	}

	logUrls := LogURLs{
		ContainerConsole: pathPrefix + "/prometheus/grafana/d/ContainerCons/?orgId=1&kiosk",
		ServiceLogs:      pathPrefix + "/prometheus/grafana/d/ServiceLogs/?orgId=1&kiosk",
	}

	traceUrls := TraceURLs{
		// 嵌功能聚合 dashboard：服务依赖图(Service Graph) + RED 指标 + Recent Traces 表，kiosk 干净嵌入
		Tempo: pathPrefix + "/prometheus/grafana/d/TempoDashbrd/tempo-dashboard?orgId=1&kiosk",
	}

	w.Header().Set("Content-Type", "application/json")
	// custom 与 resource/log/trace 并列但形态不同：它是数组(条目名由用户起，没有固定键)，
	// 前端据此单独渲染「自定义」分组。没有已启用的自定义页面时该字段为空数组。
	response := struct {
		Resource IframeURLs        `json:"resource"`
		Log      LogURLs           `json:"log"`
		Trace    TraceURLs         `json:"trace"`
		Custom   []CustomPageEntry `json:"custom"`
	}{
		Resource: iframeUrls,
		Log:      logUrls,
		Trace:    traceUrls,
		Custom:   customPages,
	}
	if response.Custom == nil {
		response.Custom = []CustomPageEntry{}
	}

	// 返回 JSON
	json.NewEncoder(w).Encode(response)
}
