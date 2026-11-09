// Package api 实现告警子系统的 HTTP 处理器(net/http + mongo-driver，无 gin 依赖)。
package api

import (
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/crypto"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/httputil"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// kindDataSource 是存储在 alert_config 集合的 discriminator 字段值。
const kindDataSource = "source"

// clearSecrets 清除 DataSource 的敏感字段，用于 API 响应。
func clearSecrets(ds *model.DataSource) {
	ds.Password = ""
	ds.Token = ""
}

// encryptDSSecrets 对 DataSource 的 Password/Token 加密（空字符串跳过）。
func encryptDSSecrets(ds *model.DataSource) error {
	if ds.Password != "" {
		enc, err := crypto.Encrypt(ds.Password)
		if err != nil {
			return err
		}
		ds.Password = enc
	}
	if ds.Token != "" {
		enc, err := crypto.Encrypt(ds.Token)
		if err != nil {
			return err
		}
		ds.Token = enc
	}
	return nil
}

// ListDataSources GET /api/alert/datasources
// 返回所有数据源列表(敏感字段已清除)。
func ListDataSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindDataSource})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var docs []model.DataSource
	if err := cur.All(ctx, &docs); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.DataSource, 0, len(docs))
	for i := range docs {
		clearSecrets(&docs[i])
		out = append(out, docs[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateDataSource POST /api/alert/datasources
// 从请求体读取数据源配置，加密敏感字段后写入 mongo，返回含 id 的创建结果。
func CreateDataSource(w http.ResponseWriter, r *http.Request) {
	var ds model.DataSource
	if err := httputil.ReadJSON(r, &ds); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if ds.Name == "" || ds.URL == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_name_url_required")
		return
	}
	if ds.Type == "" {
		ds.Type = "prometheus"
	}

	ds.ID = bson.NewObjectID().Hex()
	ds.Kind = kindDataSource

	if err := encryptDSSecrets(&ds); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
		return
	}

	ctx := r.Context()
	if _, err := database.Config().InsertOne(ctx, ds); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	clearSecrets(&ds)
	httputil.WriteJSON(w, http.StatusOK, ds)
}

// UpdateDataSource PUT /api/alert/datasources/{id}
// 更新已有数据源；若前端未提供新的 Password/Token，保留原有密文。
func UpdateDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	// 内置 Prometheus 只读，整体拒绝修改
	if id == model.BuiltinPrometheusID {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"key": "alert_builtin_prometheus_readonly"})
		return
	}

	var input model.DataSource
	if err := httputil.ReadJSON(r, &input); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindDataSource}

	// 先查原文档，用于保留未提供的密文字段
	var existing model.DataSource
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_datasource_not_found")
		return
	}

	// 内置单例只读，整体拒绝修改
	if existing.Builtin {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"key": "alert_builtin_prometheus_readonly"})
		return
	}

	// 未提供新密钥则保留原密文
	if input.Password == "" {
		input.Password = existing.Password
	}
	if input.Token == "" {
		input.Token = existing.Token
	}

	// 仅对新提供的明文加密（原密文保留原样）
	newPassword := input.Password != existing.Password
	newToken := input.Token != existing.Token

	if newPassword {
		enc, err := crypto.Encrypt(input.Password)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
			return
		}
		input.Password = enc
	}
	if newToken {
		enc, err := crypto.Encrypt(input.Token)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
			return
		}
		input.Token = enc
	}

	setDoc := bson.M{
		"name":      input.Name,
		"type":      input.Type,
		"url":       input.URL,
		"authType":  input.AuthType,
		"username":  input.Username,
		"password":  input.Password,
		"token":     input.Token,
		"skipSSL":   input.SkipSSL,
		"updatedAt": time.Now(),
	}
	if len(input.Purposes) > 0 {
		setDoc["purposes"] = input.Purposes
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	// 返回更新后的记录（含 id，不含密文）
	input.ID = id
	input.Kind = kindDataSource
	clearSecrets(&input)
	httputil.WriteJSON(w, http.StatusOK, input)
}

// DeleteDataSource DELETE /api/alert/datasources/{id}
func DeleteDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	// 内置 Prometheus 不可删除
	if id == model.BuiltinPrometheusID {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"key": "alert_builtin_prometheus_undeletable"})
		return
	}

	ctx := r.Context()
	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindDataSource})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_datasource_not_found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}
