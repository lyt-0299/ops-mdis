package common

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestAuthMiddlewareHandlerBlocksNoCookie(t *testing.T) {
	os.Setenv("ENV_OPS_TOKEN", "secret")
	called := false
	h := AuthMiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/alert/datasources", nil))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("无 cookie 应 401 且不放行: code=%d called=%v", rec.Code, called)
	}
}

func TestAuthMiddlewareHandlerAllowsValidCookie(t *testing.T) {
	os.Setenv("ENV_OPS_TOKEN", "secret")
	called := false
	h := AuthMiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/api/alert/datasources", nil)
	session, err := newAuthSession("secret", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "atoken", Value: session})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("有效 cookie 应放行")
	}
}

func TestAuthMiddlewareHandlerRejectsRawEnvTokenCookie(t *testing.T) {
	os.Setenv("ENV_OPS_TOKEN", "secret")
	called := false
	h := AuthMiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/api/alert/datasources", nil)
	req.AddCookie(&http.Cookie{Name: "atoken", Value: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("原始 ENV_OPS_TOKEN 不应作为 cookie 放行: code=%d called=%v", rec.Code, called)
	}
}

func TestAuthHandlerSetsSignedHttpOnlyCookie(t *testing.T) {
	os.Setenv("ENV_OPS_TOKEN", "secret")
	req := httptest.NewRequest("POST", "/api/auth", nil)
	req.Header.Set("Authorization", "secret")
	rec := httptest.NewRecorder()

	AuthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("认证成功应返回 200: code=%d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("应设置一个 cookie: got=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "atoken" {
		t.Fatalf("cookie 名称错误: %s", cookie.Name)
	}
	if cookie.Value == "secret" {
		t.Fatalf("cookie 不应保存明文部署 Token")
	}
	if !cookie.HttpOnly {
		t.Fatalf("cookie 应为 HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie SameSite 应为 Lax")
	}
	if !validAuthSession(cookie.Value, "secret", time.Now()) {
		t.Fatalf("cookie 应为有效签名会话")
	}
}

func TestLogoutHandlerClearsAuthCookie(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/logout", nil)
	rec := httptest.NewRecorder()

	LogoutHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout should return 200: code=%d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("logout should set one cookie: got=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "atoken" {
		t.Fatalf("cookie name mismatch: %s", cookie.Name)
	}
	if cookie.Value != "" {
		t.Fatalf("logout cookie value should be empty")
	}
	if cookie.MaxAge >= 0 {
		t.Fatalf("logout cookie should be expired with negative MaxAge: %d", cookie.MaxAge)
	}
	if !cookie.HttpOnly {
		t.Fatalf("logout cookie should keep HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("logout cookie SameSite should be Lax")
	}
}
