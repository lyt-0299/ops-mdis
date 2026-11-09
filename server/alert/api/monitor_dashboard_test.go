package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestEnsureMonitorDashboardMigratesLegacyPanelsOnce(t *testing.T) {
	var created atomic.Bool
	var loads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/mdis/prometheus/grafana/api/dashboards/uid/"+monitorDashboardUID:
			if !created.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"meta":{"slug":"dashboard-renamed-in-grafana"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/mdis/prometheus/grafana/api/dashboards/db":
			if created.Load() {
				t.Error("existing Grafana layout was overwritten")
			}
			var body struct {
				Overwrite bool `json:"overwrite"`
				Dashboard struct {
					UID      string `json:"uid"`
					Title    string `json:"title"`
					Editable bool   `json:"editable"`
					Panels   []struct {
						Type    string `json:"type"`
						Targets []struct {
							Expr    string `json:"expr"`
							Instant bool   `json:"instant"`
							Range   bool   `json:"range"`
						} `json:"targets"`
					} `json:"panels"`
				} `json:"dashboard"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.Overwrite || body.Dashboard.UID != monitorDashboardUID || body.Dashboard.Title != "MDIS 自定义监控" || !body.Dashboard.Editable {
				t.Errorf("unexpected dashboard settings: %+v", body)
			}
			panels := body.Dashboard.Panels
			if len(panels) != 2 || len(panels[0].Targets) != 1 || len(panels[1].Targets) != 1 {
				t.Errorf("legacy panels were not migrated: %+v", panels)
			} else {
				if p := panels[0]; p.Type != "table" || !p.Targets[0].Instant || p.Targets[0].Range || p.Targets[0].Expr != "mongodb_dbstats_storageSize" {
					t.Errorf("incorrect table migration: %+v", p)
				}
				if p := panels[1]; p.Type != "timeseries" || p.Targets[0].Instant || !p.Targets[0].Range || p.Targets[0].Expr != "mysql_up" {
					t.Errorf("incorrect chart migration: %+v", p)
				}
			}
			created.Store(true)
			_, _ = w.Write([]byte(`{"slug":"mdis-custom-monitoring"}`))
		default:
			t.Errorf("unexpected Grafana request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL + "/mdis/prometheus/grafana")
	load := func(context.Context) ([]monitorPanel, error) {
		loads++
		return []monitorPanel{
			{Name: "Database Size", View: "table", PromQL: "mongodb_dbstats_storageSize"},
			{Name: "MySQL", View: "chart", PromQL: "mysql_up"},
		}, nil
	}
	for _, want := range []string{"mdis-custom-monitoring", "dashboard-renamed-in-grafana"} {
		got, err := ensureMonitorDashboard(context.Background(), base, load)
		if err != nil || got != want {
			t.Fatalf("ensureMonitorDashboard() = %q, %v; want %q", got, err, want)
		}
	}
	if loads != 1 {
		t.Fatalf("legacy panels loaded %d times", loads)
	}
}

func TestEnsureMonitorDashboardHandlesConcurrentCreation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			w.WriteHeader(http.StatusNotFound)
		case 2:
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			_, _ = w.Write([]byte(`{"meta":{"slug":"created-by-another-request"}}`))
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL)
	got, err := ensureMonitorDashboard(context.Background(), base, func(context.Context) ([]monitorPanel, error) {
		return nil, nil
	})
	if err != nil || got != "created-by-another-request" {
		t.Fatalf("ensureMonitorDashboard() = %q, %v", got, err)
	}
}
