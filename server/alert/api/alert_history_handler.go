package api

import (
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// GetGlobalAlertHistory GET /api/alert/history
// 查询全局告警历史，支持多维筛选与分页。
func GetGlobalAlertHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sourceType := q.Get("sourceType")
	sourceName := q.Get("sourceName")
	alertState := q.Get("alertState")
	startTimeStr := q.Get("startTime")
	endTimeStr := q.Get("endTime")
	sort := q.Get("sort")
	if sort == "" {
		sort = "desc"
	}

	page, pageSize := parsePaginationHTTP(r)
	ctx := r.Context()

	filter := bson.M{}
	if sourceType != "" {
		filter["sourceType"] = sourceType
	}
	if sourceName != "" {
		filter["sourceName"] = bson.Regex{Pattern: sourceName, Options: "i"}
	}
	if alertState != "" {
		filter["alertState"] = alertState
	}
	if t, ok := parseTimeParamHTTP(startTimeStr); ok {
		if filter["triggeredAt"] == nil {
			filter["triggeredAt"] = bson.M{}
		}
		filter["triggeredAt"].(bson.M)["$gte"] = t
	}
	if t, ok := parseTimeParamHTTP(endTimeStr); ok {
		if filter["triggeredAt"] == nil {
			filter["triggeredAt"] = bson.M{}
		}
		filter["triggeredAt"].(bson.M)["$lte"] = t
	}

	total, err := database.History().CountDocuments(ctx, filter)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	sortVal := -1
	if sort == "asc" {
		sortVal = 1
	}
	skip := int64((page - 1) * pageSize)
	cur, err := database.History().Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "_id", Value: sortVal}}).
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

// DeleteGlobalAlertHistory DELETE /api/alert/history (batch by IDs)
// 批量删除指定 ID 的告警历史记录。
func DeleteGlobalAlertHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param_format", err.Error())
		return
	}
	if len(req.IDs) == 0 {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_delete_ids_required")
		return
	}

	ctx := r.Context()
	if _, err := database.History().DeleteMany(ctx, bson.M{"_id": bson.M{"$in": req.IDs}}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// DeleteAllGlobalAlertHistory DELETE /api/alert/history/all
// 根据筛选条件批量清空告警历史记录。
func DeleteAllGlobalAlertHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sourceType := q.Get("sourceType")
	sourceName := q.Get("sourceName")
	alertState := q.Get("alertState")
	startTimeStr := q.Get("startTime")
	endTimeStr := q.Get("endTime")

	filter := bson.M{}
	if sourceType != "" {
		filter["sourceType"] = sourceType
	}
	if sourceName != "" {
		filter["sourceName"] = bson.Regex{Pattern: sourceName, Options: "i"}
	}
	if alertState != "" {
		filter["alertState"] = alertState
	}
	if t, ok := parseTimeParamHTTP(startTimeStr); ok {
		if filter["triggeredAt"] == nil {
			filter["triggeredAt"] = bson.M{}
		}
		filter["triggeredAt"].(bson.M)["$gte"] = t
	}
	if t, ok := parseTimeParamHTTP(endTimeStr); ok {
		if filter["triggeredAt"] == nil {
			filter["triggeredAt"] = bson.M{}
		}
		filter["triggeredAt"].(bson.M)["$lte"] = t
	}

	// 无条件时 filter 为空 map，DeleteMany 会删所有记录；这是预期行为（前端会确认）
	ctx := r.Context()
	if _, err := database.History().DeleteMany(ctx, filter); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_clear_failed", err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "alert_history_cleared"})
}

// GetAlertHistorySourceTypes GET /api/alert/history/source-types
// 返回所有已注册的来源类型枚举值（固定列表，不依赖数据库）。
func GetAlertHistorySourceTypes(w http.ResponseWriter, r *http.Request) {
	types := []map[string]string{
		{"value": model.SourceTypePrometheus},
		{"value": model.SourceTypePortMonitor},
		{"value": model.SourceTypeSslMonitor},
		{"value": model.SourceTypeMySQLMonitor},
		{"value": model.SourceTypeMongoMonitor},
		{"value": model.SourceTypeKafka},
		{"value": model.SourceTypeKafkaRebalanceMonitor},
	}
	httputil.WriteJSON(w, http.StatusOK, types)
}

// parseTimeParamHTTP 解析时间字符串（RFC3339Nano / RFC3339）。
// 兼容 JS Date.toISOString() 带毫秒格式。
func parseTimeParamHTTP(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
