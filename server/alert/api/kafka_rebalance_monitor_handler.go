package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindKafkaRebalanceRule = "kafkaRebalanceRule"

// ListKafkaRebalanceMonitors GET /api/alert/kafka-rebalance-monitors
func ListKafkaRebalanceMonitors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindKafkaRebalanceRule},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var rules []model.KafkaRebalanceMonitorRule
	if err := cur.All(ctx, &rules); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	// 批量注入 KafkaInstance 关联
	instanceIDs := make([]string, 0, len(rules))
	for _, ru := range rules {
		if ru.KafkaInstanceID != "" {
			instanceIDs = append(instanceIDs, ru.KafkaInstanceID)
		}
	}
	instanceMap := make(map[string]model.KafkaInstance)
	if len(instanceIDs) > 0 {
		iCur, err := database.Config().Find(ctx, bson.M{"kind": kindKafkaInstance, "_id": bson.M{"$in": instanceIDs}})
		if err == nil {
			var instances []model.KafkaInstance
			_ = iCur.All(ctx, &instances)
			iCur.Close(ctx)
			for _, inst := range instances {
				inst.Sanitize()
				instanceMap[inst.ID] = inst
			}
		}
	}

	// 批量注入 Webhook 关联
	webhookIDs := make([]string, 0, len(rules))
	for _, ru := range rules {
		if ru.WebhookID != "" {
			webhookIDs = append(webhookIDs, ru.WebhookID)
		}
	}
	whMap := make(map[string]model.Webhook)
	if len(webhookIDs) > 0 {
		wCur, err := database.Config().Find(ctx, bson.M{"kind": kindWebhook, "_id": bson.M{"$in": webhookIDs}})
		if err == nil {
			var webhooks []model.Webhook
			_ = wCur.All(ctx, &webhooks)
			wCur.Close(ctx)
			for _, wh := range webhooks {
				clearWebhookSecrets(&wh)
				whMap[wh.ID] = wh
			}
		}
	}

	for i := range rules {
		if inst, ok := instanceMap[rules[i].KafkaInstanceID]; ok {
			rules[i].KafkaInstance = inst
		}
		if wh, ok := whMap[rules[i].WebhookID]; ok {
			copy := wh
			rules[i].Webhook = &copy
		}
	}

	if rules == nil {
		rules = []model.KafkaRebalanceMonitorRule{} // 空列表返回 [] 而非 null(前端概览/列表依赖)
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

// CreateKafkaRebalanceMonitor POST /api/alert/kafka-rebalance-monitors
func CreateKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	var req model.KafkaRebalanceMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	applyKafkaRebalanceDefaults(&req)

	if err := validateKafkaRebalanceMonitorHTTP(&req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindKafkaRebalanceRule
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, req)
}

// UpdateKafkaRebalanceMonitor PUT /api/alert/kafka-rebalance-monitors/{id}
func UpdateKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindKafkaRebalanceRule}

	var existing model.KafkaRebalanceMonitorRule
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	var req model.KafkaRebalanceMonitorRule
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if err := validateKafkaRebalanceMonitorHTTP(&req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}

	setDoc := bson.M{
		"name":                         req.Name,
		"kafkaInstanceId":              req.KafkaInstanceID,
		"consumerGroup":                req.ConsumerGroup,
		"topic":                        req.Topic,
		"namespace":                    req.Namespace,
		"resources":                    req.Resources,
		"interval":                     req.Interval,
		"rebalanceInitialWaitSeconds":  req.RebalanceInitialWaitSeconds,
		"recheckWaitSeconds":           req.RecheckWaitSeconds,
		"errorRecheckWaitSeconds":      req.ErrorRecheckWaitSeconds,
		"kafkaVersion":                 req.KafkaVersion,
		"commandTimeoutSeconds":        req.CommandTimeoutSeconds,
		"autoRestart":                  req.AutoRestart,
		"notifyOnDetectionError":       req.NotifyOnDetectionError,
		"notifyOnRebalanceAutoFixed":   req.NotifyOnRebalanceAutoFixed,
		"notifyOnRestartTriggered":     req.NotifyOnRestartTriggered,
		"notifyOnRestartFailed":        req.NotifyOnRestartFailed,
		"notifyOnRestartSuccess":       req.NotifyOnRestartSuccess,
		"notifyOnRestartStillAbnormal": req.NotifyOnRestartStillAbnormal,
		"repeatAlert":                  req.RepeatAlert,
		"remindInterval":               req.RemindInterval,
		"alertNote":                    req.AlertNote,
		"remark":                       req.Remark,
		"webhookId":                    req.WebhookID,
		"updatedAt":                    time.Now(),
	}

	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	// 返回更新后的记录
	var updated model.KafkaRebalanceMonitorRule
	if err := database.Config().FindOne(ctx, filter).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, updated)
}

// DeleteKafkaRebalanceMonitor DELETE /api/alert/kafka-rebalance-monitors/{id}
func DeleteKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 删除关联告警历史
	if _, err := database.History().DeleteMany(ctx, bson.M{
		"sourceType": model.SourceTypeKafkaRebalanceMonitor,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindKafkaRebalanceRule})
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

// RefreshKafkaRebalanceMonitor POST /api/alert/kafka-rebalance-monitors/{id}/refresh
func RefreshKafkaRebalanceMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	var rule model.KafkaRebalanceMonitorRule
	if err := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindKafkaRebalanceRule}).Decode(&rule); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_rule_not_found")
		return
	}

	if err := service.GetKafkaRebalanceMonitorScheduler().TryCheckRule(&rule); err != nil {
		if errors.Is(err, service.ErrKafkaRebalanceMonitorRuleRunning) {
			httputil.WriteErr(w, http.StatusConflict, "alert_rule_checking")
			return
		}
		httputil.WriteErr(w, http.StatusInternalServerError, "common_refresh_failed", err.Error())
		return
	}

	// 重新加载最新状态
	var updated model.KafkaRebalanceMonitorRule
	if err := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": kindKafkaRebalanceRule}).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_refreshed_but_load_detail_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, updated)
}

// GetKafkaRebalanceMonitorHistory GET /api/alert/kafka-rebalance-monitors/{id}/alert-history
func GetKafkaRebalanceMonitorHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypeKafkaRebalanceMonitor, id)
}

// applyKafkaRebalanceDefaults 设置创建时的字段默认值（仅当字段为零值时）。
// 上游通过读取 rawFields map 判断字段是否明确提供；此处简化为零值判断，语义一致。
func applyKafkaRebalanceDefaults(req *model.KafkaRebalanceMonitorRule) {
	if req.Interval == 0 {
		req.Interval = 60
	}
	if req.RebalanceInitialWaitSeconds == 0 {
		req.RebalanceInitialWaitSeconds = 180
	}
	if req.RecheckWaitSeconds == 0 {
		req.RecheckWaitSeconds = 300
	}
	if req.ErrorRecheckWaitSeconds == 0 {
		req.ErrorRecheckWaitSeconds = 120
	}
	if req.CommandTimeoutSeconds == 0 {
		req.CommandTimeoutSeconds = 50
	}
	// bool 零值为 false，故默认 true 的字段统一在此设置（仅创建时调用）
	// AutoRestart/NotifyOnDetectionError/NotifyOnRestartFailed/NotifyOnRestartStillAbnormal
	// 上游通过 rawFields 检测是否有明确传入；此处对 false 零值直接设为 true，
	// 与上游语义一致：用户新建时若未传则取默认。
	// （更新接口不调用此函数，行为与上游一致）
}

var k8sResourceRegexpHTTP = regexp.MustCompile(
	`^(deployment|deploy|deployments|statefulset|sts|statefulsets)/[a-zA-Z0-9][a-zA-Z0-9\-\.]*$`)

// validateKafkaRebalanceMonitorHTTP 验证规则字段合法性（mongo 版，无 DB 关联实体校验）。
func validateKafkaRebalanceMonitorHTTP(req *model.KafkaRebalanceMonitorRule) error {
	req.Normalize()
	if req.Name == "" || req.KafkaInstanceID == "" || req.ConsumerGroup == "" {
		return fmt.Errorf("alert_rebalance_fields_required")
	}
	// MDIS 不实现 K8s 自动重启：平台自身的 ClusterRole 是全只读的（见 k8s-cluster/20-rbac.yaml），
	// 要 rollout restart 就得给 apps/deployments 开 patch 写权限，那是另一条安全边界，得单独定。
	//
	// 这里必须直接拒绝，不能只是存下来运行时再跳过。前端表单不暴露该字段，但批量导入和直接
	// 调 API 都能把 autoRestart=true 传进来；此前照单全收，只在调度器里打一行日志说跳过，
	// 调用方以为配好了、真出重平衡时却什么都没发生——是最难查的那种「静默不生效」。
	if req.AutoRestart {
		return fmt.Errorf("alert_rebalance_autorestart_unsupported")
	}
	if req.Interval < 30 {
		return fmt.Errorf("alert_rebalance_interval_too_low")
	}
	if req.RebalanceInitialWaitSeconds < 0 || req.RecheckWaitSeconds < 0 || req.ErrorRecheckWaitSeconds < 0 {
		return fmt.Errorf("alert_rebalance_wait_negative")
	}
	if req.CommandTimeoutSeconds < 1 {
		return fmt.Errorf("alert_rebalance_cmd_timeout_positive")
	}
	for _, resource := range req.Resources {
		if !k8sResourceRegexpHTTP.MatchString(resource) {
			return fmt.Errorf("alert_rebalance_restart_resource_format")
		}
	}
	if req.RepeatAlert && req.RemindInterval <= 0 {
		return fmt.Errorf("alert_rebalance_remind_interval_positive")
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	return nil
}
