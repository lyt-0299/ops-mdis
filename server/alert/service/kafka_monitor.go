package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/sync/errgroup"
	"sourcecode.mingdao.net/private/MDIS/server/alert/database"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// MonitorService Kafka 监控服务
type MonitorService struct {
	workerPoolSize int
	stopChan       chan struct{}
	stopOnce       sync.Once
	wg             sync.WaitGroup

	// 连接池：按 instance.ID（string）复用 sarama.Client，并按 updatedAt 检测配置变更
	clientPool map[string]*kafkaPoolEntry
	poolLock   sync.RWMutex

	// 任务调度缓存
	lastRunCache map[string]time.Time
}

// kafkaPoolEntry 缓存单个 KafkaInstance 的 sarama.Client 句柄与配置版本。
// updatedAt 用于在 instance 被修改（如更换 Broker、SASL 凭据）后强制重建客户端，
// 避免旧客户端继续使用过期配置造成"配置改了但监控继续坏"的隐蔽失败。
type kafkaPoolEntry struct {
	client    sarama.Client
	updatedAt time.Time
}

// NewMonitorService 创建监控服务
func NewMonitorService(workerPoolSize int) *MonitorService {
	return &MonitorService{
		workerPoolSize: workerPoolSize,
		stopChan:       make(chan struct{}),
		clientPool:     make(map[string]*kafkaPoolEntry),
		lastRunCache:   make(map[string]time.Time),
	}
}

// Start 启动监控服务
func (m *MonitorService) Start() {
	log.Println("启动 Kafka 监控服务...")

	// 创建工作池 - 缓冲区大小为 Worker 数量的 2 倍，避免阻塞
	taskChan := make(chan *model.Task, m.workerPoolSize*2)

	// 启动 worker
	for i := 0; i < m.workerPoolSize; i++ {
		m.wg.Add(1)
		go m.worker(i, taskChan)
	}

	// 主循环：定期获取任务并分发
	m.wg.Add(1)
	go m.dispatcher(taskChan)

	// 启动历史数据自动清理任务
	m.wg.Add(1)
	go m.cleanupLoop()
}

// Stop 停止监控服务（多次调用安全，stopChan 仅关闭一次）
func (m *MonitorService) Stop() {
	log.Println("停止 Kafka 监控服务...")
	m.stopOnce.Do(func() { close(m.stopChan) })
	m.wg.Wait()

	// 关闭所有 Kafka 客户端连接
	m.poolLock.Lock()
	defer m.poolLock.Unlock()
	for id, entry := range m.clientPool {
		if entry != nil && entry.client != nil && !entry.client.Closed() {
			entry.client.Close()
		}
		delete(m.clientPool, id)
	}
}

// dispatcher 任务分发器
func (m *MonitorService) dispatcher(taskChan chan<- *model.Task) {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second) // 缩短扫描间隔，提高精度
	defer ticker.Stop()
	defer close(taskChan)

	for {
		select {
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.dispatchTasks(taskChan)
		}
	}
}

// dispatchTasks 分发需要执行的任务
func (m *MonitorService) dispatchTasks(taskChan chan<- *model.Task) {
	ctx := context.TODO()

	cur, err := database.Config().Find(ctx, bson.M{"kind": "task", "status": "active"})
	if err != nil {
		log.Printf("[Dispatcher] 查询任务失败 (重试中...): %v", err)
		return
	}
	defer cur.Close(ctx)

	var tasks []model.Task
	if err := cur.All(ctx, &tasks); err != nil {
		log.Printf("[Dispatcher] 解析任务失败: %v", err)
		return
	}

	// 批量加载 KafkaInstance 和 Webhook
	m.injectTaskAssociations(ctx, tasks)

	now := time.Now()
	for i := range tasks {
		task := &tasks[i]
		// 内存级去重与调度检查
		lastRun, exists := m.lastRunCache[task.ID]

		// 如果缓存中没有，默认立即执行
		if !exists || now.Sub(lastRun).Seconds() >= float64(task.Interval) {
			select {
			case taskChan <- task:
				// 更新最后运行时间
				m.lastRunCache[task.ID] = now
			default:
				log.Printf("[Dispatcher] 任务队列已满，跳过任务: %s", task.Name)
			}
		}
	}
}

// injectTaskAssociations 批量查询并注入 KafkaInstance 和 Webhook 关联数据
func (m *MonitorService) injectTaskAssociations(ctx context.Context, tasks []model.Task) {
	instIDs := make(map[string]struct{})
	wbIDs := make(map[string]struct{})
	for _, t := range tasks {
		if t.KafkaInstanceID != "" {
			instIDs[t.KafkaInstanceID] = struct{}{}
		}
		if t.WebhookID != "" {
			wbIDs[t.WebhookID] = struct{}{}
		}
	}

	// 查询 KafkaInstance
	instMap := make(map[string]model.KafkaInstance)
	if len(instIDs) > 0 {
		ids := make([]string, 0, len(instIDs))
		for id := range instIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "kafkaInstance"})
		if err == nil {
			defer cur.Close(ctx)
			var insts []model.KafkaInstance
			if cur.All(ctx, &insts) == nil {
				for _, inst := range insts {
					instMap[inst.ID] = inst
				}
			}
		}
	}

	// 查询 Webhook
	wbMap := make(map[string]model.Webhook)
	if len(wbIDs) > 0 {
		ids := make([]string, 0, len(wbIDs))
		for id := range wbIDs {
			ids = append(ids, id)
		}
		cur, err := database.Config().Find(ctx, bson.M{"_id": bson.M{"$in": ids}, "kind": "webhook"})
		if err == nil {
			defer cur.Close(ctx)
			var wbs []model.Webhook
			if cur.All(ctx, &wbs) == nil {
				for _, wb := range wbs {
					wbMap[wb.ID] = wb
				}
			}
		}
	}

	// 注入到任务
	for i := range tasks {
		if inst, ok := instMap[tasks[i].KafkaInstanceID]; ok {
			tasks[i].KafkaInstance = inst
		}
		if tasks[i].WebhookID != "" {
			if wb, ok := wbMap[tasks[i].WebhookID]; ok {
				tasks[i].Webhook = &wb
			}
		}
	}
}

// worker 工作协程
func (m *MonitorService) worker(id int, taskChan <-chan *model.Task) {
	defer m.wg.Done()

	for task := range taskChan {
		// 为每个任务创建一个带超时的 Context
		// 任务最长执行时间：检查间隔的一半，或者默认 30 秒，防止堆积
		timeout := time.Duration(task.Interval/2) * time.Second
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
		if timeout > 60*time.Second {
			timeout = 60 * time.Second
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		// 以单任务为粒度捕获 panic，避免一次采集崩溃导致整个 worker 退出
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Worker %d - 任务 %s 发生 panic: %v", id, task.Name, r)
				}
			}()
			if err := m.ExecuteTask(ctx, task); err != nil {
				log.Printf("Worker %d - 采集任务 %s 失败: %v", id, task.Name, err)
			}
		}()

		cancel()
	}
}

// kafkaEntryUsable 判定缓存的 entry 对给定 instance 是否仍然可用。
func kafkaEntryUsable(entry *kafkaPoolEntry, instance model.KafkaInstance) bool {
	return entry != nil &&
		entry.client != nil &&
		!entry.client.Closed() &&
		entry.updatedAt.Equal(instance.UpdatedAt)
}

// getClient 获取或创建 Kafka Client (连接复用)。
//
// 失效条件（满足任一即重建）：
//  1. 缓存不存在
//  2. 缓存连接已关闭
//  3. instance.UpdatedAt 与缓存的 updatedAt 不一致 —— 说明 Kafka 实例配置已被修改
func (m *MonitorService) getClient(instance model.KafkaInstance) (sarama.Client, error) {
	m.poolLock.RLock()
	entry, ok := m.clientPool[instance.ID]
	m.poolLock.RUnlock()

	if ok && kafkaEntryUsable(entry, instance) {
		return entry.client, nil
	}

	m.poolLock.Lock()
	defer m.poolLock.Unlock()

	// 双重检查
	if entry, ok = m.clientPool[instance.ID]; ok && kafkaEntryUsable(entry, instance) {
		return entry.client, nil
	}

	// 旧 entry 失效（关闭中 / 配置过期）—— 关掉再重建
	if entry != nil && entry.client != nil && !entry.client.Closed() {
		_ = entry.client.Close()
	}

	// 创建新连接
	config := sarama.NewConfig()
	config.Version = sarama.V2_6_0_0
	config.Consumer.Return.Errors = true

	// 设置连接超时
	config.Net.DialTimeout = 5 * time.Second
	config.Net.ReadTimeout = 10 * time.Second
	config.Net.WriteTimeout = 10 * time.Second

	// SASL 认证（含 TLS、Mechanism 校验、SCRAM Client Generator）
	if err := ApplyKafkaSASL(config, instance.SASL); err != nil {
		return nil, err
	}

	newClient, err := sarama.NewClient(instance.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("连接 Kafka 失败: %w", err)
	}

	m.clientPool[instance.ID] = &kafkaPoolEntry{
		client:    newClient,
		updatedAt: instance.UpdatedAt,
	}
	return newClient, nil
}

// EvictKafkaClient 关闭并移除指定实例的缓存客户端。
// 由 KafkaInstance 的 Update / Delete handler 调用，确保下次 getClient 会用最新配置重建。
func EvictKafkaClient(instanceID string) {
	if globalMonitorService == nil || instanceID == "" {
		return
	}
	globalMonitorService.poolLock.Lock()
	defer globalMonitorService.poolLock.Unlock()
	entry, ok := globalMonitorService.clientPool[instanceID]
	if !ok {
		return
	}
	if entry != nil && entry.client != nil && !entry.client.Closed() {
		_ = entry.client.Close()
	}
	delete(globalMonitorService.clientPool, instanceID)
}

// ManualRunTask 手动执行一次任务（id 为 string ObjectID）
func ManualRunTask(id string) error {
	if globalMonitorService == nil {
		return fmt.Errorf("监控服务未启动")
	}

	ctx := context.TODO()
	result := database.Config().FindOne(ctx, bson.M{"_id": id, "kind": "task"})
	if result.Err() != nil {
		return fmt.Errorf("任务不存在: %w", result.Err())
	}
	var task model.Task
	if err := result.Decode(&task); err != nil {
		return fmt.Errorf("解析任务失败: %w", err)
	}

	// 注入关联数据
	tasks := []model.Task{task}
	globalMonitorService.injectTaskAssociations(ctx, tasks)
	task = tasks[0]

	// 手动执行给予较长超时
	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return globalMonitorService.executeTask(execCtx, &task, true)
}

// ExecuteTask 执行单个任务：采集 Lag、保存历史、触发告警
func (m *MonitorService) ExecuteTask(ctx context.Context, task *model.Task) error {
	return m.executeTask(ctx, task, false)
}

func (m *MonitorService) executeTask(ctx context.Context, task *model.Task, manualRefresh bool) error {
	if !manualRefresh && (task.Status != model.TaskStatusActive || !isMonitorActive(model.SourceTypeKafka, task.ID)) {
		logSkipPaused(model.SourceTypeKafka, task.ID, task.Name)
		return nil
	}

	// 检查 Context 是否已取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 获取复用的 Kafka 客户端
	client, err := m.getClient(task.KafkaInstance)
	if err != nil {
		return err
	}
	// 注意：不要关闭 client，因为它在池中

	// Topic 留空 = 监控整个消费组：把该组已提交过 offset 的所有 topic-partition
	// 一起算进来。语义与「Kafka 重平衡」规则里的 Topic 可选保持一致。
	wholeGroup := task.Topic == ""

	// 创建 Consumer Group Coordinator
	coordinator, err := client.Coordinator(task.ConsumerGroup)
	if err != nil {
		return fmt.Errorf("获取 Coordinator 失败: %w", err)
	}

	// 获取 Consumer Group 的 Offset
	request := &sarama.OffsetFetchRequest{
		Version:       1,
		ConsumerGroup: task.ConsumerGroup,
	}
	if wholeGroup {
		// Version >= 2 且不指定任何分区时，broker 返回该组全部已提交的 topic-partition
		request.Version = 2
	} else {
		partitions, err := client.Partitions(task.Topic)
		if err != nil {
			return fmt.Errorf("获取分区列表失败: %w", err)
		}
		for _, partition := range partitions {
			request.AddPartition(task.Topic, partition)
		}
	}

	response, err := coordinator.FetchOffset(request)
	if err != nil {
		return fmt.Errorf("获取 Consumer Offset 失败: %w", err)
	}

	// 把响应摊平成 (topic, partition) → 已提交 offset
	type topicPartition struct {
		topic     string
		partition int32
	}
	committed := make(map[topicPartition]int64)
	for topicName, blocks := range response.Blocks {
		if !wholeGroup && topicName != task.Topic {
			continue
		}
		for partition, block := range blocks {
			if block == nil || block.Err != sarama.ErrNoError {
				continue
			}
			offset := block.Offset
			if offset == -1 {
				// 该分区从没提交过 offset。单 topic 模式沿用旧行为按 0 算（lag 即全量
				// 消息数）；整组模式下必须跳过——否则组里任何一个从没消费过的 topic
				// 都会把它的全部消息计入 Lag，阈值立刻被顶穿。
				if wholeGroup {
					continue
				}
				offset = 0
			}
			committed[topicPartition{topicName, partition}] = offset
		}
	}

	// 并发获取 Log End Offset
	g, _ := errgroup.WithContext(ctx)
	// 限制并发数为 10，避免创建过多 Goroutine
	g.SetLimit(10)

	// 存储 LogEndOffset 的 Map 需要线程安全
	var mu sync.Mutex
	logEndOffsets := make(map[topicPartition]int64)

	for tp := range committed {
		tp := tp // 闭包捕获
		g.Go(func() error {
			leo, err := client.GetOffset(tp.topic, tp.partition, sarama.OffsetNewest)
			if err != nil {
				return fmt.Errorf("获取 %s 分区 %d 的 LogEndOffset 失败: %w", tp.topic, tp.partition, err)
			}

			mu.Lock()
			logEndOffsets[tp] = leo
			mu.Unlock()
			return nil
		})
	}

	// 等待所有并发操作完成
	if err := g.Wait(); err != nil {
		log.Printf("[Task %s] 部分分区 Offset 获取失败: %v", task.Name, err)
	}

	// 计算总 Lag
	var totalLag int64
	for tp, currentOffset := range committed {
		mu.Lock()
		leo, ok := logEndOffsets[tp]
		mu.Unlock()
		if !ok {
			continue
		}

		lag := leo - currentOffset
		if lag < 0 {
			lag = 0
		}
		totalLag += lag
	}

	if !manualRefresh && !isMonitorActive(model.SourceTypeKafka, task.ID) {
		logSkipPaused(model.SourceTypeKafka, task.ID, task.Name)
		return nil
	}

	// 保存 Lag 历史记录
	history := model.LagHistory{
		ID:        bson.NewObjectID().Hex(),
		Kind:      "lagHistory",
		TaskID:    task.ID,
		LagValue:  totalLag,
		Timestamp: time.Now(),
	}

	dbCtx := context.TODO()
	if _, err := database.History().InsertOne(dbCtx, history); err != nil {
		return fmt.Errorf("保存 Lag 历史失败: %w", err)
	}

	if manualRefresh && !isMonitorActive(model.SourceTypeKafka, task.ID) {
		snapshotState := model.AlertStateOK
		if totalLag > task.Threshold {
			snapshotState = model.AlertStateFiring
		}
		if _, err := database.Config().UpdateOne(dbCtx,
			bson.M{"_id": task.ID, "kind": "task"},
			bson.M{"$set": bson.M{
				"alertState": snapshotState,
				"lastLag":    totalLag,
			}},
		); err != nil {
			return fmt.Errorf("更新暂停任务刷新结果失败: %w", err)
		}
		return nil
	}

	// 触发告警检查（同步执行，避免与下一轮采集产生并发写同一 task 的竞态）
	alertEngine := GetAlertEngine()
	alertEngine.ProcessLag(task, totalLag)

	return nil
}

// 全局监控服务实例
var globalMonitorService *MonitorService

// StartMonitor 启动全局监控服务
func StartMonitor(workerPoolSize int) {
	globalMonitorService = NewMonitorService(workerPoolSize)
	globalMonitorService.Start()
}

// StopMonitor 停止全局监控服务
func StopMonitor() {
	if globalMonitorService != nil {
		globalMonitorService.Stop()
	}
}

// cleanupLoop 定期清理过期数据
func (m *MonitorService) cleanupLoop() {
	defer m.wg.Done()
	// 启动 1 分钟后先执行一次，之后每 10 分钟执行一次
	initialTimer := time.NewTimer(1 * time.Minute)
	ticker := time.NewTicker(10 * time.Minute)
	defer initialTimer.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			return
		case <-initialTimer.C:
			m.performCleanup()
		case <-ticker.C:
			m.performCleanup()
		}
	}
}

// performCleanup 执行清理逻辑: 保留最近 12 小时的数据
func (m *MonitorService) performCleanup() {
	retentionThreshold := time.Now().Add(-12 * time.Hour)
	ctx := context.TODO()
	if _, err := database.History().DeleteMany(ctx, bson.M{
		"timestamp": bson.M{"$lt": retentionThreshold},
	}); err != nil {
		log.Printf("[Cleanup] 清理过期数据失败: %v", err)
	}
}
