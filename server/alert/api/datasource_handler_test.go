package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
)

func TestCreateThenListDataSource(t *testing.T) {
	if os.Getenv("ENV_OPS_MONGODB_URI") == "" {
		os.Setenv("ENV_OPS_MONGODB_URI", "mongodb://localhost:27017")
	}
	if err := database.Init(); err != nil { // Init pings; no mongo -> err -> skip
		t.Skipf("无可用 ops-mongo,跳过集成测试: %v", err)
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux)

	body := []byte(`{"name":"prom-local51","type":"prometheus","url":"http://ops-prometheus:9090"}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/alert/datasources", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("创建失败: %d %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/alert/datasources", nil))
	if rec2.Code != http.StatusOK || !bytes.Contains(rec2.Body.Bytes(), []byte("prom-local51")) {
		t.Fatalf("列表未含新建: %d %s", rec2.Code, rec2.Body.String())
	}
}
