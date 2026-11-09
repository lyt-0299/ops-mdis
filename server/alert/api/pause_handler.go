package api

import (
	"errors"
	"net/http"
	"time"

	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

// pauseRequest 暂停请求体
type pauseRequest struct {
	Mode        string     `json:"mode"`
	PausedUntil *time.Time `json:"pausedUntil"`
	Reason      string     `json:"reason"`
}

// --- Kafka 任务 ---

func PauseTask(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypeKafka)
}

func ResumeTask(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypeKafka)
}

// --- Prometheus 告警规则 ---

func PauseAlertRule(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypePrometheus)
}

func ResumeAlertRule(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypePrometheus)
}

// --- 端口连通性监控 ---

func PausePortMonitor(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypePortMonitor)
}

func ResumePortMonitor(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypePortMonitor)
}

// --- SSL 证书监控 ---

func PauseSslMonitor(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypeSslMonitor)
}

func ResumeSslMonitor(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypeSslMonitor)
}

// --- MongoDB 监控 ---

func PauseMongoMonitor(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypeMongoMonitor)
}

func ResumeMongoMonitor(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypeMongoMonitor)
}

// --- MySQL 监控 ---

func PauseMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypeMySQLMonitor)
}

func ResumeMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypeMySQLMonitor)
}

// --- Kafka 重平衡监控 ---

func PauseKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	pauseMonitor(w, r, model.SourceTypeKafkaRebalanceMonitor)
}

func ResumeKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	resumeMonitor(w, r, model.SourceTypeKafkaRebalanceMonitor)
}

// --- 内部公共实现 ---

func pauseMonitor(w http.ResponseWriter, r *http.Request, sourceType string) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	var req pauseRequest
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if err := service.PauseMonitor(sourceType, id, req.Mode, req.PausedUntil, req.Reason); err != nil {
		writePauseErr(w, err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_paused"})
}

func resumeMonitor(w http.ResponseWriter, r *http.Request, sourceType string) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	if err := service.ResumeMonitor(sourceType, id); err != nil {
		writePauseErr(w, err)
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_resumed"})
}

func writePauseErr(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrPauseTargetNotFound) {
		httputil.WriteErr(w, http.StatusNotFound, "alert_task_not_found")
		return
	}
	httputil.WriteErr(w, http.StatusBadRequest, err.Error())
}

// --------------------------------------------------------------------------
// 暂停字段规范化辅助函数（供各 domain handler 调用）
// --------------------------------------------------------------------------

// normalizeAlertRulePauseFields 在写入数据库前规范化 AlertRule 的暂停相关字段。
func normalizeAlertRulePauseFields(rule *model.AlertRule, previousStatus string) {
	if rule.Status == model.TaskStatusActive {
		rule.PauseMode = ""
		rule.PausedUntil = nil
		rule.PausedAt = nil
		rule.PauseReason = ""
		if previousStatus == model.TaskStatusPaused {
			rule.AlertState = model.AlertStateOK
			rule.FiringAt = nil
			rule.RecoveredAt = nil
		}
		return
	}
	if rule.Status == model.TaskStatusPaused {
		normalizePausedTimeFields(&rule.PauseMode, &rule.PausedUntil, &rule.PausedAt)
	}
}

// normalizePortPauseFields 在写入数据库前规范化 PortMonitorRule 的暂停相关字段。
func normalizePortPauseFields(rule *model.PortMonitorRule, previousStatus string) {
	if rule.Status == model.TaskStatusActive {
		rule.PauseMode = ""
		rule.PausedUntil = nil
		rule.PausedAt = nil
		rule.PauseReason = ""
		if previousStatus == model.TaskStatusPaused {
			rule.AlertState = model.AlertStateOK
			rule.FiringAt = nil
			rule.RecoveredAt = nil
		}
		return
	}
	if rule.Status == model.TaskStatusPaused {
		normalizePausedTimeFields(&rule.PauseMode, &rule.PausedUntil, &rule.PausedAt)
	}
}

// normalizeSslPauseFields 在写入数据库前规范化 SslMonitorRule 的暂停相关字段。
func normalizeSslPauseFields(rule *model.SslMonitorRule, previousStatus string) {
	if rule.Status == model.TaskStatusActive {
		rule.PauseMode = ""
		rule.PausedUntil = nil
		rule.PausedAt = nil
		rule.PauseReason = ""
		if previousStatus == model.TaskStatusPaused {
			rule.AlertState = model.AlertStateOK
			rule.FiringAt = nil
			rule.RecoveredAt = nil
		}
		return
	}
	if rule.Status == model.TaskStatusPaused {
		normalizePausedTimeFields(&rule.PauseMode, &rule.PausedUntil, &rule.PausedAt)
	}
}

// normalizePausedTimeFields 设置暂停模式默认值并在手动模式时清空到期时间。
func normalizePausedTimeFields(mode *string, pausedUntil **time.Time, pausedAt **time.Time) {
	if *mode == "" {
		*mode = model.PauseModeManual
	}
	if *mode == model.PauseModeManual {
		*pausedUntil = nil
	}
	if *pausedAt == nil {
		now := time.Now()
		*pausedAt = &now
	}
}
