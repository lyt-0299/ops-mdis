package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindAlertRule = "alertRule"

// --------------------------------------------------------------------------
// 内部帮助函数
// --------------------------------------------------------------------------

// resolveAlertRuleRelations 为单条规则查询关联数据源与 Webhook（不含实例统计）。
func resolveAlertRuleRelations(r *http.Request, rule *model.AlertRule) {
	ctx := r.Context()
	if rule.DataSourceID != "" {
		var ds model.DataSource
		if err := database.Config().FindOne(ctx, bson.M{"_id": rule.DataSourceID, "kind": kindDataSource}).Decode(&ds); err == nil {
			clearSecrets(&ds)
			rule.DataSource = ds
		}
	}
	if rule.WebhookID != "" {
		var wh model.Webhook
		if err := database.Config().FindOne(ctx, bson.M{"_id": rule.WebhookID, "kind": kindWebhook}).Decode(&wh); err == nil {
			clearWebhookSecrets(&wh)
			rule.Webhook = &wh
		}
	}
}

// --------------------------------------------------------------------------
// CRUD
// --------------------------------------------------------------------------

// ListAlertRules GET /api/alert/alert-rules
// 支持可选的 ?category= 过滤。
func ListAlertRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := bson.M{"kind": kindAlertRule}
	if cat := r.URL.Query().Get("category"); cat != "" {
		filter["category"] = cat
	}

	cur, err := database.Config().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var rules []model.AlertRule
	if err := cur.All(ctx, &rules); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	// 批量收集 dataSourceID / webhookID，一次性查回来
	dsIDs := make([]string, 0)
	whIDs := make([]string, 0)
	for _, rule := range rules {
		if rule.DataSourceID != "" {
			dsIDs = append(dsIDs, rule.DataSourceID)
		}
		if rule.WebhookID != "" {
			whIDs = append(whIDs, rule.WebhookID)
		}
	}

	dsMap := make(map[string]model.DataSource)
	if len(dsIDs) > 0 {
		dsCur, err := database.Config().Find(ctx, bson.M{"kind": kindDataSource, "_id": bson.M{"$in": dsIDs}})
		if err == nil {
			var dsList []model.DataSource
			_ = dsCur.All(ctx, &dsList)
			dsCur.Close(ctx)
			for _, ds := range dsList {
				clearSecrets(&ds)
				dsMap[ds.ID] = ds
			}
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

	out := make([]model.AlertRule, 0, len(rules))
	for i := range rules {
		if ds, ok := dsMap[rules[i].DataSourceID]; ok {
			rules[i].DataSource = ds
		}
		if wh, ok := whMap[rules[i].WebhookID]; ok {
			copy := wh
			rules[i].Webhook = &copy
		}
		// 实例统计（忽略错误，字段默认零值）
		total, firing, mutedFiring, _ := service.CountPrometheusInstanceStates(rules[i].ID)
		rules[i].InstanceTotal = total
		rules[i].InstanceFiring = firing
		rules[i].InstanceMutedFiring = mutedFiring
		out = append(out, rules[i])
	}

	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateAlertRule POST /api/alert/alert-rules
func CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule model.AlertRule
	if err := httputil.ReadJSON(r, &rule); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// custom 分类无指标类型概念（前端约定送空串，PromQL 由用户自填）
	if rule.Name == "" || rule.DataSourceID == "" || rule.Category == "" ||
		(rule.Category != "custom" && rule.MetricType == "") {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_rule_fields_required")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindAlertRule, "name": rule.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_task_name_taken")
		return
	}

	// 验证数据源存在
	dsCount, err := database.Config().CountDocuments(ctx, bson.M{"_id": rule.DataSourceID, "kind": kindDataSource})
	if err != nil || dsCount == 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_datasource_not_found")
		return
	}

	// 生成或校验 PromQL
	if rule.PromQL == "" {
		if rule.Category == "custom" {
			httputil.WriteErr(w, http.StatusBadRequest, "alert_custom_promql_required")
			return
		}
		if promQL, ok := model.GeneratePromQL(rule.Category, rule.MetricType, rule.Filters); ok {
			rule.PromQL = promQL
		} else {
			httputil.WriteErr(w, http.StatusBadRequest, "alert_unknown_metric_type")
			return
		}
	} else {
		if err := validatePromQL(rule.PromQL); err != nil {
			httputil.WriteErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// 运算符校验
	validOps := map[string]bool{">": true, "<": true, ">=": true, "<=": true, "==": true, "!=": true}
	if !validOps[rule.Operator] {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_operator")
		return
	}

	// 默认值
	if rule.Interval == 0 {
		rule.Interval = 60
	}
	if rule.Duration == 0 {
		rule.Duration = 60
	}
	if rule.RecoveryDuration == 0 {
		rule.RecoveryDuration = 60
	}
	if rule.Status == "" {
		rule.Status = model.TaskStatusActive
	}
	rule.AlertState = model.AlertStateOK
	rule.ID = bson.NewObjectID().Hex()
	rule.Kind = kindAlertRule
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	if _, err := database.Config().InsertOne(ctx, rule); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	resolveAlertRuleRelations(r, &rule)
	httputil.WriteJSON(w, http.StatusOK, rule)
}

// UpdateAlertRule PUT /api/alert/alert-rules/{id}
func UpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindAlertRule}

	var existing model.AlertRule
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	var updateData model.AlertRule
	if err := httputil.ReadJSON(r, &updateData); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// 名称唯一性校验（排除自身）
	if updateData.Name != "" && updateData.Name != existing.Name {
		count, err := database.Config().CountDocuments(ctx, bson.M{
			"kind": kindAlertRule,
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

	// PromQL 再生成逻辑（镜像上游行为）
	shouldRegeneratePromQL := false
	if updateData.PromQL == "" {
		shouldRegeneratePromQL = true
	} else {
		targetCategory := existing.Category
		if updateData.Category != "" {
			targetCategory = updateData.Category
		}
		if targetCategory != "custom" {
			if (updateData.Category != "" && updateData.Category != existing.Category) ||
				(updateData.MetricType != "" && updateData.MetricType != existing.MetricType) ||
				(updateData.Filters != nil) {
				shouldRegeneratePromQL = true
			}
		}
	}

	if shouldRegeneratePromQL {
		category := existing.Category
		if updateData.Category != "" {
			category = updateData.Category
		}
		metricType := existing.MetricType
		if updateData.MetricType != "" {
			metricType = updateData.MetricType
		}
		filters := existing.Filters
		if updateData.Filters != nil {
			filters = updateData.Filters
		}
		if category != "custom" {
			if promQL, ok := model.GeneratePromQL(category, metricType, filters); ok {
				updateData.PromQL = promQL
			}
		}
	}

	if updateData.PromQL != "" {
		if err := validatePromQL(updateData.PromQL); err != nil {
			httputil.WriteErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// 从暂停恢复为活跃时重置告警状态
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
	if updateData.DataSourceID != "" {
		existing.DataSourceID = updateData.DataSourceID
	}
	if updateData.Category != "" {
		existing.Category = updateData.Category
	}
	if updateData.MetricType != "" {
		existing.MetricType = updateData.MetricType
	}
	if updateData.PromQL != "" {
		existing.PromQL = updateData.PromQL
	}
	if updateData.Operator != "" {
		existing.Operator = updateData.Operator
	}
	existing.Threshold = updateData.Threshold
	if updateData.Duration != 0 {
		existing.Duration = updateData.Duration
	}
	if updateData.RecoveryDuration != 0 {
		existing.RecoveryDuration = updateData.RecoveryDuration
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
	existing.RepeatAlert = updateData.RepeatAlert
	existing.RemindInterval = updateData.RemindInterval
	if updateData.Filters != nil {
		existing.Filters = updateData.Filters
	}

	normalizeAlertRulePauseFields(&existing, previousStatus)

	existing.UpdatedAt = time.Now()

	setDoc := bson.M{
		"name":             existing.Name,
		"dataSourceId":     existing.DataSourceID,
		"category":         existing.Category,
		"metricType":       existing.MetricType,
		"promql":           existing.PromQL,
		"operator":         existing.Operator,
		"threshold":        existing.Threshold,
		"duration":         existing.Duration,
		"recoveryDuration": existing.RecoveryDuration,
		"interval":         existing.Interval,
		"alertNote":        existing.AlertNote,
		"remark":           existing.Remark,
		"webhookId":        existing.WebhookID,
		"repeatAlert":      existing.RepeatAlert,
		"remindInterval":   existing.RemindInterval,
		"filters":          existing.Filters,
		"updatedAt":        existing.UpdatedAt,
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	resolveAlertRuleRelations(r, &existing)
	httputil.WriteJSON(w, http.StatusOK, existing)
}

// DeleteAlertRule DELETE /api/alert/alert-rules/{id}
// 同步删除关联的告警历史、静默记录和实例状态。
func DeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 验证规则存在
	count, err := database.Config().CountDocuments(ctx, bson.M{"_id": id, "kind": kindAlertRule})
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
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	// 删除静默记录
	if _, err := database.Config().DeleteMany(ctx, bson.M{
		"kind":       "silence",
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	// 删除实例状态
	if _, err := database.State().DeleteMany(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	// 删除规则本体
	if _, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindAlertRule}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// --------------------------------------------------------------------------
// 实例明细 & 静默
// --------------------------------------------------------------------------

// ListAlertRuleInstances GET /api/alert/alert-rules/{id}/instances
func ListAlertRuleInstances(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	instances, err := service.ListPrometheusInstances(id)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_load_instance_detail_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": instances})
}

// CreateAlertRuleSilences POST /api/alert/alert-rules/{id}/silences
// 为 Prometheus 规则的指定实例批量创建/更新静默。
func CreateAlertRuleSilences(w http.ResponseWriter, r *http.Request) {
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

	silences, err := service.CreatePrometheusSilences(id, req)
	if err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": silences})
}

// CancelAlertRuleSilence DELETE /api/alert/alert-rules/{id}/silences?scope_key=...
// 取消单个实例的静默。
func CancelAlertRuleSilence(w http.ResponseWriter, r *http.Request) {
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

	if err := service.CancelPrometheusSilence(id, scopeKey); err != nil {
		writePauseErr(w, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_silence_cancelled"})
}

// --------------------------------------------------------------------------
// 刷新（手动触发检查）
// --------------------------------------------------------------------------

// RefreshAlertRule POST /api/alert/alert-rules/{id}/refresh
func RefreshAlertRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	var rule model.AlertRule
	if err := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindAlertRule}).Decode(&rule); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	// 尝试手动触发调度器执行，与定时任务共享 runningRules 互斥保护
	scheduler := service.GetAlertScheduler()
	if scheduler != nil {
		// 分开报：此前任何错误都写成 409 alert_rule_checking，前端一律显示
		// 「规则正在检查中，请稍后重试」——而真实原因往往是数据源缺失或查询失败，
		// 用户照着提示反复重试永远不会成功。
		if err := scheduler.TryCheckRule(&rule); err != nil {
			switch {
			case errors.Is(err, service.ErrAlertRuleRunning):
				httputil.WriteErr(w, http.StatusConflict, "alert_rule_checking")
			case errors.Is(err, service.ErrAlertRuleDataSourceMissing):
				httputil.WriteErr(w, http.StatusBadRequest, "alert_rule_datasource_missing")
			default:
				httputil.WriteErr(w, http.StatusInternalServerError, "common_refresh_failed", err.Error())
			}
			return
		}
	}

	// 重新加载更新后的状态
	_ = database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindAlertRule}).Decode(&rule)

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"key":         "common_refreshed",
		"last_value":  rule.LastValue,
		"alert_state": rule.AlertState,
	})
}

// --------------------------------------------------------------------------
// 告警历史
// --------------------------------------------------------------------------

// GetAlertRuleHistory GET /api/alert/alert-rules/{id}/alert-history
func GetAlertRuleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypePrometheus, id)
}

// --------------------------------------------------------------------------
// 指标模板（只读，来自 model 内置定义）
// --------------------------------------------------------------------------

// GetMetricTemplates GET /api/alert/metric-templates
func GetMetricTemplates(w http.ResponseWriter, r *http.Request) {
	type metricTemplateOption struct {
		Type   string `json:"type"`
		PromQL string `json:"promql"`
		Unit   string `json:"unit"`
	}
	type metricCategoryOption struct {
		Category  string                 `json:"category"`
		Templates []metricTemplateOption `json:"templates"`
	}

	categories := model.GetMetricCategories()
	result := make([]metricCategoryOption, 0, len(categories))
	for _, cat := range categories {
		item := metricCategoryOption{
			Category:  cat.Category,
			Templates: make([]metricTemplateOption, 0, len(cat.Templates)),
		}
		for _, tpl := range cat.Templates {
			item.Templates = append(item.Templates, metricTemplateOption{
				Type:   tpl.Type,
				PromQL: tpl.PromQL,
				Unit:   tpl.Unit,
			})
		}
		result = append(result, item)
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

// --------------------------------------------------------------------------
// 内部工具
// --------------------------------------------------------------------------

// validatePromQL 校验 PromQL 表达式，防止注入和超长。
func validatePromQL(promQL string) error {
	const maxLen = 10000
	if len(promQL) > maxLen {
		return fmt.Errorf("alert_promql_too_long")
	}
	for _, ch := range promQL {
		if ch < 32 && ch != '\t' && ch != '\n' && ch != '\r' {
			return fmt.Errorf("alert_promql_illegal_control_char")
		}
	}
	inDQ, inSQ, inBT := false, false, false
	for i := 0; i < len(promQL); i++ {
		c := promQL[i]
		if inBT {
			if c == '`' {
				inBT = false
			}
			continue
		}
		if inSQ {
			if c == '\\' {
				i++
				continue
			}
			if c == '\'' {
				inSQ = false
			}
			continue
		}
		if inDQ {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inDQ = false
			}
			continue
		}
		switch c {
		case '"':
			inDQ = true
		case '\'':
			inSQ = true
		case '`':
			inBT = true
		case '#':
			return fmt.Errorf("alert_promql_illegal_comment")
		case '/':
			if i+1 < len(promQL) && promQL[i+1] == '*' {
				return fmt.Errorf("alert_promql_illegal_comment")
			}
		case '*':
			if i+1 < len(promQL) && promQL[i+1] == '/' {
				return fmt.Errorf("alert_promql_illegal_comment")
			}
		}
	}
	return nil
}
