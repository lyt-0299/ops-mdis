package api

import (
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindMongoRule = "mongoRule"

// resolveMongoRuleAssociations 批量注入 MongoInstance 和 Webhook 关联（清除敏感字段）。
func resolveMongoRuleAssociations(r *http.Request, rules []model.MongoMonitorRule) []model.MongoMonitorRule {
	// 收集需要查询的 ID
	instIDs := make([]string, 0)
	webhookIDs := make([]string, 0)
	instIDSet := make(map[string]struct{})
	webhookIDSet := make(map[string]struct{})
	for _, ru := range rules {
		if ru.MongoInstanceID != "" {
			if _, ok := instIDSet[ru.MongoInstanceID]; !ok {
				instIDSet[ru.MongoInstanceID] = struct{}{}
				instIDs = append(instIDs, ru.MongoInstanceID)
			}
		}
		if ru.WebhookID != "" {
			if _, ok := webhookIDSet[ru.WebhookID]; !ok {
				webhookIDSet[ru.WebhookID] = struct{}{}
				webhookIDs = append(webhookIDs, ru.WebhookID)
			}
		}
	}

	reqCtx := r.Context()

	// 查询 MongoInstance
	instMap := make(map[string]model.MongoInstance)
	if len(instIDs) > 0 {
		iCur, err := database.Config().Find(reqCtx, bson.M{"kind": kindMongoInstance, "_id": bson.M{"$in": instIDs}})
		if err == nil {
			var insts []model.MongoInstance
			_ = iCur.All(reqCtx, &insts)
			iCur.Close(reqCtx)
			for _, inst := range insts {
				inst.Sanitize()
				instMap[inst.ID] = inst
			}
		}
	}

	// 查询 Webhook
	whMap := make(map[string]model.Webhook)
	if len(webhookIDs) > 0 {
		wCur, err := database.Config().Find(reqCtx, bson.M{"kind": kindWebhook, "_id": bson.M{"$in": webhookIDs}})
		if err == nil {
			var whs []model.Webhook
			_ = wCur.All(reqCtx, &whs)
			wCur.Close(reqCtx)
			for _, wh := range whs {
				clearWebhookSecrets(&wh)
				whMap[wh.ID] = wh
			}
		}
	}

	for i := range rules {
		if inst, ok := instMap[rules[i].MongoInstanceID]; ok {
			rules[i].MongoInstance = inst
		}
		if wh, ok := whMap[rules[i].WebhookID]; ok {
			cp := wh
			rules[i].Webhook = &cp
		}
	}
	return rules
}

// resolveMongoRuleByID 按 ID 查询单条规则并注入关联。
func resolveMongoRuleByID(r *http.Request, id string) (model.MongoMonitorRule, error) {
	var rule model.MongoMonitorRule
	if err := database.Config().FindOne(r.Context(), bson.M{"_id": id, "kind": kindMongoRule}).Decode(&rule); err != nil {
		return rule, err
	}
	rules := resolveMongoRuleAssociations(r, []model.MongoMonitorRule{rule})
	return rules[0], nil
}

// ListMongoMonitors GET /api/alert/mongo-monitors
func ListMongoMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindMongoRule},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var rules []model.MongoMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	rules = resolveMongoRuleAssociations(r, rules)
	if rules == nil {
		rules = []model.MongoMonitorRule{} // 空列表返回 [] 而非 null(前端概览/列表依赖)
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

// CreateMongoMonitor POST /api/alert/mongo-monitors
func CreateMongoMonitor(w http.ResponseWriter, r *http.Request) {
	var req model.MongoMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_rule_name_required")
		return
	}
	if req.MongoInstanceID == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mongo_instance_required")
		return
	}

	// 验证监控类型
	validTypes := map[string]bool{"replica_status": true, "oplog_lag": true, "oplog_window": true}
	if !validTypes[req.MonitorType] {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_monitor_type")
		return
	}

	ctx := r.Context()

	// 验证 MongoDB 实例存在且已启用「告警」用途(与数据源页能力门控一致):
	// 单节点等未勾 alert 的实例不可配告警,否则 oplog 取不到值会误报。
	var inst model.MongoInstance
	if err := database.Config().FindOne(ctx, bson.M{"kind": kindMongoInstance, "_id": req.MongoInstanceID}).Decode(&inst); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mongo_instance_not_found")
		return
	}
	hasAlert := false
	for _, p := range inst.Purposes {
		if p == "alert" {
			hasAlert = true
			break
		}
	}
	if !hasAlert {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mongo_alert_purpose_required")
		return
	}

	// oplog_lag / oplog_window 类型必须提供阈值
	if req.MonitorType != "replica_status" && req.Threshold <= 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_threshold_positive")
		return
	}

	// 设置默认值
	if req.Interval <= 0 {
		req.Interval = 60
	}
	if req.Duration <= 0 {
		req.Duration = 60
	}
	if req.RecoveryDuration <= 0 {
		req.RecoveryDuration = 60
	}
	if req.Operator == "" {
		if req.MonitorType == "oplog_window" {
			req.Operator = "<"
		} else {
			req.Operator = ">"
		}
	}
	if req.Status == "" {
		req.Status = model.TaskStatusActive
	}
	req.AlertState = model.AlertStateOK

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindMongoRule
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	// 重新加载带关联的完整记录
	rule, err := resolveMongoRuleByID(r, req.ID)
	if err != nil {
		httputil.WriteJSON(w, http.StatusOK, req)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// UpdateMongoMonitor PUT /api/alert/mongo-monitors/{id}
func UpdateMongoMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindMongoRule}

	if err := database.Config().FindOne(ctx, filter).Err(); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	var req model.MongoMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// 验证监控类型
	if req.MonitorType != "" {
		validTypes := map[string]bool{"replica_status": true, "oplog_lag": true, "oplog_window": true}
		if !validTypes[req.MonitorType] {
			httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_monitor_type")
			return
		}
	}

	setDoc := bson.M{
		"name":             req.Name,
		"mongoInstanceId":  req.MongoInstanceID,
		"monitorType":      req.MonitorType,
		"threshold":        req.Threshold,
		"operator":         req.Operator,
		"duration":         req.Duration,
		"recoveryDuration": req.RecoveryDuration,
		"interval":         req.Interval,
		"alertNote":        req.AlertNote,
		"remark":           req.Remark,
		"webhookId":        req.WebhookID,
		"repeatAlert":      req.RepeatAlert,
		"remindInterval":   req.RemindInterval,
		"updatedAt":        time.Now(),
	}

	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	rule, err := resolveMongoRuleByID(r, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// DeleteMongoMonitor DELETE /api/alert/mongo-monitors/{id}
func DeleteMongoMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 同步删除关联的告警历史、静默、实例状态
	sourceFilter := bson.M{"sourceType": model.SourceTypeMongoMonitor, "sourceId": id}
	if _, err := database.History().DeleteMany(ctx, sourceFilter); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	// 删除静默记录（存储在 Config 集合）
	database.Config().DeleteMany(ctx, bson.M{"kind": "alertSilence", "sourceType": model.SourceTypeMongoMonitor, "sourceId": id}) //nolint:errcheck
	// 删除实例状态（存储在 State 集合）
	database.State().DeleteMany(ctx, sourceFilter) //nolint:errcheck

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindMongoRule})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// RefreshMongoMonitor POST /api/alert/mongo-monitors/{id}/refresh
func RefreshMongoMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	var rule model.MongoMonitorRule
	if err := database.Config().FindOne(r.Context(), bson.M{"_id": id, "kind": kindMongoRule}).Decode(&rule); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	// 注入关联（调度器 TryCheckRule 需要 MongoInstance 已加载）
	rules := resolveMongoRuleAssociations(r, []model.MongoMonitorRule{rule})
	rule = rules[0]

	// TryCheckRule 返回 false 表示规则正在运行中
	if !service.GetMongoMonitorScheduler().TryCheckRule(&rule) {
		httputil.WriteErr(w, http.StatusConflict, "alert_rule_checking")
		return
	}

	// 重新加载最新状态
	updated, err := resolveMongoRuleByID(r, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_refreshed_but_load_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// GetMongoMonitorHistory GET /api/alert/mongo-monitors/{id}/alert-history
func GetMongoMonitorHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypeMongoMonitor, id)
}

// ListMongoMonitorInstances GET /api/alert/mongo-monitors/{id}/instances
func ListMongoMonitorInstances(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	instances, err := service.ListMonitorInstances(model.SourceTypeMongoMonitor, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_load_node_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": instances})
}

// CreateMongoMonitorSilences POST /api/alert/mongo-monitors/{id}/silences
func CreateMongoMonitorSilences(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	var req service.AlertSilenceRequest
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	silences, err := service.CreateMonitorSilences(model.SourceTypeMongoMonitor, id, req)
	if err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": silences})
}

// CancelMongoMonitorSilence DELETE /api/alert/mongo-monitors/{id}/silences
func CancelMongoMonitorSilence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	scopeKey := r.URL.Query().Get("scopeKey")
	if scopeKey == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_silence_target_required")
		return
	}
	if err := service.CancelMonitorSilence(model.SourceTypeMongoMonitor, id, scopeKey); err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_silence_cancelled"})
}
