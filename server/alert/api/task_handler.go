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

const kindTask = "task"

// ListTasks GET /api/alert/tasks
func ListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cur, err := database.Config().Find(ctx, bson.M{"kind": kindTask},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var tasks []model.Task
	if err := cur.All(ctx, &tasks); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	// 批量注入 KafkaInstance
	instanceIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.KafkaInstanceID != "" {
			instanceIDs = append(instanceIDs, t.KafkaInstanceID)
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

	// 批量注入 Webhook
	webhookIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t.WebhookID != "" {
			webhookIDs = append(webhookIDs, t.WebhookID)
		}
	}
	webhookMap := make(map[string]model.Webhook)
	if len(webhookIDs) > 0 {
		wCur, err := database.Config().Find(ctx, bson.M{"kind": kindWebhook, "_id": bson.M{"$in": webhookIDs}})
		if err == nil {
			var webhooks []model.Webhook
			_ = wCur.All(ctx, &webhooks)
			wCur.Close(ctx)
			for _, wh := range webhooks {
				clearWebhookSecrets(&wh)
				webhookMap[wh.ID] = wh
			}
		}
	}

	type TaskWithLag struct {
		model.Task
		CurrentLag int64 `json:"currentLag"`
	}

	result := make([]TaskWithLag, 0, len(tasks))
	for i := range tasks {
		if inst, ok := instanceMap[tasks[i].KafkaInstanceID]; ok {
			tasks[i].KafkaInstance = inst
		}
		if wh, ok := webhookMap[tasks[i].WebhookID]; ok {
			copy := wh
			tasks[i].Webhook = &copy
		}
		result = append(result, TaskWithLag{
			Task:       tasks[i],
			CurrentLag: tasks[i].LastLag,
		})
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

// CreateTask POST /api/alert/tasks
func CreateTask(w http.ResponseWriter, r *http.Request) {
	var task model.Task
	if err := httputil.ReadJSON(r, &task); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if task.Interval < 10 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_check_interval_too_low")
		return
	}
	if task.Threshold < 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_threshold_negative")
		return
	}

	ctx := r.Context()

	// 任务名称唯一性校验
	nameCount, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindTask, "name": task.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if nameCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_task_name_taken")
		return
	}

	// 同一 Kafka 实例下，消费组+Topic 唯一
	dupCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind":            kindTask,
		"kafkaInstanceId": task.KafkaInstanceID,
		"consumerGroup":   task.ConsumerGroup,
		"topic":           task.Topic,
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if dupCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_kafka_task_duplicate")
		return
	}

	task.ID = bson.NewObjectID().Hex()
	task.Kind = kindTask
	if task.Status == "" {
		task.Status = model.TaskStatusActive
	}
	if task.AlertState == "" {
		task.AlertState = model.AlertStateOK
	}
	task.CreatedAt = time.Now()
	task.UpdatedAt = task.CreatedAt

	if _, err := database.Config().InsertOne(ctx, task); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, task)
}

// UpdateTask PUT /api/alert/tasks/{id}
func UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindTask}

	var existing model.Task
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_task_not_found")
		return
	}

	var task model.Task
	if err := httputil.ReadJSON(r, &task); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if task.Interval < 10 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_check_interval_too_low")
		return
	}
	if task.Threshold < 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_threshold_negative")
		return
	}

	// 任务名称唯一性校验（排除自身）
	nameCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind": kindTask,
		"name": task.Name,
		"_id":  bson.M{"$ne": id},
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if nameCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_task_name_taken")
		return
	}

	// 消费组+Topic 唯一性校验（排除自身）
	dupCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind":            kindTask,
		"kafkaInstanceId": task.KafkaInstanceID,
		"consumerGroup":   task.ConsumerGroup,
		"topic":           task.Topic,
		"_id":             bson.M{"$ne": id},
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if dupCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_kafka_task_duplicate")
		return
	}

	setDoc := bson.M{
		"name":            task.Name,
		"kafkaInstanceId": task.KafkaInstanceID,
		"topic":           task.Topic,
		"consumerGroup":   task.ConsumerGroup,
		"threshold":       task.Threshold,
		"interval":        task.Interval,
		"alertNote":       task.AlertNote,
		"remark":          task.Remark,
		"webhookId":       task.WebhookID,
		"repeatAlert":     task.RepeatAlert,
		"remindInterval":  task.RemindInterval,
		"updatedAt":       time.Now(),
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	task.ID = id
	task.Kind = kindTask
	task.CreatedAt = existing.CreatedAt
	task.LastLag = existing.LastLag
	httputil.WriteJSON(w, http.StatusOK, task)
}

// DeleteTask DELETE /api/alert/tasks/{id}
func DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 删除告警历史
	if _, err := database.History().DeleteMany(ctx, bson.M{
		"sourceType": model.SourceTypeKafka,
		"sourceId":   id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	// 删除 Lag 历史
	if _, err := database.History().DeleteMany(ctx, bson.M{
		"kind":   "lagHistory",
		"taskId": id,
	}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindTask})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_task_not_found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// GetTaskHistory GET /api/alert/tasks/{id}/history — Lag 趋势（最近 100 条）
func GetTaskHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	cur, err := database.History().Find(ctx,
		bson.M{"kind": "lagHistory", "taskId": id},
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(100))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var histories []model.LagHistory
	if err := cur.All(ctx, &histories); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	// 内存反转，恢复时间正序供前端绘图
	for i, j := 0, len(histories)-1; i < j; i, j = i+1, j-1 {
		histories[i], histories[j] = histories[j], histories[i]
	}

	httputil.WriteJSON(w, http.StatusOK, histories)
}

// RefreshTask POST /api/alert/tasks/{id}/refresh — 手动立即执行一次
func RefreshTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	if err := service.ManualRunTask(id); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_refresh_failed", err.Error())
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_refreshed"})
}

// GetTaskAlertHistory GET /api/alert/tasks/{id}/alert-history
func GetTaskAlertHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}
	listAlertHistoryBySourceID(w, r, model.SourceTypeKafka, id)
}

// listAlertHistoryBySourceID 按 sourceType+sourceID 分页查询历史（内部公共函数）
func listAlertHistoryBySourceID(w http.ResponseWriter, r *http.Request, sourceType, sourceID string) {
	page, pageSize := parsePaginationHTTP(r)
	ctx := r.Context()

	filter := bson.M{"sourceType": sourceType, "sourceId": sourceID}

	total, err := database.History().CountDocuments(ctx, filter)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	skip := int64((page - 1) * pageSize)
	cur, err := database.History().Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "_id", Value: -1}}).
			SetSkip(skip).
			SetLimit(int64(pageSize)))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var items []model.AlertHistory
	if err := cur.All(ctx, &items); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}
	if items == nil {
		items = []model.AlertHistory{}
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"total": total,
		"items": items,
	})
}

// parsePaginationHTTP 解析通用分页参数
func parsePaginationHTTP(r *http.Request) (page, pageSize int) {
	page = 1
	pageSize = 20
	q := r.URL.Query()
	if p := q.Get("page"); p != "" {
		if v := atoiPositive(p); v > 0 {
			page = v
		}
	}
	if ps := q.Get("pageSize"); ps != "" {
		if v := atoiPositive(ps); v > 0 {
			if v > 100 {
				v = 100
			}
			pageSize = v
		}
	}
	return
}

// atoiPositive 字符串转正整数，非法返回 0
func atoiPositive(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
