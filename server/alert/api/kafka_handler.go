package api

import (
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
	"sourcecode.mingdao.net/private/MDIS/server/alert/service"
)

const kindKafkaInstance = "kafkaInstance"

// encryptKafkaSASLPassword 对 SASL.Password 加密（空字符串跳过）。
func encryptKafkaSASLPassword(sasl *model.SASL) error {
	if sasl == nil || sasl.Password == "" {
		return nil
	}
	enc, err := crypto.Encrypt(sasl.Password)
	if err != nil {
		return err
	}
	sasl.Password = enc
	return nil
}

// ListKafkaInstances GET /api/alert/kafka-instances
func ListKafkaInstances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindKafkaInstance})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var instances []model.KafkaInstance
	if err := cur.All(ctx, &instances); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.KafkaInstance, 0, len(instances))
	for i := range instances {
		instances[i].Sanitize()
		out = append(out, instances[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateKafkaInstance POST /api/alert/kafka-instances
func CreateKafkaInstance(w http.ResponseWriter, r *http.Request) {
	// 默认启用：请求体里没带 enabled 时按「启用」处理（显式传 false 才停用）。
	// 早先直接 var req 靠 Go 零值，等于「不传 = 停用」——客户用 API 批量导入数据源后
	// 全是停用状态、还得再逐个 toggle 一遍才会被采集，且没有任何提示。
	instance := model.KafkaInstance{Enabled: true}
	if err := httputil.ReadJSON(r, &instance); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// SASL 机制白名单校验
	if instance.SASL != nil && instance.SASL.Enabled && !instance.SASL.IsValidMechanism() {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_unsupported_sasl")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindKafkaInstance, "name": instance.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_validate_failed", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_instance_name_taken")
		return
	}

	// 加密 SASL 密码
	if err := encryptKafkaSASLPassword(instance.SASL); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
		return
	}

	instance.ID = bson.NewObjectID().Hex()
	instance.Kind = kindKafkaInstance
	instance.CreatedAt = time.Now()
	instance.UpdatedAt = instance.CreatedAt

	// 用途兜底：请求体没带 purposes 时按前端新建表单的默认值（看图 + 告警）处理。
	// 没有兜底时 API 建出来的数据源 purposes 为空，服务发现按 purposes 含 "metrics" 过滤，
	// 于是「建成功了、UI 里也看得到，但永远不采集」——比 enabled=false 更隐蔽。
	if len(instance.Purposes) == 0 {
		instance.Purposes = []string{"metrics", "alert"}
	}
	if _, err := database.Config().InsertOne(ctx, instance); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	instance.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, instance)
}

// UpdateKafkaInstance PUT /api/alert/kafka-instances/{id}
func UpdateKafkaInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindKafkaInstance}

	var existing model.KafkaInstance
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	var req model.KafkaInstance
	if err := httputil.ReadJSON(r, &req); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// SASL 机制白名单校验
	if req.SASL != nil && req.SASL.Enabled && !req.SASL.IsValidMechanism() {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_unsupported_sasl")
		return
	}

	// 唯一性校验（排除自身）
	count, err := database.Config().CountDocuments(ctx, bson.M{
		"kind": kindKafkaInstance,
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

	// SASL 密码留空或掩码时保留原密文
	if req.SASL != nil && (req.SASL.Password == "" || req.SASL.Password == model.MaskValue) {
		if existing.SASL != nil {
			req.SASL.Password = existing.SASL.Password
		} else {
			req.SASL.Password = ""
		}
	} else if req.SASL != nil && req.SASL.Password != "" {
		// 新明文需要加密
		if err := encryptKafkaSASLPassword(req.SASL); err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
			return
		}
	}

	setDoc := bson.M{
		"name":      req.Name,
		"brokers":   req.Brokers,
		"sasl":      req.SASL,
		"updatedAt": time.Now(),
	}
	if len(req.Purposes) > 0 {
		setDoc["purposes"] = req.Purposes
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	// 配置变更后驱逐缓存连接
	service.EvictKafkaClient(id)

	req.ID = id
	req.Kind = kindKafkaInstance
	req.CreatedAt = existing.CreatedAt
	req.Sanitize()
	httputil.WriteJSON(w, http.StatusOK, req)
}

// DeleteKafkaInstance DELETE /api/alert/kafka-instances/{id}
func DeleteKafkaInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 检查是否有关联的监控任务
	taskCount, err := database.Config().CountDocuments(ctx, bson.M{
		"kind":            kindTask,
		"kafkaInstanceId": id,
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if taskCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_kafka_instance_has_tasks",
			fmt.Sprintf("%d monitor task(s) still exist under this instance, please delete them first", taskCount))
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindKafkaInstance})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_instance_not_found")
		return
	}

	// 清理缓存连接，避免句柄泄漏
	service.EvictKafkaClient(id)

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}
