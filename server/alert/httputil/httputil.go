// Package httputil 提供告警 api 层统一的 JSON 读写助手(替代 gin 的 c.JSON / c.ShouldBindJSON)。
package httputil

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteErr 统一错误响应: {key, detail}。
//   status  HTTP 状态码
//   key     英文 snake 机器键,前端按 key 映射成中文再走 _l() 翻译(如 "alert_rule_not_found")。
//           ⚠️ 只发英文 key,绝不发中文/最终展示文案——文案归前端。
//   detail  可选底层兜底文本(动态/原始 err,前端原样附在文案后;通常传 err.Error())。
func WriteErr(w http.ResponseWriter, status int, key string, detail ...string) {
	msg := ""
	if len(detail) > 0 {
		msg = detail[0]
	}
	WriteJSON(w, status, map[string]any{"key": key, "detail": msg})
}

func ReadJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
