package model

import (
	"regexp"
	"strings"
)

// MetricTemplate 指标模板定义
type MetricTemplate struct {
	Type        string `json:"type"`        // 指标类型标识
	Name        string `json:"name"`        // 显示名称
	Description string `json:"description"` // 描述说明
	PromQL      string `json:"promql"`      // PromQL 模板
	Unit        string `json:"unit"`        // 单位（如 %）
}

// MetricCategory 指标分类
type MetricCategory struct {
	Category    string           `json:"category"`    // 分类标识
	Name        string           `json:"name"`        // 分类名称
	Description string           `json:"description"` // 分类描述
	Templates   []MetricTemplate `json:"templates"`   // 模板列表
}

// GetMetricCategories 获取所有指标分类和模板
// 这里使用内置定义，后续可扩展为从配置文件或数据库读取
func GetMetricCategories() []MetricCategory {
	return []MetricCategory{
		{
			Category:    "server",
			Name:        "服务器监控",
			Description: "基于 node_exporter 的服务器基础基础资源监控",
			Templates: []MetricTemplate{
				{
					Type:        "cpu",
					Name:        "CPU 使用率",
					Description: "服务器 CPU 使用率（所有核心平均值）",
					PromQL:      `100 - (avg without(cpu, mode) (irate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`,
					Unit:        "%",
				},
				{
					Type:        "memory",
					Name:        "内存使用率",
					Description: "服务器内存使用率",
					PromQL:      `(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100`,
					Unit:        "%",
				},
				{
					Type:        "disk",
					Name:        "磁盘使用率",
					Description: "服务器根分区磁盘使用率",
					PromQL:      `(1 - (node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"})) * 100`,
					Unit:        "%",
				},
				{
					Type:        "disk_io",
					Name:        "磁盘 IO 使用率",
					Description: "服务器磁盘 IO 繁忙程度",
					PromQL:      `irate(node_disk_io_time_seconds_total[5m]) * 100`,
					Unit:        "%",
				},
				{
					Type:        "online",
					Name:        "在线状态",
					Description: "基于 Prometheus up 指标判断目标在线状态（1=在线，0=离线），默认当 up=0（离线）时告警",
					PromQL:      `up`,
					Unit:        "",
				},
				{
					Type:        "network_in",
					Name:        "网络入流量",
					Description: "服务器网络接收速率",
					PromQL:      `irate(node_network_receive_bytes_total{device!~"lo|veth.*|docker.*|br.*"}[5m]) / 1024 / 1024`,
					Unit:        "MB/s",
				},
				{
					Type:        "network_out",
					Name:        "网络出流量",
					Description: "服务器网络发送速率",
					PromQL:      `irate(node_network_transmit_bytes_total{device!~"lo|veth.*|docker.*|br.*"}[5m]) / 1024 / 1024`,
					Unit:        "MB/s",
				},
			},
		},
		{
			Category:    "kubernetes",
			Name:        "Kubernetes 集群",
			Description: "集群上报状态与节点健康。多集群时按 origin_prometheus 标签区分集群",
			Templates: []MetricTemplate{
				{
					// 推模式（采集组件 remote_write）下最该有的一条告警。
					// 拉模式时 Prometheus 的 up 天然就是存活探针，target 挂了 targets 页立刻红；
					// 换成推之后，「没配过 / 配错了 / 网络断了 / 集群没了」在平台侧长得一模一样，
					// 全都只表现为「没数据」，没人主动去看就永远发现不了。这条把它补回来。
					// 阈值按上报间隔定：采集端默认 15s scrape，给 300 秒（5 分钟）足够宽松。
					Type:        "cluster_stale",
					Name:        "集群数据断流",
					Description: "距该集群最后一次上报指标已过去多久。多集群纳管必配——集群不再推数据时，平台侧看不出与「从未配置」的区别",
					PromQL:      `time() - max by (origin_prometheus) (timestamp(kube_node_info))`,
					Unit:        "秒",
				},
				{
					Type:        "node_not_ready",
					Name:        "节点 NotReady 数量",
					Description: "集群内处于 NotReady 状态的节点数",
					PromQL:      `count by (origin_prometheus) (kube_node_status_condition{condition="Ready",status="true"} == 0)`,
					Unit:        "个",
				},
				{
					Type:        "pod_crashloop",
					Name:        "CrashLoopBackOff Pod 数量",
					Description: "集群内处于 CrashLoopBackOff 的容器数",
					PromQL:      `count by (origin_prometheus) (kube_pod_container_status_waiting_reason{reason="CrashLoopBackOff"} == 1)`,
					Unit:        "个",
				},
			},
		},
		{
			Category:    "custom",
			Name:        "自定义 PromQL",
			Description: "直接编写自定义 PromQL 表达式进行高级监控",
			Templates: []MetricTemplate{
				{
					Type:        "custom_query",
					Name:        "自定义查询",
					Description: "手动编写 PromQL 表达式,支持任意复杂的监控场景",
					PromQL:      "", // 用户自定义,不提供默认值
					Unit:        "", // 用户自定义单位
				},
			},
		},
	}
}

// sanitizeFilterValue 清除用户输入中可能破坏正则语法的特殊字符
// 只保留 IP/域名/K8s 名称中合法的字符集: 字母、数字、.、-、_、:
var safeFilterValueRe = regexp.MustCompile(`[^a-zA-Z0-9._:\-]`)

func sanitizeFilterValue(s string) string {
	return safeFilterValueRe.ReplaceAllString(s, "")
}

// GeneratePromQL 根据分类、指标类型和过滤条件生成 PromQL
func GeneratePromQL(category, metricType string, filters map[string]string) (string, bool) {
	categories := GetMetricCategories()
	var template string
	found := false

	for _, cat := range categories {
		if cat.Category == category {
			for _, tpl := range cat.Templates {
				if tpl.Type == metricType {
					template = tpl.PromQL
					found = true
					break
				}
			}
		}
	}

	if !found {
		return "", false
	}

	// 如果没有过滤条件，直接返回模板
	if len(filters) == 0 {
		return template, true
	}

	// Server 监控过滤: instance
	if category == "server" {
		if instance, ok := filters["instance"]; ok && instance != "" {
			// 获取操作符，默认是 =~ (包含)
			op := "=~"
			if val, ok := filters["instance_op"]; ok && val == "exclude" {
				op = "!~"
			}

			// 支持多IP输入（逗号分隔），并兼容带端口/不带端口的匹配
			parts := strings.Split(instance, ",")
			var validParts []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					validParts = append(validParts, sanitizeFilterValue(p))
				}
			}

			if len(validParts) > 0 {
				regexPattern := strings.Join(validParts, "|")
				injector := `instance` + op + `"^(` + regexPattern + `)(:.*)?$",`
				template = replaceSelector(template, injector)
			}
		}
	}

	// Kubernetes 监控过滤: origin_prometheus（集群名）
	// 多集群纳管时按集群建规则用——不填则一条规则覆盖所有已上报的集群。
	if category == "kubernetes" {
		if cluster, ok := filters["origin_prometheus"]; ok && cluster != "" {
			op := "=~"
			if val, ok := filters["origin_prometheus_op"]; ok && val == "exclude" {
				op = "!~"
			}

			parts := strings.Split(cluster, ",")
			var validParts []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					validParts = append(validParts, sanitizeFilterValue(p))
				}
			}

			if len(validParts) > 0 {
				injector := `origin_prometheus` + op + `"^(` + strings.Join(validParts, "|") + `)$",`
				template = replaceSelector(template, injector)
			}
		}
	}

	return template, true
}

// replaceSelector 在 PromQL 的指标选择器中安全地注入 Label 条件
func replaceSelector(promql, injection string) string {
	re := regexp.MustCompile(`\b(node_[a-zA-Z0-9_]+|container_[a-zA-Z0-9_]+|kube_[a-zA-Z0-9_]+|up)\b(\{[^}]*\})?`)

	return re.ReplaceAllStringFunc(promql, func(match string) string {
		if strings.Contains(match, "{") {
			// 指标已有选择器，在 { 后注入
			return strings.Replace(match, "{", "{"+injection, 1)
		}
		// 指标没有选择器，创建新的
		return match + "{" + strings.TrimSuffix(injection, ",") + "}"
	})
}
