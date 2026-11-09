package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ReadMonitorData reads Prometheus's data envelope without discarding labels,
// timestamps or result types. The caller only supplies fixed read endpoints.
func (c *PrometheusClient) ReadMonitorData(ctx context.Context, method, path string, params url.Values) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(c.dataSource.URL, "/") + path
	var form io.Reader
	if method == http.MethodPost {
		form = strings.NewReader(params.Encode())
	} else {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, form)
	if err != nil {
		return nil, fmt.Errorf("invalid Prometheus endpoint")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.addAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Prometheus did not respond")
	}
	defer resp.Body.Close()

	const maxResponse = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, fmt.Errorf("could not read Prometheus response or response exceeded 8 MiB")
	}
	var result struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid Prometheus response (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || result.Status != "success" || len(result.Data) == 0 {
		return nil, fmt.Errorf("Prometheus HTTP %d: %.1000s", resp.StatusCode, result.Error)
	}
	return result.Data, nil
}
