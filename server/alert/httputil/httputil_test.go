package httputil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]any{"a": 1})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("头/码错误: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `"a":1`) {
		t.Fatalf("body 错误: %s", rec.Body.String())
	}
}

func TestReadJSON(t *testing.T) {
	var v struct {
		Name string `json:"name"`
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"x"}`))
	if err := ReadJSON(req, &v); err != nil || v.Name != "x" {
		t.Fatalf("解析失败: %v %+v", err, v)
	}
}
