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
)

const kindESInstance = "esInstance"

// ListESInstances GET /api/alert/es-instances
func ListESInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindESInstance},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var instances []model.ESInstance
	if err := cur.All(ctx, &instances); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.ESInstance, 0, len(instances))
	for i := range instances {
		instances[i].Sanitize()
		out = append(out, instances[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateESInstance POST /api/alert/es-instances
func CreateESInstance(w http.ResponseWriter, r *http.Request) {
	// 默认启用：请求体里没带 enabled 时按「启用」处理（显式传 false 才停用）。
	// 早先直接 var req 靠 Go 零值，等于「不传 = 停用」——客户用 API 批量导入数据源后
	// 全是停用状态、还得再逐个 toggle 一遍才会被采集，且没有任何提示。
	req := model.ESInstance{Enabled: true}
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	// nodes 与旧的单地址字段互相兜底，见 NormalizeNodes
	req.NormalizeNodes()

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	if req.URL == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_url_required")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindESInstance, "name": req.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_name_taken")
		return
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

	if len(req.Purposes) == 0 {
		req.Purposes = []string{"metrics"}
	}

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindESInstance
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	req.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, req)
}

// UpdateESInstance PUT /api/alert/es-instances/{id}
func UpdateESInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindESInstance}

	var existing model.ESInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	var req model.ESInstance
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	// nodes 与旧的单地址字段互相兜底，见 NormalizeNodes
	req.NormalizeNodes()

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	if req.URL == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_url_required")
		return
	}

	// 唯一性校验（排除自身）
	count, err := database.Config().CountDocuments(ctx, bson.M{
		"kind": kindESInstance,
		"name": req.Name,
		"_id":  bson.M{"$ne": id},
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_name_taken")
		return
	}

	// 密码为脱敏值或空时保留原密文
	if req.Password == model.MaskValue || req.Password == "" {
		req.Password = existing.Password
	} else {
		enc, err := crypto.Encrypt(req.Password)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_password_failed", err.Error())
			return
		}
		req.Password = enc
	}

	setDoc := bson.M{
		"name":      req.Name,
		"url":       req.URL,
		"username":  req.Username,
		"password":  req.Password,
		"skipSSL":   req.SkipSSL,
		"purposes":  req.Purposes,
		"updatedAt": time.Now(),
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	var updated model.ESInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	updated.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// DeleteESInstance DELETE /api/alert/es-instances/{id}
func DeleteESInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindESInstance})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}
