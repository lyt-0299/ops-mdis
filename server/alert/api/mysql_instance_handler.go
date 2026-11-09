package api

import (
	"fmt"
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

const kindMySQLInstance = "mysqlInstance"

// ListMySQLInstances GET /api/alert/mysql-instances
func ListMySQLInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindMySQLInstance},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var instances []model.MySQLInstance
	if err := cur.All(ctx, &instances); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.MySQLInstance, 0, len(instances))
	for i := range instances {
		instances[i].Sanitize()
		out = append(out, instances[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateMySQLInstance POST /api/alert/mysql-instances
func CreateMySQLInstance(w http.ResponseWriter, r *http.Request) {
	// 默认启用：请求体里没带 enabled 时按「启用」处理（显式传 false 才停用）。
	// 早先直接 var req 靠 Go 零值，等于「不传 = 停用」——客户用 API 批量导入数据源后
	// 全是停用状态、还得再逐个 toggle 一遍才会被采集，且没有任何提示。
	req := model.MySQLInstance{Enabled: true}
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if _, err := req.MetricCollection.ExporterArgs(kindMySQLInstance); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "monitor_collectors_invalid", err.Error())
		return
	}
	req.Normalize()

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	if req.Username == "" || req.Password == "" || req.Database == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_conn_fields_required")
		return
	}
	if !isValidMySQLArchitecture(req.Architecture) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_mysql_arch")
		return
	}
	if err := validateMySQLInstanceNodes(req.Architecture, req.Nodes); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// 加密密码
	enc, err := crypto.Encrypt(req.Password)
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_password_failed", err.Error())
		return
	}
	req.Password = enc

	req.ID = bson.NewObjectID().Hex()
	req.Kind = kindMySQLInstance
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

	req.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, req)
}

// UpdateMySQLInstance PUT /api/alert/mysql-instances/{id}
func UpdateMySQLInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindMySQLInstance}

	var existing model.MySQLInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	var req model.MySQLInstance
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if _, err := req.MetricCollection.ExporterArgs(kindMySQLInstance); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "monitor_collectors_invalid", err.Error())
		return
	}
	req.Normalize()

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

	if req.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_name_required")
		return
	}
	if req.Username == "" || req.Password == "" || req.Database == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_mysql_conn_fields_required")
		return
	}
	if !isValidMySQLArchitecture(req.Architecture) {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_invalid_mysql_arch")
		return
	}
	if err := validateMySQLInstanceNodes(req.Architecture, req.Nodes); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// 架构类型变更时检查是否有关联规则
	if existing.Architecture != req.Architecture {
		ruleCount, _ := database.Config().CountDocuments(ctx, bson.M{
			"kind":            kindMySQLRule,
			"mysqlInstanceId": id,
		})
		if err := validateMySQLArchitectureChange(existing.Architecture, req.Architecture, ruleCount); err != nil {
			httputil.WriteErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	setDoc := bson.M{
		"name":         req.Name,
		"architecture": req.Architecture,
		"nodes":        req.Nodes,
		"username":     req.Username,
		"password":     req.Password,
		"database":     req.Database,
		"tls":          req.TLS,
		"skipSSL":      req.SkipSSL,
		"updatedAt":    time.Now(),
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

	service.EvictMySQLClientPool(id)

	var updated model.MySQLInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&updated); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "alert_updated_but_load_detail_failed", err.Error())
		return
	}
	updated.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// DeleteMySQLInstance DELETE /api/alert/mysql-instances/{id}
func DeleteMySQLInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 检查是否有关联的监控规则
	ruleCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind":            kindMySQLRule,
		"mysqlInstanceId": id,
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if ruleCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_has_rules")
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindMySQLInstance})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	service.EvictMySQLClientPool(id)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}

// isValidMySQLArchitecture 校验架构类型合法性。
func isValidMySQLArchitecture(value string) bool {
	switch value {
	case model.MySQLArchitectureStandalone, model.MySQLArchitectureReplication, model.MySQLArchitectureMGR:
		return true
	}
	return false
}

// validateMySQLInstanceNodes 校验节点列表与架构类型的匹配。
func validateMySQLInstanceNodes(architecture string, nodes []string) error {
	if len(nodes) == 0 {
		return fmt.Errorf("alert_mysql_node_required")
	}
	switch architecture {
	case model.MySQLArchitectureStandalone:
		if len(nodes) != 1 {
			return fmt.Errorf("alert_mysql_single_node_one_addr")
		}
	case model.MySQLArchitectureReplication:
		if len(nodes) < 2 {
			return fmt.Errorf("alert_mysql_replication_two_addrs")
		}
	}
	return nil
}

// validateMySQLArchitectureChange 校验架构变更是否合法（有关联规则时禁止变更）。
func validateMySQLArchitectureChange(existing, next string, ruleCount int64) error {
	if existing != next && ruleCount > 0 {
		return fmt.Errorf("alert_mysql_arch_locked_referenced")
	}
	return nil
}
