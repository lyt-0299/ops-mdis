package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errProfileForbidden is a sentinel error simulating a system.profile permission denial.
var errProfileForbidden = errors.New("not authorized to execute command on database: system.profile")

func TestMongoDiagnoseReason(t *testing.T) {
	// system.profile 不可读 → 应给出明确原因
	got := diagnoseReasonFromErr(errProfileForbidden)
	if got == "" {
		t.Fatal("应给出不可读 system.profile 的原因")
	}
}

func TestMongoAlertStandalone(t *testing.T) {
	// replSetGetStatus 报 "not running with --replSet" → 单节点不可用
	if !isStandaloneErr("not running with --replSet") {
		t.Fatal("应识别 standalone")
	}
}

func TestIsStandaloneErrVariants(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"not running with --replSet", true},
		{"NoReplicationEnabled", true},
		{"some other error", false},
		{"", false},
	}
	for _, c := range cases {
		got := isStandaloneErr(c.msg)
		if got != c.want {
			t.Errorf("isStandaloneErr(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestDiagnoseReasonFromErrNil(t *testing.T) {
	// nil error → 空字符串
	if got := diagnoseReasonFromErr(nil); got != "" {
		t.Fatalf("expected empty reason for nil err, got %q", got)
	}
}

func TestDiagnoseReasonFromErrUnauthorized(t *testing.T) {
	// Unauthorized error variant
	err := errors.New("Unauthorized to read from collection")
	got := diagnoseReasonFromErr(err)
	if got == "" {
		t.Fatal("should return reason for Unauthorized error")
	}
	// should mention profile read permission
	if !containsAny(got, []string{"权限", "system.profile", "无法"}) {
		t.Fatalf("unexpected reason %q", got)
	}
}

func TestDiagnoseReasonFromErrGeneric(t *testing.T) {
	err := errors.New("network timeout")
	got := diagnoseReasonFromErr(err)
	if got == "" {
		t.Fatal("generic error should produce a reason string")
	}
}

// containsAny checks if s contains any of the given substrings.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// 目标把自己的对外地址写死在重定向里(Grafana 配了 root_url 就会 301 过去)，
// 而那个地址从平台这边连不上时，错误必须点出跳转目标 —— 否则用户拿着一个其实
// 好好的地址反复排查。实测 http://ops-grafana:3000 就是 301 到 localhost:3000。
func TestProbeWebUIReportsRedirectTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 跳到一个必然连不上的地址(端口 1 上不会有服务)
		http.Redirect(w, r, "http://127.0.0.1:1/dead-end", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	err, caps := ProbeWebUICapabilities(srv.URL)
	if err == nil {
		t.Fatal("跳转目标连不上时应报错")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("错误里应点出跳转目标，实际: %v", err)
	}
	if caps != nil {
		t.Fatalf("失败时不该返回能力位，实际 %v", caps)
	}
}

// 目标自带登录页(Langfuse 首页就是 401)时不能判成连不上 —— 服务本身是活的。
func TestProbeWebUIAcceptsAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if err, caps := ProbeWebUICapabilities(srv.URL); err != nil || caps["ui"].Available != true {
		t.Fatalf("401 应视为可达，实际 err=%v caps=%v", err, caps)
	}
}

// 404/5xx 说明地址填得不对(填到某个子路径或别的服务上了)，要报出来。
func TestProbeWebUIRejectsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err, _ := ProbeWebUICapabilities(srv.URL); err == nil {
		t.Fatal("404 应报错")
	}
}
