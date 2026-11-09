package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
)

const monitorDashboardUID = "mdis-custom-monitoring"

var monitorGrafanaClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func openMonitorDashboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	upstream := os.Getenv("ENV_PROMETHEUS_GRAFANA")
	if upstream == "" {
		upstream = "http://ops-grafana:3000"
	}
	prefix := strings.TrimRight(r.Header.Get("X-Forwarded-Path"), "/")
	base, err := url.Parse(upstream)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		httputil.WriteErr(w, http.StatusBadGateway, "monitor_grafana_unavailable")
		return
	}
	base = base.JoinPath(prefix, "prometheus/grafana")
	slug, err := ensureMonitorDashboard(ctx, base, loadMonitorPanels)
	if err != nil {
		log.Printf("[monitor-dashboard] %v", err)
		httputil.WriteErr(w, http.StatusBadGateway, "monitor_grafana_unavailable")
		return
	}
	// Only send a path on the authenticated MDIS gateway.
	path := prefix + "/prometheus/grafana/d/" + monitorDashboardUID
	if slug != "" {
		path += "/" + url.PathEscape(slug)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"url": path})
}

func ensureMonitorDashboard(ctx context.Context, base *url.URL, load func(context.Context) ([]monitorPanel, error)) (string, error) {
	lookup := base.JoinPath("api/dashboards/uid", monitorDashboardUID)
	var existing struct {
		Meta struct {
			Slug string `json:"slug"`
		} `json:"meta"`
	}
	status, err := monitorGrafanaRequest(ctx, http.MethodGet, lookup, nil, &existing)
	if err != nil || status == http.StatusOK {
		return existing.Meta.Slug, err
	}
	if status != http.StatusNotFound {
		return "", fmt.Errorf("Grafana dashboard lookup returned HTTP %d", status)
	}

	panels, err := load(ctx)
	if err != nil {
		return "", fmt.Errorf("load legacy monitor panels: %w", err)
	}
	var saved struct {
		Slug string `json:"slug"`
	}
	status, err = monitorGrafanaRequest(ctx, http.MethodPost, base.JoinPath("api/dashboards/db"), initialMonitorDashboard(panels), &saved)
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
		return saved.Slug, nil
	case http.StatusConflict, http.StatusPreconditionFailed:
		// Another request may have created the dashboard after our initial read.
		// Open the winning version without overwriting any Grafana edits.
		status, err = monitorGrafanaRequest(ctx, http.MethodGet, lookup, nil, &existing)
		if err != nil || status == http.StatusOK {
			return existing.Meta.Slug, err
		}
		return "", fmt.Errorf("Grafana dashboard lookup after conflict returned HTTP %d", status)
	default:
		return "", fmt.Errorf("Grafana dashboard creation returned HTTP %d", status)
	}
}

func monitorGrafanaRequest(ctx context.Context, method string, endpoint *url.URL, payload any, out any) (int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := monitorGrafanaClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode Grafana dashboard response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func initialMonitorDashboard(legacy []monitorPanel) map[string]any {
	datasource := map[string]string{"type": "prometheus", "uid": "DS_PROMETHEUS"}
	panels := make([]map[string]any, 0, len(legacy))
	for i, old := range legacy {
		target := map[string]any{"refId": "A", "datasource": datasource, "expr": old.PromQL, "range": true, "instant": false}
		panel := map[string]any{
			"id": i + 1, "title": old.Name, "type": "timeseries", "datasource": datasource,
			"gridPos": map[string]int{"x": 0, "y": i * 9, "w": 24, "h": 9},
			"targets": []any{target},
		}
		if old.View == "table" {
			panel["type"] = "table"
			target["format"], target["range"], target["instant"] = "table", false, true
			panel["transformations"] = []any{map[string]any{
				"id": "organize",
				"options": map[string]any{
					"excludeByName": map[string]bool{"__name__": true, "Time": true},
					"indexByName":   map[string]int{"database": 0, "schema": 1, "instance": 2, "Value": 3},
					"renameByName":  map[string]string{"database": "数据库", "schema": "库名", "instance": "实例", "Value": "值"},
				},
			}}
			panel["options"] = map[string]any{
				"sortBy": []any{map[string]any{"displayName": "值", "desc": true}},
			}
		}
		panels = append(panels, panel)
	}
	return map[string]any{
		"overwrite": false,
		"dashboard": map[string]any{
			"uid": monitorDashboardUID, "title": "MDIS 自定义监控", "editable": true,
			"schemaVersion": 39, "panels": panels, "refresh": "30s",
			"time": map[string]string{"from": "now-1h", "to": "now"},
		},
	}
}
