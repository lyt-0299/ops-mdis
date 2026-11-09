package api

import (
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindSslRule = "sslRule"

// resolveSslRuleWebhook 注入关联 Webhook（清除敏感字段）。
func resolveSslRuleWebhook(r *http.Request, rule *model.SslMonitorRule) {
	if rule.WebhookID == "" {
		return
	}
	ctx := r.Context()
	var wh model.Webhook
	if err := database.Config().FindOne(ctx, bson.M{"_id": rule.WebhookID, "kind": kindWebhook}).Decode(&wh); err == nil {
		clearWebhookSecrets(&wh)
		rule.Webhook = &wh
	}
}

// ListSslMonitors GET /api/alert/ssl-monitors
func ListSslMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindSslRule},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var rules []model.SslMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	// 批量查 Webhook
	whIDs := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.WebhookID != "" {
			whIDs = append(whIDs, rule.WebhookID)
		}
	}
	whMap := make(map[string]model.Webhook)
	if len(whIDs) > 0 {
		whCur, err := database.Config().Find(ctx, bson.M{"kind": kindWebhook, "_id": bson.M{"$in": whIDs}})
		if err == nil {
			var whList []model.Webhook
			_ = whCur.All(ctx, &whList)
			whCur.Close(ctx)
			for _, wh := range whList {
				clearWebhookSecrets(&wh)
				whMap[wh.ID] = wh
			}
		}
	}

	out := make([]model.SslMonitorRule, 0, len(rules))
	for i := range rules {
		if wh, ok := whMap[rules[i].WebhookID]; ok {
			copy := wh
			rules[i].Webhook = &copy
		}
		out = append(out, rules[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateSslMonitor POST /api/alert/ssl-monitors
func CreateSslMonitor(w http.ResponseWriter, r *http.Request) {
	var rule model.SslMonitorRule
	if err := httputil.ReadJSON(r, &rule); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if rule.Name == "" || rule.Domain == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_ssl_fields_required")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindSslRule, "name": rule.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_task_name_taken")
		return
	}

	// 默认值
	if rule.Port <= 0 {
		rule.Port = 443
	}
	if rule.Timeout == 0 {
		rule.Timeout = 10
	}
	if rule.WarnDays == 0 {
		rule.WarnDays = 14
	}
	if rule.Interval == 0 {
		rule.Interval = 86400
	}
	if rule.Status == "" {
		rule.Status = model.TaskStatusActive
	}
	rule.AlertState = model.AlertStateOK
	rule.ID = bson.NewObjectID().Hex()
	rule.Kind = kindSslRule
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	if _, err := database.Config().InsertOne(ctx, rule); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	resolveSslRuleWebhook(r, &rule)
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// UpdateSslMonitor PUT /api/alert/ssl-monitors/{id}
func UpdateSslMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindSslRule}

	var existing model.SslMonitorRule
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	var updateData model.SslMonitorRule
	if err := httputil.ReadJSON(r, &updateData); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// 名称唯一性校验（排除自身）
	if updateData.Name != "" && updateData.Name != existing.Name {
		count, err := database.Config().CountDocuments(ctx, bson.M{
			"kind": kindSslRule,
			"name": updateData.Name,
			"_id":  bson.M{"$ne": id},
		})
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
			return
		}
		if count > 0 {
			httputil.WriteErr(w, http.StatusConflict, "alert_task_name_taken")
			return
		}
	}

	previousStatus := existing.Status
	if updateData.Status == model.TaskStatusActive && existing.Status == model.TaskStatusPaused {
		existing.AlertState = model.AlertStateOK
		existing.FiringAt = nil
		existing.RecoveredAt = nil
	}

	// 合并字段
	if updateData.Name != "" {
		existing.Name = updateData.Name
	}
	if updateData.Domain != "" {
		existing.Domain = updateData.Domain
	}
	if updateData.Port > 0 {
		existing.Port = updateData.Port
	}
	if updateData.Timeout != 0 {
		existing.Timeout = updateData.Timeout
	}
	if updateData.WarnDays != 0 {
		existing.WarnDays = updateData.WarnDays
	}
	if updateData.Interval != 0 {
		existing.Interval = updateData.Interval
	}
	existing.AlertNote = updateData.AlertNote
	existing.Remark = updateData.Remark
	existing.WebhookID = updateData.WebhookID
	if updateData.Status != "" {
		existing.Status = updateData.Status
	}
	existing.PauseMode = updateData.PauseMode
	existing.PausedUntil = updateData.PausedUntil
	existing.PausedAt = updateData.PausedAt
	existing.PauseReason = updateData.PauseReason
	existing.IgnoreSslErrors = updateData.IgnoreSslErrors
	existing.RepeatAlert = updateData.RepeatAlert
	existing.RemindInterval = updateData.RemindInterval

	normalizeSslPauseFields(&existing, previousStatus)

	existing.UpdatedAt = time.Now()

	setDoc := bson.M{
		"name":            existing.Name,
		"domain":          existing.Domain,
		"port":            existing.Port,
		"timeout":         existing.Timeout,
		"warnDays":        existing.WarnDays,
		"interval":        existing.Interval,
		"alertNote":       existing.AlertNote,
		"remark":          existing.Remark,
		"webhookId":       existing.WebhookID,
		"ignoreSslErrors": existing.IgnoreSslErrors,
		"repeatAlert":     existing.RepeatAlert,
		"remindInterval":  existing.RemindInterval,
		"updatedAt":       existing.UpdatedAt,
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	resolveSslRuleWebhook(r, &existing)
	httputil.WriteJSON(w, http.StatusOK, existing)
}

// DeleteSslMonitor DELETE /api/alert/ssl-monitors/{id}
// 同步删除关联告警历史。
func DeleteSslMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	count, err := database.Config().CountDocuments(ctx, bson.M{"_id": id, "kind": kindSslRule})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	if count == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	// 删除告警历史
	if _, err := database.History().DeleteMany(ctx, bson.M{
		"sourceType": model.SourceTypeSslMonitor,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	// 删除规则本体
	if _, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindSslRule}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// RefreshSslMonitor POST /api/alert/ssl-monitors/{id}/refresh
func RefreshSslMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	var rule model.SslMonitorRule
	if err := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindSslRule}).Decode(&rule); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	scheduler := service.GetSslMonitorScheduler()
	if scheduler != nil {
		if err := scheduler.TryCheckRule(&rule); err != nil {
			// 「正在检查中」是可重试的，其它错误不是——分开报，否则用户只会反复点
			if strings.Contains(err.Error(), "already running") {
				httputil.WriteErr(w, http.StatusConflict, "alert_rule_checking")
				return
			}
			httputil.WriteErr(w, http.StatusInternalServerError, "common_refresh_failed", err.Error())
			return
		}
	}

	// 重新加载更新后的状态
	_ = database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindSslRule}).Decode(&rule)

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"key":         "common_refreshed",
		"alert_state": rule.AlertState,
		"days_left":   rule.DaysLeft,
		"cert_expiry": rule.CertExpiry,
	})
}

// GetSslMonitorHistory GET /api/alert/ssl-monitors/{id}/alert-history
func GetSslMonitorHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypeSslMonitor, id)
}
