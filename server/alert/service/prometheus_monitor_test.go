package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

func TestReadMonitorData(t *testing.T) {
	const expression = `sum by (instance) (mongodb_up{job="mongodb"})`
	const data = `{"resultType":"matrix","result":[{"metric":{"instance":"prod"},"values":[[42,"1"],[57,"0"]]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/prometheus/server/api/v1/query_range" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.FormValue("query") != expression || r.FormValue("step") != "15" {
			t.Errorf("unexpected request form: %v", r.Form)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("request missing data source authorization")
		}
		_, _ = w.Write([]byte(`{"status":"success","data":` + data + `}`))
	}))
	defer server.Close()

	client := NewEphemeralPrometheusClient(&model.DataSource{
		URL: server.URL + "/prometheus/server/", AuthType: "bearer", Token: "test-token",
	})
	result, err := client.ReadMonitorData(context.Background(), http.MethodPost, "/api/v1/query_range", url.Values{
		"query": {expression}, "step": {"15"},
	})
	if err != nil || string(result) != data {
		t.Fatalf("result = %s, error = %v", result, err)
	}
}

func TestReadMonitorDataReturnsPrometheusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":"parse error: unexpected end of input"}`))
	}))
	defer server.Close()

	client := NewEphemeralPrometheusClient(&model.DataSource{URL: server.URL})
	_, err := client.ReadMonitorData(context.Background(), http.MethodPost, "/api/v1/query", url.Values{"query": {"sum("}})
	if err == nil || !strings.Contains(err.Error(), "unexpected end of input") {
		t.Fatalf("expected PromQL parse error, got %v", err)
	}
}
