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

const kindWebhook = "webhook"

// encryptWebhookSecrets 对 Webhook 敏感字段加密（空字符串跳过）。
func encryptWebhookSecrets(w *model.Webhook) error {
	if w.SMTPPass != "" {
		enc, err := crypto.Encrypt(w.SMTPPass)
		if err != nil {
			return err
		}
		w.SMTPPass = enc
	}
	return nil
}

// clearWebhookSecrets 清除响应中的敏感字段。
func clearWebhookSecrets(w *model.Webhook) {
	w.SMTPPass = ""
}

// ListWebhooks GET /api/alert/webhooks
func ListWebhooks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := database.Config().Find(ctx, bson.M{"kind": kindWebhook})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_query_failed", err.Error())
		return
	}
	defer cur.Close(ctx)

	var docs []model.Webhook
	if err := cur.All(ctx, &docs); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_read_failed", err.Error())
		return
	}

	out := make([]model.Webhook, 0, len(docs))
	for i := range docs {
		clearWebhookSecrets(&docs[i])
		out = append(out, docs[i])
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// CreateWebhook POST /api/alert/webhooks
func CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var wh model.Webhook
	if err := httputil.ReadJSON(r, &wh); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}
	if wh.Name == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "alert_webhook_name_required")
		return
	}

	ctx := r.Context()

	// 唯一性校验
	count, err := database.Config().CountDocuments(ctx, bson.M{"kind": kindWebhook, "name": wh.Name})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_webhook_name_taken")
		return
	}

	wh.ID = bson.NewObjectID().Hex()
	wh.Kind = kindWebhook
	wh.CreatedAt = time.Now()
	wh.UpdatedAt = wh.CreatedAt

	if err := encryptWebhookSecrets(&wh); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
		return
	}

	if _, err := database.Config().InsertOne(ctx, wh); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_create_failed", err.Error())
		return
	}

	clearWebhookSecrets(&wh)
	httputil.WriteJSON(w, http.StatusOK, wh)
}

// UpdateWebhook PUT /api/alert/webhooks/{id}
func UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()
	filter := bson.M{"_id": id, "kind": kindWebhook}

	var existing model.Webhook
	if err := database.Config().FindOne(ctx, filter).Decode(&existing); err != nil {
		httputil.WriteErr(w, http.StatusNotFound, "alert_webhook_not_found")
		return
	}

	var input model.Webhook
	if err := httputil.ReadJSON(r, &input); err != nil {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_param", err.Error())
		return
	}

	// 唯一性校验（排除自身）
	count, err := database.Config().CountDocuments(ctx, bson.M{
		"kind": kindWebhook,
		"name": input.Name,
		"_id":  bson.M{"$ne": id},
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if count > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_webhook_name_taken")
		return
	}

	// 掩码或空值时保留原密文
	if input.SMTPPass == model.MaskValue || input.SMTPPass == "" {
		input.SMTPPass = existing.SMTPPass
	} else {
		// 新明文需要加密
		enc, err := crypto.Encrypt(input.SMTPPass)
		if err != nil {
			httputil.WriteErr(w, http.StatusInternalServerError, "common_encrypt_failed", err.Error())
			return
		}
		input.SMTPPass = enc
	}

	setDoc := bson.M{
		"name":                input.Name,
		"type":                input.Type,
		"url":                 input.URL,
		"proxyUrl":            input.ProxyURL,
		"mentionedList":       input.MentionedList,
		"mentionedMobileList": input.MentionedMobileList,
		"isAtAll":             input.IsAtAll,
		"smtpHost":            input.SMTPHost,
		"smtpPort":            input.SMTPPort,
		"smtpUser":            input.SMTPUser,
		"smtpPass":            input.SMTPPass,
		"skipSSL":             input.SkipSSL,
		"emailTo":             input.EmailTo,
		"updatedAt":           time.Now(),
	}
	if _, err := database.Config().UpdateOne(ctx, filter, bson.M{"$set": setDoc}); err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_update_failed", err.Error())
		return
	}

	input.ID = id
	input.Kind = kindWebhook
	input.CreatedAt = existing.CreatedAt
	clearWebhookSecrets(&input)
	httputil.WriteJSON(w, http.StatusOK, input)
}

// DeleteWebhook DELETE /api/alert/webhooks/{id}
// 注意：上游会检查多张关联表，mongo 版暂时跳过引用检查（配置均在同一集合，需要扫描各 kind 下 webhookId）。
// 若需要强制检查引用，后续可在此扫描 kindTask/kindKafkaRebalanceRule 等。
func DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteErr(w, http.StatusBadRequest, "common_invalid_id")
		return
	}

	ctx := r.Context()

	// 检查是否仍被其他配置引用
	refCount, err := database.Config().CountDocuments(ctx, bson.M{
		"webhookId": id,
		"kind":      bson.M{"$ne": kindWebhook},
	})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_internal_error", err.Error())
		return
	}
	if refCount > 0 {
		httputil.WriteErr(w, http.StatusConflict, "alert_webhook_referenced")
		return
	}

	res, err := database.Config().DeleteOne(ctx, bson.M{"_id": id, "kind": kindWebhook})
	if err != nil {
		httputil.WriteErr(w, http.StatusInternalServerError, "common_delete_failed", err.Error())
		return
	}
	if res.DeletedCount == 0 {
		httputil.WriteErr(w, http.StatusNotFound, "alert_webhook_not_found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]string{"key": "common_deleted"})
}
