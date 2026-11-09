package common

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const authCookieName = "atoken"
const authSessionTTL = 12 * time.Hour

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func signAuthSession(nonce string, expires int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("%s.%d", nonce, expires)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newAuthSession(secret string, now time.Time) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)
	expires := now.Add(authSessionTTL).Unix()
	return fmt.Sprintf("%s.%d.%s", nonce, expires, signAuthSession(nonce, expires, secret)), nil
}

func validAuthSession(value string, secret string, now time.Time) bool {
	if value == "" || secret == "" {
		return false
	}

	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}

	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > expires {
		return false
	}

	expected := signAuthSession(parts[0], expires, secret)
	return subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expected)) == 1
}

// 中间件函数，验证请求是否包含有效的会话 Cookie。
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authCookieName)
		if err != nil || !validAuthSession(cookie.Value, os.Getenv("ENV_OPS_TOKEN"), time.Now()) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// 验证部署 Token，校验通过后签发短期会话 Cookie。
func AuthHandler(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	envToken := os.Getenv("ENV_OPS_TOKEN")

	if token != envToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionValue, err := newAuthSession(envToken, time.Now())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    sessionValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(authSessionTTL),
		MaxAge:   int(authSessionTTL.Seconds()),
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "Authorized"})
}

// LogoutHandler clears the signed auth session cookie.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "LoggedOut"})
}

// SessionCheckHandler 仅供网关 nginx 的 auth_request 子请求使用：
// 真正的校验由外层 AuthMiddleware（验证签名会话 Cookie）完成，命中即返回 200。
// 网关各 iframe 代理（grafana/loki/tempo/alloy/...）的 /_auth 指向本端点，
// nginx 无需自行识别签名会话，统一交给后端校验。
func SessionCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// AuthMiddlewareHandler 与 AuthMiddleware 同语义，但包装 http.Handler，
// 用于挂载整棵子树(如 /api/alert/ 的子 ServeMux)。
func AuthMiddlewareHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(authCookieName)
		if err != nil || !validAuthSession(cookie.Value, os.Getenv("ENV_OPS_TOKEN"), time.Now()) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
