package api

import (
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindMongoInstance = "mongoInstance"

// ListMongoInstances GET /api/alert/mongo-instances
func ListMongoInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindMongoInstance},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var instances []model.MongoInstance
	if err := cur.All(ctx, &instances); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.MongoInstance, 0, len(instances))
	for i := range instances {
		instances[i].Sanitize()
		out = append(out, instances[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateMongoInstance POST /api/alert/mongo-instances
func CreateMongoInstance(w http.ResponseWriter, r *http.Request) {
	// 默认启用：请求体里没带 enabled 时按「启用」处理（显式传 false 才停用）。
	// 早先直接 var req 靠 Go 零值，等于「不传 = 停用」——客户用 API 批量导入数据源后
	// 全是停用状态、还得再逐个 toggle 一遍才会被采集，且没有任何提示。
	req := model.MongoInstance{Enabled: true}
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if _, err := req.MetricCollection.ExporterArgs(kindMongoInstance); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "monitor_collectors_invalid", err.Error())
		return
	}

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	if req.URI == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_uri_required")
		return
	}
	if req.AuthDB == "" {
		req.AuthDB = "admin"
	}

	// 加密密码（如果提供了明文密码）
	if req.Password != "" {
		enc, err := crypto.Encrypt(req.Password)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_password_failed", err.Error())
			return
		}
		req.Password = enc
	}

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindMongoInstance
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	ctx := r.Context()
	// 用途兜底：请求体没带 purposes 时按前端新建表单的默认值（看图 + 告警）处理。
	// 没有兜底时 API 建出来的数据源 purposes 为空，服务发现按 purposes 含 "metrics" 过滤，
	// 于是「建成功了、UI 里也看得到，但永远不采集」——比 enabled=false 更隐蔽。
	if len(req.Purposes) == 0 {
		req.Purposes = []string{"metrics", "alert"}
	}
	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	reloadSlowQueryTargets("新增 MongoDB 实例 " + req.Name)

	req.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, req)
}

// UpdateMongoInstance PUT /api/alert/mongo-instances/{id}
func UpdateMongoInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindMongoInstance}

	var existing model.MongoInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	var req model.MongoInstance
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if _, err := req.MetricCollection.ExporterArgs(kindMongoInstance); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "monitor_collectors_invalid", err.Error())
		return
	}

	// 密码为脱敏值或空时保留原密文
	if req.Password == model.MaskValue || req.Password == "" {
		req.Password = existing.Password
	} else {
		// 新明文需要加密
		enc, err := crypto.Encrypt(req.Password)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_password_failed", err.Error())
			return
		}
		req.Password = enc
	}

	// URI 中的密码若为脱敏占位，用原 URI 还原
	req.URI = model.RestoreMongoURIPassword(req.URI, existing.URI)

	setDoc := bson.M{
		"name":      req.Name,
		"uri":       req.URI,
		"username":  req.Username,
		"password":  req.Password,
		"authDb":    req.AuthDB,
		"tls":       req.TLS,
		"skipSSL":   req.SkipSSL,
		"updatedAt": time.Now(),
	}
	if req.MetricCollection != nil {
		setDoc["metricCollection"] = req.MetricCollection
	}
	if len(req.Purposes) > 0 {
		setDoc["purposes"] = req.Purposes
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	// 配置变更后关闭旧连接，下次使用时自动重建
	service.CloseMongoClient(id)
	reloadSlowQueryTargets("修改 MongoDB 实例 " + id)

	// 返回更新后的记录
	var updated model.MongoInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	updated.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// DeleteMongoInstance DELETE /api/alert/mongo-instances/{id}
func DeleteMongoInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 检查是否有关联的监控规则
	ruleCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind":            kindMongoRule,
		"mongoInstanceId": id,
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if ruleCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_has_rules")
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindMongoInstance})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	// 清理缓存连接
	service.CloseMongoClient(id)
	reloadSlowQueryTargets("删除 MongoDB 实例 " + id)

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}
