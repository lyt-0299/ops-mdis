package api

import (
	"errors"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindMySQLRule = "mysqlRule"

// resolveMySQLRuleAssociations 批量注入 MySQLInstance 和 Webhook 关联（清除敏感字段）。
func resolveMySQLRuleAssociations(r *http.Request, rules []model.MySQLMonitorRule) []model.MySQLMonitorRule {
	instIDs := make([]string, 0)
	webhookIDs := make([]string, 0)
	instIDSet := make(map[string]struct{})
	webhookIDSet := make(map[string]struct{})
	for _, ru := range rules {
		if ru.MySQLInstanceID != "" {
			if _, ok := instIDSet[ru.MySQLInstanceID]; !ok {
				instIDSet[ru.MySQLInstanceID] = struct{}{}
				instIDs = append(instIDs, ru.MySQLInstanceID)
			}
		}
		if ru.WebhookID != "" {
			if _, ok := webhookIDSet[ru.WebhookID]; !ok {
				webhookIDSet[ru.WebhookID] = struct{}{}
				webhookIDs = append(webhookIDs, ru.WebhookID)
			}
		}
	}

	ctx := r.Context()

	instMap := make(map[string]model.MySQLInstance)
	if len(instIDs) > 0 {
		iCur, err := database.Config().Find(ctx, bson.M{"kind": kindMySQLInstance, "_id": bson.M{"$in": instIDs}})
		if err == nil {
			var insts []model.MySQLInstance
			_ = iCur.All(ctx, &insts)
			iCur.Close(ctx)
			for _, inst := range insts {
				inst.Sanitize()
				instMap[inst.ID] = inst
			}
		}
	}

	whMap := make(map[string]model.Webhook)
	if len(webhookIDs) > 0 {
		wCur, err := database.Config().Find(ctx, bson.M{"kind": kindWebhook, "_id": bson.M{"$in": webhookIDs}})
		if err == nil {
			var whs []model.Webhook
			_ = wCur.All(ctx, &whs)
			wCur.Close(ctx)
			for _, wh := range whs {
				clearWebhookSecrets(&wh)
				whMap[wh.ID] = wh
			}
		}
	}

	for i := range rules {
		if inst, ok := instMap[rules[i].MySQLInstanceID]; ok {
			rules[i].MySQLInstance = inst
		}
		if wh, ok := whMap[rules[i].WebhookID]; ok {
			cp := wh
			rules[i].Webhook = &cp
		}
	}
	return rules
}

// resolveMySQLRuleByID 按 ID 查询单条规则并注入关联。
func resolveMySQLRuleByID(r *http.Request, id string) (model.MySQLMonitorRule, error) {
	var rule model.MySQLMonitorRule
	if err := database.Config().FindOne(r.Context(), bson.M{"_id": id, "kind": kindMySQLRule}).Decode(&rule); err != nil {
		return rule, err
	}
	rules := resolveMySQLRuleAssociations(r, []model.MySQLMonitorRule{rule})
	return rules[0], nil
}

// ListMySQLMonitors GET /api/alert/mysql-monitors
func ListMySQLMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindMySQLRule},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var rules []model.MySQLMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	rules = resolveMySQLRuleAssociations(r, rules)
	if rules == nil {
		rules = []model.MySQLMonitorRule{} // 空列表返回 [] 而非 null(前端概览/列表依赖)
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

// CreateMySQLMonitor POST /api/alert/mysql-monitors
func CreateMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	var req model.MySQLMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if req.Name == "" || req.MySQLInstanceID == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_rule_fields_required")
		return
	}

	ctx := r.Context()

	// 验证 MySQL 实例存在并获取架构类型
	var inst model.MySQLInstance
	if err := database.Config().FindOne(ctx, bson.M{"kind": kindMySQLInstance, "_id": req.MySQLInstanceID}).Decode(&inst); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_datasource_not_found")
		return
	}

	normalizeMySQLMonitorRuleForInstance(&req, inst.Architecture)
	if !isValidMySQLMonitorTypeForArchitecture(req.MonitorType, inst.Architecture) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_arch_unsupported_monitor")
		return
	}

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindMySQLRule
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	rule, err := resolveMySQLRuleByID(r, req.ID)
	if err != nil {
		httputil.WriteJSON(w, http.StatusOK, req)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// UpdateMySQLMonitor PUT /api/alert/mysql-monitors/{id}
func UpdateMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindMySQLRule}

	if err := database.Config().FindOne(ctx, filter).Err(); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	var req model.MySQLMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if req.Name == "" || req.MySQLInstanceID == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_rule_fields_required")
		return
	}

	var inst model.MySQLInstance
	if err := database.Config().FindOne(ctx, bson.M{"kind": kindMySQLInstance, "_id": req.MySQLInstanceID}).Decode(&inst); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_datasource_not_found")
		return
	}

	normalizeMySQLMonitorRuleForInstance(&req, inst.Architecture)
	if !isValidMySQLMonitorTypeForArchitecture(req.MonitorType, inst.Architecture) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_arch_unsupported_monitor")
		return
	}

	setDoc := bson.M{
		"name":                    req.Name,
		"mysqlInstanceId":         req.MySQLInstanceID,
		"monitorType":             req.MonitorType,
		"replicationLagThreshold": req.ReplicationLagThreshold,
		"mgrQueueThreshold":       req.MGRQueueThreshold,
		"duration":                req.Duration,
		"recoveryDuration":        req.RecoveryDuration,
		"interval":                req.Interval,
		"alertNote":               req.AlertNote,
		"remark":                  req.Remark,
		"webhookId":               req.WebhookID,
		"repeatAlert":             req.RepeatAlert,
		"remindInterval":          req.RemindInterval,
		"updatedAt":               time.Now(),
	}

	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	rule, err := resolveMySQLRuleByID(r, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// DeleteMySQLMonitor DELETE /api/alert/mysql-monitors/{id}
func DeleteMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 同步删除关联的告警历史、静默、实例状态
	sourceFilter := bson.M{"sourceType": model.SourceTypeMySQLMonitor, "sourceId": id}
	if _, err := database.History().DeleteMany(ctx, sourceFilter); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	database.Config().DeleteMany(ctx, bson.M{"kind": "alertSilence", "sourceType": model.SourceTypeMySQLMonitor, "sourceId": id}) //nolint:errcheck
	database.State().DeleteMany(ctx, sourceFilter)                                                                                //nolint:errcheck

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindMySQLRule})
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

// RefreshMySQLMonitor POST /api/alert/mysql-monitors/{id}/refresh
func RefreshMySQLMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	var rule model.MySQLMonitorRule
	if err := database.Config().FindOne(r.Context(), bson.M{"_id": id, "kind": kindMySQLRule}).Decode(&rule); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	// 注入关联（调度器需要 MySQLInstance 已加载）
	rules := resolveMySQLRuleAssociations(r, []model.MySQLMonitorRule{rule})
	rule = rules[0]

	if err := service.GetMySQLMonitorScheduler().TryCheckRule(&rule); err != nil {
		if errors.Is(err, service.ErrMySQLMonitorRuleRunning) {
			httputil.WriteErr(w, http.StatusConflict, "alert_rule_checking")
			return
		}
		httputil.WriteErr(w, http.StatusInternalServerError, "common_refresh_failed", err.Error())
		return
	}

	updated, err := resolveMySQLRuleByID(r, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_refreshed_but_load_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// GetMySQLMonitorHistory GET /api/alert/mysql-monitors/{id}/alert-history
func GetMySQLMonitorHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypeMySQLMonitor, id)
}

// ListMySQLMonitorInstances GET /api/alert/mysql-monitors/{id}/instances
func ListMySQLMonitorInstances(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	instances, err := service.ListMonitorInstances(model.SourceTypeMySQLMonitor, id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_load_node_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": instances})
}

// CreateMySQLMonitorSilences POST /api/alert/mysql-monitors/{id}/silences
func CreateMySQLMonitorSilences(w http.ResponseWriter, r *http.Request) {
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
	silences, err := service.CreateMonitorSilences(model.SourceTypeMySQLMonitor, id, req)
	if err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": silences})
}

// CancelMySQLMonitorSilence DELETE /api/alert/mysql-monitors/{id}/silences
func CancelMySQLMonitorSilence(w http.ResponseWriter, r *http.Request) {
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
	if err := service.CancelMonitorSilence(model.SourceTypeMySQLMonitor, id, scopeKey); err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_silence_cancelled"})
}

// normalizeMySQLMonitorRuleForInstance 按实例架构规范化规则字段默认值。
func normalizeMySQLMonitorRuleForInstance(rule *model.MySQLMonitorRule, architecture string) {
	if rule.MonitorType == "" || rule.MonitorType == model.MySQLMonitorTypeHealth || rule.MonitorType == model.MySQLMonitorTypeMGRStatus {
		rule.MonitorType = defaultMySQLMonitorTypeForArchitecture(architecture)
	}
	if rule.Interval <= 0 {
		rule.Interval = 60
	}
	if rule.Duration <= 0 {
		rule.Duration = 60
	}
	if rule.RecoveryDuration <= 0 {
		rule.RecoveryDuration = 60
	}
	if rule.ReplicationLagThreshold <= 0 {
		rule.ReplicationLagThreshold = 300
	}
	if rule.MGRQueueThreshold <= 0 {
		rule.MGRQueueThreshold = 100
	}
	if rule.Status == "" {
		rule.Status = model.TaskStatusActive
	}
	if rule.AlertState == "" {
		rule.AlertState = model.AlertStateOK
	}
}

// defaultMySQLMonitorTypeForArchitecture 返回给定架构的默认监控类型。
func defaultMySQLMonitorTypeForArchitecture(architecture string) string {
	switch architecture {
	case model.MySQLArchitectureReplication:
		return model.MySQLMonitorTypeReplicationLag
	case model.MySQLArchitectureMGR:
		return model.MySQLMonitorTypeMGRNodeStatus
	default:
		return model.MySQLMonitorTypeConnectivity
	}
}

// isValidMySQLMonitorTypeForArchitecture 校验监控类型与架构是否兼容。
func isValidMySQLMonitorTypeForArchitecture(monitorType, architecture string) bool {
	if monitorType == model.MySQLMonitorTypeConnectivity {
		return true
	}
	if architecture == model.MySQLArchitectureReplication &&
		(monitorType == model.MySQLMonitorTypeConnectivity ||
			monitorType == model.MySQLMonitorTypeReplicationLag ||
			monitorType == model.MySQLMonitorTypeReplicationStatus) {
		return true
	}
	if architecture == model.MySQLArchitectureMGR &&
		(monitorType == model.MySQLMonitorTypeMGRNodeStatus || monitorType == model.MySQLMonitorTypeMGRSyncStatus) {
		return true
	}
	return false
}
