package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// AlertSilenceRequest 创建/更新静默请求
type AlertSilenceRequest struct {
	ScopeKeys  []string   `json:"scopeKeys"`
	Mode       string     `json:"mode"`
	MutedUntil *time.Time `json:"mutedUntil"`
	Reason     string     `json:"reason"`
}

// AlertSilenceScheduler 静默过期调度器
type AlertSilenceScheduler struct {
	stopChan  chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

var (
	globalAlertSilenceScheduler     *AlertSilenceScheduler
	globalAlertSilenceSchedulerOnce sync.Once
)

// GetAlertSilenceScheduler 获取全局静默调度器单例
func GetAlertSilenceScheduler() *AlertSilenceScheduler {
	globalAlertSilenceSchedulerOnce.Do(func() {
		globalAlertSilenceScheduler = &AlertSilenceScheduler{stopChan: make(chan struct{})}
	})
	return globalAlertSilenceScheduler
}

// StartAlertSilenceScheduler 启动全局静默过期调度器
func StartAlertSilenceScheduler() {
	GetAlertSilenceScheduler().Start()
}

// StopAlertSilenceScheduler 停止全局静默过期调度器
func StopAlertSilenceScheduler() {
	if globalAlertSilenceScheduler != nil {
		globalAlertSilenceScheduler.Stop()
	}
}

func (s *AlertSilenceScheduler) Start() {
	s.startOnce.Do(func() {
		log.Println("[AlertSilenceScheduler] 启动告警静默过期调度器")
		s.expireSilences()
		go s.run()
	})
}

func (s *AlertSilenceScheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stopChan) })
}

func (s *AlertSilenceScheduler) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.expireSilences()
		}
	}
}

// expireSilences 将所有已过期的临时静默状态从 active → expired。
// 上游用 GORM Transaction；mongo 无通用行锁，改为原子 UpdateMany。
func (s *AlertSilenceScheduler) expireSilences() {
	now := time.Now().UTC()
	ctx := context.TODO()

	result, err := database.Config().UpdateMany(ctx,
		bson.M{
			"kind":       "silence",
			"status":     model.SilenceStatusActive,
			"mode":       model.SilenceModeTemporary,
			"mutedUntil": bson.M{"$ne": nil, "$lte": now},
		},
		bson.M{"$set": bson.M{
			"status":    model.SilenceStatusExpired,
			"updatedAt": now,
		}},
	)
	if err != nil {
		log.Printf("[AlertSilenceScheduler] 查询/更新过期静默失败: %v", err)
		return
	}
	if result.ModifiedCount > 0 {
		log.Printf("[AlertSilenceScheduler] 已自动过期 %d 条临时静默", result.ModifiedCount)
	}
}

// CreatePrometheusSilences 为 Prometheus 规则创建静默
func CreatePrometheusSilences(ruleID string, req AlertSilenceRequest) ([]model.AlertSilence, error) {
	return CreateMonitorSilences(model.SourceTypePrometheus, ruleID, req)
}

// CreateMonitorSilences 通用静默创建函数（支持多 scopeKey）。
// 上游使用 GORM clause.OnConflict(source_type, source_id, scope_type, scope_key) —— 即
// "存在则更新 display_name/mode/muted_until/reason/status/updated_at，否则插入"。
// mongo 版用 FindOneAndUpdate + upsert: true 实现等价逻辑。
func CreateMonitorSilences(sourceType string, sourceID string, req AlertSilenceRequest) ([]model.AlertSilence, error) {
	if len(req.ScopeKeys) == 0 {
		return nil, errors.New("scope_keys cannot be empty")
	}
	normalizedScopeKeys := make([]string, 0, len(req.ScopeKeys))
	seenScopeKeys := make(map[string]bool, len(req.ScopeKeys))
	for _, rawKey := range req.ScopeKeys {
		scopeKey := strings.TrimSpace(rawKey)
		if scopeKey == "" || seenScopeKeys[scopeKey] {
			continue
		}
		normalizedScopeKeys = append(normalizedScopeKeys, scopeKey)
		seenScopeKeys[scopeKey] = true
	}
	if len(normalizedScopeKeys) == 0 {
		return nil, errors.New("scope_keys cannot be empty")
	}
	if req.Mode == "" {
		req.Mode = model.SilenceModeTemporary
	}
	now := time.Now().UTC()
	switch req.Mode {
	case model.SilenceModeTemporary:
		if req.MutedUntil == nil {
			return nil, errors.New("temporary silence requires muted_until")
		}
		if !req.MutedUntil.After(now) {
			return nil, errors.New("muted_until must be later than current time")
		}
	case model.SilenceModePermanent:
		req.MutedUntil = nil
	default:
		return nil, fmt.Errorf("unsupported silence mode: %s", req.Mode)
	}

	ctx := context.TODO()

	// 校验静默目标存在（对应上游 ensureSilenceTargetExists）
	if err := ensureSilenceTargetExistsMongo(ctx, sourceType, sourceID); err != nil {
		return nil, ErrPauseTargetNotFound
	}

	// 加载 displayName 映射（从 State 集合）
	displayNames, err := loadInstanceDisplayNamesMongo(ctx, sourceType, sourceID)
	if err != nil {
		return nil, err
	}

	var result []model.AlertSilence

	for _, scopeKey := range normalizedScopeKeys {
		displayName := displayNames[scopeKey]
		if displayName == "" {
			displayName = scopeKey
		}

		var mutedUntil *time.Time
		if req.MutedUntil != nil {
			t := req.MutedUntil.UTC()
			mutedUntil = &t
		}

		// Upsert：以 (sourceType, sourceId, scopeType, scopeKey) 为复合唯一键
		filter := bson.M{
			"kind":       "silence",
			"sourceType": sourceType,
			"sourceId":   sourceID,
			"scopeType":  model.SilenceScopeInstance,
			"scopeKey":   scopeKey,
		}
		setDoc := bson.M{
			"displayName": displayName,
			"mode":        req.Mode,
			"mutedUntil":  mutedUntil,
			"reason":      strings.TrimSpace(req.Reason),
			"status":      model.SilenceStatusActive,
			"updatedAt":   now,
		}
		setOnInsertDoc := bson.M{
			"_id":       bson.NewObjectID().Hex(),
			"kind":      "silence",
			"createdAt": now,
		}
		update := bson.M{
			"$set":         setDoc,
			"$setOnInsert": setOnInsertDoc,
		}
		opts := options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After)

		var silence model.AlertSilence
		if err := database.Config().FindOneAndUpdate(ctx, filter, update, opts).Decode(&silence); err != nil {
			return nil, fmt.Errorf("upsert silence (scopeKey=%s) 失败: %v", scopeKey, err)
		}
		result = append(result, silence)
	}

	// 检查是否全部实例已静默，若是则重置规则告警状态
	if err := resetMonitorStateIfFullySilencedMongo(ctx, sourceType, sourceID); err != nil {
		log.Printf("[AlertSilence] 检查全量静默后重置状态失败 [%s/%s]: %v", sourceType, sourceID, err)
	}

	return result, nil
}

// CancelPrometheusSilence 取消 Prometheus 规则的单个实例静默
func CancelPrometheusSilence(ruleID string, scopeKey string) error {
	return CancelMonitorSilence(model.SourceTypePrometheus, ruleID, scopeKey)
}

// CancelMonitorSilence 通用取消静默
func CancelMonitorSilence(sourceType string, sourceID string, scopeKey string) error {
	ctx := context.TODO()
	now := time.Now().UTC()

	result, err := database.Config().UpdateOne(ctx,
		bson.M{
			"kind":       "silence",
			"sourceType": sourceType,
			"sourceId":   sourceID,
			"scopeType":  model.SilenceScopeInstance,
			"scopeKey":   scopeKey,
			"status":     model.SilenceStatusActive,
		},
		bson.M{"$set": bson.M{
			"status":    model.SilenceStatusCancelled,
			"updatedAt": now,
		}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrPauseTargetNotFound
	}
	return nil
}

// ListPrometheusInstances 列出 Prometheus 规则的所有实例状态
func ListPrometheusInstances(ruleID string) ([]model.AlertInstanceState, error) {
	return ListMonitorInstances(model.SourceTypePrometheus, ruleID)
}

// ListMonitorInstances 通用实例状态列表查询
func ListMonitorInstances(sourceType string, sourceID string) ([]model.AlertInstanceState, error) {
	ctx := context.TODO()

	// 排序：Firing 优先，再按 updatedAt DESC，displayName ASC
	// 上游：Order("alert_state = 'Firing' DESC, updated_at DESC, display_name ASC")
	// mongo 无 SQL 条件排序；改用 aggregation $addFields + $sort 实现等价
	pipeline := []bson.M{
		{"$match": bson.M{
			"sourceType": sourceType,
			"sourceId":   sourceID,
		}},
		{"$addFields": bson.M{
			"_firingFirst": bson.M{"$cond": bson.A{
				bson.M{"$eq": []interface{}{"$alertState", model.AlertStateFiring}},
				0, 1,
			}},
		}},
		{"$sort": bson.D{
			{Key: "_firingFirst", Value: 1},
			{Key: "updatedAt", Value: -1},
			{Key: "displayName", Value: 1},
		}},
		{"$project": bson.M{"_firingFirst": 0}},
	}

	cur, err := database.State().Aggregate(ctx, pipeline)
	if err != nil {
		return []model.AlertInstanceState{}, nil
	}
	defer cur.Close(ctx)

	var instances []model.AlertInstanceState
	if err := cur.All(ctx, &instances); err != nil {
		return nil, err
	}

	// 注入静默信息
	silences, err := LoadActiveSilences(sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	for i := range instances {
		if silence, ok := silences[instances[i].ScopeKey]; ok {
			instances[i].Muted = true
			instances[i].MutedUntil = silence.MutedUntil
			instances[i].MuteReason = silence.Reason
			instances[i].SilenceID = silence.ID
		}
	}
	return instances, nil
}

// MonitorInstanceSnapshot 一个监控对象实例的快照（供子监控调度器使用）
type MonitorInstanceSnapshot struct {
	ScopeKey    string
	DisplayName string
	AlertState  string
	LastValue   float64
	LastDetail  string
}

// SuppressedHistoryMeta 写入静默历史所需的元数据
type SuppressedHistoryMeta struct {
	SourceType     string
	SourceID       string
	SourceName     string
	Duration       int
	Interval       int
	RemindInterval int
}

// UpdateMonitorInstanceStates 通用实例状态更新函数（供 port/ssl/mongo/mysql 调度器调用）。
// 上游使用 GORM clause.OnConflict upsert；mongo 版逐个 FindOneAndUpdate + upsert。
func UpdateMonitorInstanceStates(meta SuppressedHistoryMeta, snapshots []MonitorInstanceSnapshot, silences map[string]model.AlertSilence, recordSuppressedHistory bool) error {
	ctx := context.TODO()
	now := time.Now().UTC()

	// 加载当前已有的实例状态（用于继承 firingAt）
	cur, err := database.State().Find(ctx, bson.M{
		"sourceType": meta.SourceType,
		"sourceId":   meta.SourceID,
	})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var existing []model.AlertInstanceState
	if err := cur.All(ctx, &existing); err != nil {
		return err
	}
	existingByKey := make(map[string]model.AlertInstanceState, len(existing))
	for _, state := range existing {
		existingByKey[state.ScopeKey] = state
	}

	currentScopeKeys := make([]string, 0, len(snapshots))
	seenScopeKeys := make(map[string]bool, len(snapshots))

	for _, snapshot := range snapshots {
		scopeKey := strings.TrimSpace(snapshot.ScopeKey)
		if scopeKey == "" || seenScopeKeys[scopeKey] {
			continue
		}
		seenScopeKeys[scopeKey] = true
		currentScopeKeys = append(currentScopeKeys, scopeKey)

		state := snapshot.AlertState
		if state == "" {
			state = model.AlertStateOK
		}
		firingAt := now
		if old, ok := existingByKey[scopeKey]; ok && old.FiringAt != nil && old.AlertState == model.AlertStateFiring {
			firingAt = *old.FiringAt
		}
		displayName := strings.TrimSpace(snapshot.DisplayName)
		if displayName == "" {
			displayName = scopeKey
		}

		setDoc := bson.M{
			"displayName": displayName,
			"alertState":  state,
			"lastValue":   snapshot.LastValue,
			"lastDetail":  snapshot.LastDetail,
			"updatedAt":   now,
		}
		if state == model.AlertStateFiring {
			if old, ok := existingByKey[scopeKey]; !ok || old.FiringAt == nil || old.AlertState != model.AlertStateFiring {
				setDoc["firingAt"] = now
			}
			setDoc["recoveredAt"] = nil
		} else {
			setDoc["firingAt"] = nil
			if old, ok := existingByKey[scopeKey]; ok && old.AlertState == model.AlertStateFiring {
				setDoc["recoveredAt"] = now
			}
		}

		filter := bson.M{
			"sourceType": meta.SourceType,
			"sourceId":   meta.SourceID,
			"scopeType":  model.SilenceScopeInstance,
			"scopeKey":   scopeKey,
		}
		update := bson.M{
			"$set": setDoc,
			"$setOnInsert": bson.M{
				"_id":       bson.NewObjectID().Hex(),
				"createdAt": now,
			},
		}
		opts := options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After)

		var updatedState model.AlertInstanceState
		if err := database.State().FindOneAndUpdate(ctx, filter, update, opts).Decode(&updatedState); err != nil {
			return fmt.Errorf("upsert instance state (scopeKey=%s) 失败: %v", scopeKey, err)
		}

		if recordSuppressedHistory && state == model.AlertStateFiring {
			if silence, ok := silences[scopeKey]; ok {
				if hasMetMonitorAlertDuration(meta, firingAt, now) && shouldWriteMonitorSuppressedHistory(existingByKey[scopeKey], meta, now) {
					if err := writeMonitorSuppressedHistoryMongo(ctx, meta, silence, snapshot.LastValue); err != nil {
						return err
					}
					// 更新 lastSuppressedAt
					_, _ = database.State().UpdateOne(ctx,
						bson.M{
							"sourceType": meta.SourceType,
							"sourceId":   meta.SourceID,
							"scopeType":  model.SilenceScopeInstance,
							"scopeKey":   scopeKey,
						},
						bson.M{"$set": bson.M{"lastSuppressedAt": now}},
					)
					// 更新本地缓存
					cached := existingByKey[scopeKey]
					cached.LastSuppressedAt = &now
					existingByKey[scopeKey] = cached
				}
			}
		}
	}

	return pruneMonitorInstanceStatesMongo(ctx, meta.SourceType, meta.SourceID, currentScopeKeys)
}

// LoadActiveSilences 加载指定监控对象当前有效的静默规则，以 scopeKey 为索引。
func LoadActiveSilences(sourceType string, sourceID string) (map[string]model.AlertSilence, error) {
	now := time.Now().UTC()
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{
		"kind":       "silence",
		"sourceType": sourceType,
		"sourceId":   sourceID,
		"status":     model.SilenceStatusActive,
		"$or": []bson.M{
			{"mode": model.SilenceModePermanent},
			{"mutedUntil": nil},
			{"mutedUntil": bson.M{"$gt": now}},
		},
	})
	if err != nil {
		return map[string]model.AlertSilence{}, nil
	}
	defer cur.Close(ctx)

	var silences []model.AlertSilence
	if err := cur.All(ctx, &silences); err != nil {
		return nil, err
	}

	result := make(map[string]model.AlertSilence, len(silences))
	for _, silence := range silences {
		result[silence.ScopeKey] = silence
	}
	return result, nil
}

// UpdatePrometheusInstanceStates Prometheus 专用实例状态更新（含静默历史写入）。
// 上游使用 GORM clause.OnConflict upsert；mongo 版逐个 FindOneAndUpdate + upsert。
func UpdatePrometheusInstanceStates(rule *model.AlertRule, results []VectorResult, firing map[string]bool, silences map[string]model.AlertSilence, recordSuppressedHistory bool) error {
	ctx := context.TODO()
	now := time.Now().UTC()

	// 加载当前已有的实例状态（用于继承 firingAt）
	cur, err := database.State().Find(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   rule.ID,
	})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var existing []model.AlertInstanceState
	if err := cur.All(ctx, &existing); err != nil {
		return err
	}
	existingByKey := make(map[string]model.AlertInstanceState, len(existing))
	for _, state := range existing {
		existingByKey[state.ScopeKey] = state
	}

	resultsByScope := make(map[string][]VectorResult)
	currentScopeKeys := make([]string, 0, len(results))
	seenScopeKeys := make(map[string]bool)
	for _, res := range results {
		scopeKey := prometheusScopeKey(res.Metric)
		resultsByScope[scopeKey] = append(resultsByScope[scopeKey], res)
		if !seenScopeKeys[scopeKey] {
			currentScopeKeys = append(currentScopeKeys, scopeKey)
			seenScopeKeys[scopeKey] = true
		}
	}

	suppressedWritten := make(map[string]bool)

	for _, scopeKey := range currentScopeKeys {
		res := selectRepresentativeVectorResult(rule, resultsByScope[scopeKey])
		displayName := prometheusDisplayName(res.Metric)
		state := model.AlertStateOK
		if firing[scopeKey] {
			state = model.AlertStateFiring
		}
		detail, _ := json.Marshal(res.Metric)
		firingAt := now
		if old, ok := existingByKey[scopeKey]; ok && old.FiringAt != nil && old.AlertState == model.AlertStateFiring {
			firingAt = *old.FiringAt
		}

		setDoc := bson.M{
			"displayName": displayName,
			"alertState":  state,
			"lastValue":   res.Value,
			"lastDetail":  string(detail),
			"updatedAt":   now,
		}
		if state == model.AlertStateFiring {
			if old, ok := existingByKey[scopeKey]; !ok || old.FiringAt == nil || old.AlertState != model.AlertStateFiring {
				setDoc["firingAt"] = now
			}
			setDoc["recoveredAt"] = nil
		} else {
			setDoc["firingAt"] = nil
			if old, ok := existingByKey[scopeKey]; ok && old.AlertState == model.AlertStateFiring {
				setDoc["recoveredAt"] = now
			}
		}

		filter := bson.M{
			"sourceType": model.SourceTypePrometheus,
			"sourceId":   rule.ID,
			"scopeType":  model.SilenceScopeInstance,
			"scopeKey":   scopeKey,
		}
		update := bson.M{
			"$set": setDoc,
			"$setOnInsert": bson.M{
				"_id":       bson.NewObjectID().Hex(),
				"createdAt": now,
			},
		}
		opts := options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After)

		if err := database.State().FindOneAndUpdate(ctx, filter, update, opts).Err(); err != nil {
			return fmt.Errorf("upsert prometheus instance state (scopeKey=%s) 失败: %v", scopeKey, err)
		}

		if recordSuppressedHistory && state == model.AlertStateFiring {
			if silence, ok := silences[scopeKey]; ok {
				if !suppressedWritten[scopeKey] && hasMetAlertDuration(rule, firingAt, now) && shouldWriteSuppressedHistory(existingByKey[scopeKey], rule, now) {
					if err := writeSuppressedHistoryMongo(ctx, rule, silence, res.Value); err != nil {
						return err
					}
					_, _ = database.State().UpdateOne(ctx,
						bson.M{
							"sourceType": model.SourceTypePrometheus,
							"sourceId":   rule.ID,
							"scopeType":  model.SilenceScopeInstance,
							"scopeKey":   scopeKey,
						},
						bson.M{"$set": bson.M{"lastSuppressedAt": now}},
					)
					cached := existingByKey[scopeKey]
					cached.LastSuppressedAt = &now
					existingByKey[scopeKey] = cached
					suppressedWritten[scopeKey] = true
				}
			}
		}
	}

	return prunePrometheusInstanceStatesMongo(ctx, rule.ID, currentScopeKeys)
}

// hasMetAlertDuration 检查 Prometheus 规则是否已满足告警持续时间
func hasMetAlertDuration(rule *model.AlertRule, firingAt time.Time, now time.Time) bool {
	duration := rule.Duration
	if duration < rule.Interval {
		duration = rule.Interval
	}
	if duration <= 0 {
		return true
	}
	return now.Sub(firingAt).Seconds() >= float64(duration)
}

// selectRepresentativeVectorResult 从多个同 scopeKey 结果中选取最具代表性的值
func selectRepresentativeVectorResult(rule *model.AlertRule, results []VectorResult) VectorResult {
	if len(results) == 0 {
		return VectorResult{}
	}
	best := results[0]
	bestFiring := matchesAlertCondition(best.Value, rule.Operator, rule.Threshold)
	for _, res := range results[1:] {
		resFiring := matchesAlertCondition(res.Value, rule.Operator, rule.Threshold)
		if resFiring != bestFiring {
			if resFiring {
				best = res
				bestFiring = true
			}
			continue
		}
		if isMoreRepresentativeAlertValue(res.Value, best.Value, rule.Operator, rule.Threshold) {
			best = res
		}
	}
	return best
}

func matchesAlertCondition(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return floatEquals(value, threshold)
	case "!=":
		return !floatEquals(value, threshold)
	default:
		return false
	}
}

func isMoreRepresentativeAlertValue(candidate float64, current float64, operator string, threshold float64) bool {
	switch operator {
	case ">", ">=", "!=":
		return candidate > current
	case "<", "<=":
		return candidate < current
	case "==":
		return absFloat(candidate-threshold) < absFloat(current-threshold)
	default:
		return false
	}
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// prunePrometheusInstanceStatesMongo 删除不再出现在 Prometheus 查询结果中的过时实例状态。
func prunePrometheusInstanceStatesMongo(ctx context.Context, ruleID string, currentScopeKeys []string) error {
	filter := bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   ruleID,
		"scopeType":  model.SilenceScopeInstance,
	}
	if len(currentScopeKeys) > 0 {
		filter["scopeKey"] = bson.M{"$nin": currentScopeKeys}
	}
	_, err := database.State().DeleteMany(ctx, filter)
	return err
}

// loadEarliestPrometheusInstanceFiringAt — see alert_scheduler.go

// CountPrometheusInstanceStates 统计 Prometheus 规则实例的总数 / 告警数 / 静默告警数。
func CountPrometheusInstanceStates(ruleID string) (total, firing, mutedFiring int64, err error) {
	ctx := context.TODO()

	total, err = database.State().CountDocuments(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   ruleID,
	})
	if err != nil {
		return
	}

	firing, err = database.State().CountDocuments(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   ruleID,
		"alertState": model.AlertStateFiring,
	})
	if err != nil {
		return
	}

	silences, silenceErr := LoadActiveSilences(model.SourceTypePrometheus, ruleID)
	if silenceErr != nil {
		err = silenceErr
		return
	}
	if len(silences) == 0 {
		return
	}

	keys := make([]string, 0, len(silences))
	for key := range silences {
		keys = append(keys, key)
	}
	mutedFiring, err = database.State().CountDocuments(ctx, bson.M{
		"sourceType": model.SourceTypePrometheus,
		"sourceId":   ruleID,
		"alertState": model.AlertStateFiring,
		"scopeKey":   bson.M{"$in": keys},
	})
	return
}

// prometheusScopeKey 从 Prometheus metric labels 提取 scopeKey
func prometheusScopeKey(metric map[string]string) string {
	for _, key := range []string{"instance", "host", "hostname", "nodename", "pod", "kubernetes_pod_name"} {
		if val := strings.TrimSpace(metric[key]); val != "" {
			return val
		}
	}
	return fmt.Sprintf("%v", metric)
}

// prometheusDisplayName 从 metric labels 提取友好显示名称
func prometheusDisplayName(metric map[string]string) string {
	name := ""
	for _, key := range []string{"nodename", "hostname", "host"} {
		if val := strings.TrimSpace(metric[key]); val != "" {
			name = val
			break
		}
	}
	instance := strings.TrimSpace(metric["instance"])
	if name != "" && instance != "" {
		return fmt.Sprintf("%s (%s)", name, instance)
	}
	if name != "" {
		return name
	}
	if instance != "" {
		return instance
	}
	for _, key := range []string{"pod", "kubernetes_pod_name"} {
		if val := strings.TrimSpace(metric[key]); val != "" {
			return val
		}
	}
	return fmt.Sprintf("%v", metric)
}

// loadInstanceDisplayNamesMongo 从 State 集合加载已有实例的 displayName 映射
func loadInstanceDisplayNamesMongo(ctx context.Context, sourceType string, sourceID string) (map[string]string, error) {
	cur, err := database.State().Find(ctx, bson.M{
		"sourceType": sourceType,
		"sourceId":   sourceID,
	})
	if err != nil {
		return map[string]string{}, nil
	}
	defer cur.Close(ctx)

	var instances []model.AlertInstanceState
	if err := cur.All(ctx, &instances); err != nil {
		return nil, err
	}
	result := make(map[string]string, len(instances))
	for _, inst := range instances {
		result[inst.ScopeKey] = inst.DisplayName
	}
	return result, nil
}

// resetMonitorStateIfFullySilencedMongo 检查是否所有 Firing 实例都已被静默，
// 若是则重置规则的 alertState 为 OK。
// 上游在 GORM Transaction 中执行；mongo 不支持跨集合原子事务（单 rs 下可用 session，
// 但此处仅做软状态重置，非关键写，改为两步非原子操作即可保持业务正确性）。
func resetMonitorStateIfFullySilencedMongo(ctx context.Context, sourceType string, sourceID string) error {
	// 1. 查询当前所有 Firing 状态的实例
	cur, err := database.State().Find(ctx, bson.M{
		"sourceType": sourceType,
		"sourceId":   sourceID,
		"alertState": model.AlertStateFiring,
	})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var firingStates []model.AlertInstanceState
	if err := cur.All(ctx, &firingStates); err != nil {
		return err
	}
	if len(firingStates) == 0 {
		return nil
	}

	// 2. 查询当前有效的静默
	silences, err := LoadActiveSilences(sourceType, sourceID)
	if err != nil {
		return err
	}
	silenced := make(map[string]bool, len(silences))
	for _, silence := range silences {
		silenced[silence.ScopeKey] = true
	}

	// 3. 若有任何 Firing 实例未被静默，不重置
	for _, state := range firingStates {
		if !silenced[state.ScopeKey] {
			return nil
		}
	}

	// 4. 全量静默 → 重置规则告警状态
	kind, ok := sourceKindMap[sourceType]
	if !ok {
		return fmt.Errorf("unsupported silence source type: %s", sourceType)
	}
	_, err = database.Config().UpdateOne(ctx,
		bson.M{"_id": sourceID, "kind": kind},
		bson.M{"$set": bson.M{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		}},
	)
	return err
}

// ensureSilenceTargetExistsMongo 校验静默目标（规则/任务）是否存在
func ensureSilenceTargetExistsMongo(ctx context.Context, sourceType string, sourceID string) error {
	kind, ok := sourceKindMap[sourceType]
	if !ok {
		return fmt.Errorf("unsupported silence source type: %s", sourceType)
	}
	result := database.Config().FindOne(ctx, bson.M{"_id": sourceID, "kind": kind})
	if result.Err() == mongo.ErrNoDocuments {
		return errors.New("target not found")
	}
	return result.Err()
}

// pruneMonitorInstanceStatesMongo 删除不再出现在当前检查结果中的过时实例状态
func pruneMonitorInstanceStatesMongo(ctx context.Context, sourceType string, sourceID string, currentScopeKeys []string) error {
	filter := bson.M{
		"sourceType": sourceType,
		"sourceId":   sourceID,
		"scopeType":  model.SilenceScopeInstance,
	}
	if len(currentScopeKeys) > 0 {
		filter["scopeKey"] = bson.M{"$nin": currentScopeKeys}
	}
	_, err := database.State().DeleteMany(ctx, filter)
	return err
}

// isMissingSilenceTable 上游 SQLite 特有错误检查；mongo 版无需此检查，始终返回 false。
// 保留函数签名以供潜在调用点使用，避免编译错误。
func isMissingSilenceTable(err error) bool {
	return false
}

// silenceDisplayName 获取静默记录的展示名称
func silenceDisplayName(silence model.AlertSilence) string {
	if silence.DisplayName != "" {
		return silence.DisplayName
	}
	return silence.ScopeKey
}

// silenceSourceName 通过查询 Config 集合获取静默来源名称
func silenceSourceName(ctx context.Context, silence model.AlertSilence) string {
	kind, ok := sourceKindMap[silence.SourceType]
	if !ok {
		return silence.SourceType
	}
	result := database.Config().FindOne(ctx, bson.M{"_id": silence.SourceID, "kind": kind})
	if result.Err() != nil {
		return silence.SourceType
	}
	var doc struct {
		Name string `bson:"name"`
	}
	if err := result.Decode(&doc); err != nil {
		return silence.SourceType
	}
	return doc.Name
}

// shouldWriteSuppressedHistory 判断是否需要写入 Prometheus 规则的静默历史
func shouldWriteSuppressedHistory(existing model.AlertInstanceState, rule *model.AlertRule, now time.Time) bool {
	if existing.ID == "" || existing.AlertState != model.AlertStateFiring || existing.LastSuppressedAt == nil {
		return true
	}
	interval := rule.RemindInterval
	if interval <= 0 {
		interval = 6 * 3600
	}
	return now.Sub(*existing.LastSuppressedAt).Seconds() >= float64(interval)
}

// writeSuppressedHistoryMongo 写入 Prometheus 规则静默历史
func writeSuppressedHistoryMongo(ctx context.Context, rule *model.AlertRule, silence model.AlertSilence, value float64) error {
	content := fmt.Sprintf("任务名称：%s\n静默对象：%s\n当前值：%.2f\n状态：已静默，未发送通知",
		rule.Name, silenceDisplayName(silence), value)
	if silence.MutedUntil != nil {
		content += fmt.Sprintf("\n静默到期：%s", silence.MutedUntil.Local().Format("2006-01-02 15:04:05"))
	} else {
		content += "\n静默到期：永久"
	}
	if silence.Reason != "" {
		content += fmt.Sprintf("\n静默原因：%s", silence.Reason)
	}

	record := model.AlertHistory{
		ID:          bson.NewObjectID().Hex(),
		SourceType:  model.SourceTypePrometheus,
		SourceID:    rule.ID,
		SourceName:  rule.Name,
		AlertState:  model.AlertStateSuppressed,
		Content:     content,
		TriggeredAt: time.Now().UTC(),
	}
	_, err := database.History().InsertOne(ctx, record)
	return err
}

// hasMetMonitorAlertDuration 检查通用监控规则是否已满足告警持续时间
func hasMetMonitorAlertDuration(meta SuppressedHistoryMeta, firingAt time.Time, now time.Time) bool {
	duration := meta.Duration
	if duration < meta.Interval {
		duration = meta.Interval
	}
	if duration <= 0 {
		return true
	}
	return now.Sub(firingAt).Seconds() >= float64(duration)
}

// shouldWriteMonitorSuppressedHistory 判断是否需要写入通用监控规则的静默历史
func shouldWriteMonitorSuppressedHistory(existing model.AlertInstanceState, meta SuppressedHistoryMeta, now time.Time) bool {
	if existing.ID == "" || existing.AlertState != model.AlertStateFiring || existing.LastSuppressedAt == nil {
		return true
	}
	interval := meta.RemindInterval
	if interval <= 0 {
		interval = 6 * 3600
	}
	return now.Sub(*existing.LastSuppressedAt).Seconds() >= float64(interval)
}

// writeMonitorSuppressedHistoryMongo 写入通用监控规则静默历史
func writeMonitorSuppressedHistoryMongo(ctx context.Context, meta SuppressedHistoryMeta, silence model.AlertSilence, value float64) error {
	sourceName := meta.SourceName
	if sourceName == "" {
		sourceName = silenceSourceName(ctx, silence)
	}
	content := fmt.Sprintf("任务名称：%s\n静默对象：%s\n当前值：%.2f\n状态：已静默，未发送通知",
		sourceName, silenceDisplayName(silence), value)
	if silence.MutedUntil != nil {
		content += fmt.Sprintf("\n静默到期：%s", silence.MutedUntil.Local().Format("2006-01-02 15:04:05"))
	} else {
		content += "\n静默到期：永久"
	}
	if silence.Reason != "" {
		content += fmt.Sprintf("\n静默原因：%s", silence.Reason)
	}

	record := model.AlertHistory{
		ID:          bson.NewObjectID().Hex(),
		SourceType:  meta.SourceType,
		SourceID:    meta.SourceID,
		SourceName:  sourceName,
		AlertState:  model.AlertStateSuppressed,
		Content:     content,
		TriggeredAt: time.Now().UTC(),
	}
	_, err := database.History().InsertOne(ctx, record)
	return err
}
