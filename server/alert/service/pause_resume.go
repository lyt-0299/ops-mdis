package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// ErrPauseTargetNotFound 暂停/恢复目标不存在错误
var ErrPauseTargetNotFound = errors.New("pause target not found")

// pauseTarget 描述单种监控对象的暂停/恢复元数据。
// 上游通过 GORM model 指针区分 table；mongo 版通过 kind 区分文档。
type pauseTarget struct {
	sourceType string
	label      string
	kind       string            // alert_config 集合中的 kind 值
	reset      map[string]interface{} // 恢复时需要额外重置的字段（camelCase bson 名称）
}

// pauseTargets 对应上游同名 map，key 为 sourceType 常量。
var pauseTargets = map[string]pauseTarget{
	model.SourceTypeKafka: {
		sourceType: model.SourceTypeKafka,
		label:      "Kafka task",
		kind:       "task",
		reset: map[string]interface{}{
			"alertState": model.AlertStateOK,
		},
	},
	model.SourceTypePrometheus: {
		sourceType: model.SourceTypePrometheus,
		label:      "Prometheus rule",
		kind:       "alertRule",
		reset: map[string]interface{}{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		},
	},
	model.SourceTypePortMonitor: {
		sourceType: model.SourceTypePortMonitor,
		label:      "port monitor",
		kind:       "portRule",
		reset: map[string]interface{}{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		},
	},
	model.SourceTypeSslMonitor: {
		sourceType: model.SourceTypeSslMonitor,
		label:      "SSL monitor",
		kind:       "sslRule",
		reset: map[string]interface{}{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		},
	},
	model.SourceTypeMongoMonitor: {
		sourceType: model.SourceTypeMongoMonitor,
		label:      "MongoDB monitor",
		kind:       "mongoRule",
		reset: map[string]interface{}{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		},
	},
	model.SourceTypeMySQLMonitor: {
		sourceType: model.SourceTypeMySQLMonitor,
		label:      "MySQL monitor",
		kind:       "mysqlRule",
		reset: map[string]interface{}{
			"alertState":  model.AlertStateOK,
			"firingAt":    nil,
			"recoveredAt": nil,
		},
	},
	model.SourceTypeKafkaRebalanceMonitor: {
		sourceType: model.SourceTypeKafkaRebalanceMonitor,
		label:      "Kafka rebalance monitor",
		kind:       "kafkaRebalanceRule",
		reset: map[string]interface{}{
			"alertState": model.AlertStateOK,
			"lastState":  model.KafkaRebalanceStateNormal,
		},
	},
}

// PauseResumeScheduler 临时暂停自动恢复调度器
type PauseResumeScheduler struct {
	stopChan chan struct{}
	once     sync.Once
}

var (
	globalPauseResumeScheduler     *PauseResumeScheduler
	globalPauseResumeSchedulerOnce sync.Once
)

// GetPauseResumeScheduler 获取全局暂停恢复调度器单例
func GetPauseResumeScheduler() *PauseResumeScheduler {
	globalPauseResumeSchedulerOnce.Do(func() {
		globalPauseResumeScheduler = &PauseResumeScheduler{
			stopChan: make(chan struct{}),
		}
	})
	return globalPauseResumeScheduler
}

// StartPauseResumeScheduler 启动全局暂停恢复调度器
func StartPauseResumeScheduler() {
	GetPauseResumeScheduler().Start()
}

// StopPauseResumeScheduler 停止全局暂停恢复调度器
func StopPauseResumeScheduler() {
	if globalPauseResumeScheduler != nil {
		globalPauseResumeScheduler.Stop()
	}
}

func (s *PauseResumeScheduler) Start() {
	log.Println("[PauseResumeScheduler] 启动临时暂停自动恢复调度器")
	s.recoverExpiredPauses()
	go s.run()
}

func (s *PauseResumeScheduler) Stop() {
	s.once.Do(func() {
		close(s.stopChan)
	})
}

func (s *PauseResumeScheduler) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.recoverExpiredPauses()
		}
	}
}

func (s *PauseResumeScheduler) recoverExpiredPauses() {
	for _, target := range pauseTargets {
		if err := resumeExpired(target, time.Now()); err != nil {
			log.Printf("[PauseResumeScheduler] 自动恢复 %s 失败: %v", target.label, err)
		}
	}
}

// PauseMonitor 暂停指定监控对象（支持 manual / temporary 两种模式）
func PauseMonitor(sourceType string, id string, mode string, until *time.Time, reason string) error {
	target, ok := pauseTargets[sourceType]
	if !ok {
		return fmt.Errorf("unsupported pause source type: %s", sourceType)
	}

	now := time.Now()
	if mode == "" {
		mode = model.PauseModeManual
	}

	updates := map[string]interface{}{
		"status":      model.TaskStatusPaused,
		"pauseMode":   mode,
		"pausedAt":    now,
		"pauseReason": reason,
	}

	switch mode {
	case model.PauseModeManual:
		updates["pausedUntil"] = nil
	case model.PauseModeTemporary:
		if until == nil {
			return errors.New("temporary pause requires paused_until")
		}
		if !until.After(now) {
			return errors.New("paused_until must be later than current time")
		}
		updates["pausedUntil"] = until.UTC()
	default:
		return fmt.Errorf("unsupported pause mode: %s", mode)
	}

	ctx := context.TODO()
	result, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": id, "kind": target.kind},
		bson.M{"$set": updates},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrPauseTargetNotFound
	}
	return nil
}

// ResumeMonitor 立即恢复指定监控对象
func ResumeMonitor(sourceType string, id string) error {
	target, ok := pauseTargets[sourceType]
	if !ok {
		return fmt.Errorf("unsupported pause source type: %s", sourceType)
	}

	updates := activeResetUpdates(target)
	ctx := context.TODO()
	result, err := database.Config().UpdateOne(ctx,
		bson.M{"_id": id, "kind": target.kind},
		bson.M{"$set": updates},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrPauseTargetNotFound
	}
	return nil
}

// resumeExpired 批量恢复所有已到期的临时暂停对象。
// 上游 GORM 版用 db.Model(target.model).Where(...).Updates(...)，RowsAffected 判断日志；
// mongo 版用 UpdateMany + ModifiedCount。
func resumeExpired(target pauseTarget, now time.Time) error {
	updates := activeResetUpdates(target)
	ctx := context.TODO()
	result, err := database.Config().UpdateMany(ctx,
		bson.M{
			"kind":        target.kind,
			"status":      model.TaskStatusPaused,
			"pauseMode":   model.PauseModeTemporary,
			"pausedUntil": bson.M{"$ne": nil, "$lte": now},
		},
		bson.M{"$set": updates},
	)
	if err != nil {
		return err
	}
	if result.ModifiedCount > 0 {
		log.Printf("[PauseResumeScheduler] 已自动恢复 %d 条 %s", result.ModifiedCount, target.label)
	}
	return nil
}

// activeResetUpdates 构造恢复为 active 状态时需要 $set 的字段集合
func activeResetUpdates(target pauseTarget) map[string]interface{} {
	updates := map[string]interface{}{
		"status":      model.TaskStatusActive,
		"pauseMode":   "",
		"pausedUntil": nil,
		"pausedAt":    nil,
		"pauseReason": "",
	}
	for key, value := range target.reset {
		updates[key] = value
	}
	return updates
}
