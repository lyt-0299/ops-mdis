package service

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// PrometheusClient Prometheus HTTP API 客户端
type PrometheusClient struct {
	dataSource *model.DataSource
	httpClient *http.Client
}

// 连接池：复用同一 DataSource 的 HTTP Client
// 键由 string ID 替代原 uint ID
var (
	promClientPool = make(map[string]*promPoolEntry)
	promPoolLock   sync.RWMutex
)

type promPoolEntry struct {
	client    *PrometheusClient
	updatedAt time.Time // 用于检测配置变更（DataSource 无 UpdatedAt，此处保留字段备用）
}

// NewPrometheusClient 创建或复用 Prometheus 客户端
func NewPrometheusClient(ds *model.DataSource) *PrometheusClient {
	// 尝试从缓存复用
	promPoolLock.RLock()
	if entry, ok := promClientPool[ds.ID]; ok && ds.ID != "" {
		promPoolLock.RUnlock()
		return entry.client
	}
	promPoolLock.RUnlock()

	// 创建新客户端
	promPoolLock.Lock()
	defer promPoolLock.Unlock()

	// 双重检查
	if entry, ok := promClientPool[ds.ID]; ok && ds.ID != "" {
		// 关闭旧连接（配置变更时）
		if t, ok := entry.client.httpClient.Transport.(*http.Transport); ok {
			t.CloseIdleConnections()
		}
		_ = entry // suppress unused warning; entry may still be reused below if keys match
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: ds.SkipSSL,
		},
	}

	pc := &PrometheusClient{
		dataSource: ds,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}

	if ds.ID != "" {
		promClientPool[ds.ID] = &promPoolEntry{
			client:    pc,
			updatedAt: time.Now(),
		}
	}

	return pc
}

// NewEphemeralPrometheusClient 创建一次性 Prometheus 客户端，不入连接池、
// 不复用、不污染池中告警引擎在用的客户端。用于连接测试 / PromQL 预览等
// 临时、可能携带未保存或变动配置的场景。
func NewEphemeralPrometheusClient(ds *model.DataSource) *PrometheusClient {
	return &PrometheusClient{
		dataSource: ds,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: ds.SkipSSL},
			},
			Timeout: 10 * time.Second,
		},
	}
}

// PrometheusQueryResult Prometheus 即时查询结果
type PrometheusQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"` // [timestamp, value]
		} `json:"result"`
	} `json:"data"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

// addAuth 添加认证信息到请求
func (c *PrometheusClient) addAuth(req *http.Request) {
	switch c.dataSource.AuthType {
	case "basic":
		req.SetBasicAuth(c.dataSource.Username, c.dataSource.Password)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.dataSource.Token)
	}
}

// TestConnection 测试连接
func (c *PrometheusClient) TestConnection() error {
	// 使用 /-/ready 端点检查 Prometheus 是否就绪
	reqURL := fmt.Sprintf("%s/-/ready", c.dataSource.URL)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("连接失败，状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Query 执行即时查询
func (c *PrometheusClient) Query(promQL string) (*PrometheusQueryResult, error) {
	reqURL := fmt.Sprintf("%s/api/v1/query?query=%s", c.dataSource.URL, url.QueryEscape(promQL))
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result PrometheusQueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("查询错误: %s - %s", result.ErrorType, result.Error)
	}

	return &result, nil
}

// VectorResult 表示单个序列的结果
type VectorResult struct {
	Metric map[string]string
	Value  float64
}

// GetVectorValues 获取查询结果的所有序列值
func (c *PrometheusClient) GetVectorValues(promQL string) ([]VectorResult, error) {
	result, err := c.Query(promQL)
	if err != nil {
		return nil, err
	}

	if len(result.Data.Result) == 0 {
		return nil, nil
	}

	var results []VectorResult
	for _, res := range result.Data.Result {
		if len(res.Value) < 2 {
			continue
		}

		valueStr, ok := res.Value[1].(string)
		if !ok {
			continue
		}

		var val float64
		if _, err := fmt.Sscanf(valueStr, "%f", &val); err != nil {
			continue
		}

		// 忽略无效值
		if math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}

		results = append(results, VectorResult{
			Metric: res.Metric,
			Value:  val,
		})
	}

	return results, nil
}

// GetFirstValue 获取查询结果的第一个值（保持向下兼容）
func (c *PrometheusClient) GetFirstValue(promQL string) (float64, error) {
	results, err := c.GetVectorValues(promQL)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("查询无结果")
	}
	return results[0].Value, nil
}

// GetMaxValue 获取查询结果中所有序列的最大值。
func (c *PrometheusClient) GetMaxValue(promQL string) (float64, error) {
	results, err := c.GetVectorValues(promQL)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("查询无结果")
	}

	maxValue := results[0].Value
	for _, res := range results[1:] {
		if res.Value > maxValue {
			maxValue = res.Value
		}
	}
	return maxValue, nil
}
