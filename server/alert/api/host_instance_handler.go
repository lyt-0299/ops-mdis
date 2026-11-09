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
)

const kindHostInstance = model.KindHostInstance

// validHostAddress 校验 address 形如 host:port(非空、含且仅含一个冒号、两侧均非空)。
func validHostAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host, port, ok := strings.Cut(addr, ":")
	if !ok || host == "" || port == "" {
		return false
	}
	// port 段不应再含冒号(排除 IPv6 等非 host:port 形态)
	if strings.Contains(port, ":") {
		return false
	}
	return true
}

// ListHostInstances GET /api/alert/host-instances
func ListHostInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindHostInstance},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var instances []model.HostInstance
	if err := cur.All(ctx, &instances); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.HostInstance, 0, len(instances))
	for i := range instances {
		instances[i].Sanitize()
		out = append(out, instances[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateHostInstance POST /api/alert/host-instances
func CreateHostInstance(w http.ResponseWriter, r *http.Request) {
	// 默认启用：请求体里没带 enabled 时按「启用」处理（显式传 false 才停用）。
	// 早先直接 var req 靠 Go 零值，等于「不传 = 停用」——客户用 API 批量导入数据源后
	// 全是停用状态、还得再逐个 toggle 一遍才会被采集，且没有任何提示。
	req := model.HostInstance{Enabled: true}
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if !validHostAddress(req.Address) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_address_invalid")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindHostInstance, "name": req.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_name_taken")
		return
	}

	if len(req.Purposes) == 0 {
		req.Purposes = []string{"metrics"}
	}

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindHostInstance
	req.CreatedAt = time.Now()
	req.UpdatedAt = req.CreatedAt

	if _, err := database.Config().InsertOne(ctx, req); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed_name_exists", err.Error())
		return
	}

	req.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, req)
}

// UpdateHostInstance PUT /api/alert/host-instances/{id}
func UpdateHostInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindHostInstance}

	var existing model.HostInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	var req model.HostInstance
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if !validHostAddress(req.Address) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_address_invalid")
		return
	}

	// 唯一性校验（排除自身）
	count, err := database.Config().CountDocuments(ctx, bson.M{
		"kind": kindHostInstance,
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

	setDoc := bson.M{
		"name":      req.Name,
		"address":   req.Address,
		"purposes":  req.Purposes,
		"updatedAt": time.Now(),
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	var updated model.HostInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	updated.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// DeleteHostInstance DELETE /api/alert/host-instances/{id}
func DeleteHostInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindHostInstance})
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
