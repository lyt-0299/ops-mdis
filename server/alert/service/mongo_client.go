package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// MongoPoolClient MongoDB 连接客户端（含连接池复用）
type MongoPoolClient struct {
	instance  *model.MongoInstance
	client    *mongo.Client
	updatedAt time.Time
}

// 连接池：按 MongoInstance.ID（string）复用
var (
	mongoClientPool = make(map[string]*MongoPoolClient)
	mongoPoolLock   sync.RWMutex
)

// 直连客户端缓存：用于按节点查询 oplog window，缓存键为 "instanceID:memberName"
var (
	directClientPool = make(map[string]*mongo.Client)
	directPoolLock   sync.Mutex
)

// NewMongoPoolClient 创建或复用 MongoDB 客户端
func NewMongoPoolClient(inst *model.MongoInstance) (*MongoPoolClient, error) {
	// 尝试从缓存复用
	mongoPoolLock.RLock()
	if entry, ok := mongoClientPool[inst.ID]; ok && inst.ID != "" {
		if entry.updatedAt.Equal(inst.UpdatedAt) {
			mongoPoolLock.RUnlock()
			return entry, nil
		}
	}
	mongoPoolLock.RUnlock()

	// 创建新客户端
	mongoPoolLock.Lock()
	defer mongoPoolLock.Unlock()

	// 双重检查
	if entry, ok := mongoClientPool[inst.ID]; ok && inst.ID != "" {
		if entry.updatedAt.Equal(inst.UpdatedAt) {
			return entry, nil
		}
		// 配置有变化，断开旧主连接
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = entry.client.Disconnect(ctx)
		cancel()
		// directClientPool 是按 instanceID 索引的副本节点直连缓存，
		// 主连接配置过期时这些 direct client 也用的是旧凭据/host，必须一并失效。
		evictDirectClients(inst.ID)
	}

	client, err := buildMongoClient(inst)
	if err != nil {
		return nil, err
	}

	pc := &MongoPoolClient{
		instance:  inst,
		client:    client,
		updatedAt: inst.UpdatedAt,
	}

	if inst.ID != "" {
		mongoClientPool[inst.ID] = pc
	}

	return pc, nil
}

// normalizeMongoURI 填掉"内嵌凭据+带库名+无 authSource"这一唯一会认证失败的组合：
// 此时驱动会拿库名当 authSource。注入 authSource=authDB(缺省 admin)。
func normalizeMongoURI(uri, authDB string) string {
	u, err := url.Parse(uri)
	if err != nil || u.User == nil { // 解析失败或无内嵌凭据 → 走 SetAuth 路径，不动
		return uri
	}
	if strings.TrimPrefix(u.Path, "/") == "" { // 无库名 → 驱动默认 admin，不动
		return uri
	}
	q := u.Query()
	if q.Get("authSource") != "" { // 已指定 → 不动
		return uri
	}
	src := authDB
	if src == "" {
		src = "admin"
	}
	q.Set("authSource", src)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildMongoClient 根据 MongoInstance 配置构建 mongo.Client
func buildMongoClient(inst *model.MongoInstance) (*mongo.Client, error) {
	opts := options.Client().ApplyURI(normalizeMongoURI(inst.URI, inst.AuthDB))

	// 如果独立字段提供了凭据（且 URI 中未指定），则覆盖
	if inst.Username != "" && inst.Password != "" {
		credential := options.Credential{
			Username:   inst.Username,
			Password:   inst.Password,
			AuthSource: inst.AuthDB,
		}
		opts.SetAuth(credential)
	}

	// TLS 配置
	if inst.TLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: inst.SkipSSL,
		}
		opts.SetTLSConfig(tlsConfig)
	}

	// 连接池与超时配置
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetServerSelectionTimeout(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("创建 MongoDB 客户端失败: %w", err)
	}

	// Ping 测试
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	return client, nil
}

// TestConnection 测试 MongoDB 连接
func (c *MongoPoolClient) TestConnection(ctx context.Context) error {
	if err := c.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("Ping 失败: %w", err)
	}
	return nil
}

// Close 断开底层连接，主要防范离群的临时检测客户端泄露句柄
func (c *MongoPoolClient) Close(ctx context.Context) error {
	if c.client != nil {
		return c.client.Disconnect(ctx)
	}
	return nil
}

// ReplSetStatus replSetGetStatus 返回结构
type ReplSetStatus struct {
	Set     string          `bson:"set"`
	Members []ReplSetMember `bson:"members"`
}

// ReplSetMember 副本集成员信息
type ReplSetMember struct {
	Name       string    `bson:"name"`
	StateStr   string    `bson:"stateStr"`   // PRIMARY, SECONDARY, ARBITER, RECOVERING, DOWN 等
	Health     float64   `bson:"health"`     // 1=健康, 0=不健康
	OptimeDate time.Time `bson:"optimeDate"` // Oplog 最新时间
	Self       bool      `bson:"self"`
}

// OplogLagResult 单个节点的 Oplog 延迟信息
type OplogLagResult struct {
	MemberName string  `json:"member_name"`
	StateStr   string  `json:"state_str"`
	LagSeconds float64 `json:"lag_seconds"`
}

// GetReplSetStatus 获取副本集状态
func (c *MongoPoolClient) GetReplSetStatus(ctx context.Context) (*ReplSetStatus, error) {
	adminDB := c.client.Database("admin")
	result := adminDB.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})

	var status ReplSetStatus
	if err := result.Decode(&status); err != nil {
		return nil, fmt.Errorf("解析 replSetGetStatus 结果失败: %w", err)
	}

	return &status, nil
}

// GetOplogLag 获取各 Secondary 相对 Primary 的 Oplog 延迟
func (c *MongoPoolClient) GetOplogLag(ctx context.Context) ([]OplogLagResult, error) {
	status, err := c.GetReplSetStatus(ctx)
	if err != nil {
		return nil, err
	}

	// 找到 Primary 的 optimeDate
	var primaryOptimeDate time.Time
	var primaryFound bool
	for _, m := range status.Members {
		if m.StateStr == "PRIMARY" {
			primaryOptimeDate = m.OptimeDate
			primaryFound = true
			break
		}
	}

	if !primaryFound {
		return nil, fmt.Errorf("副本集中未找到 PRIMARY 节点")
	}

	var results []OplogLagResult
	for _, m := range status.Members {
		if m.StateStr == "SECONDARY" {
			lag := primaryOptimeDate.Sub(m.OptimeDate).Seconds()
			if lag < 0 {
				lag = 0
			}
			results = append(results, OplogLagResult{
				MemberName: m.Name,
				StateStr:   m.StateStr,
				LagSeconds: lag,
			})
		}
	}

	return results, nil
}

// OplogWindowResult 单个节点的 Oplog 保存时间信息
type OplogWindowResult struct {
	MemberName  string  `json:"member_name"`
	StateStr    string  `json:"state_str"`
	WindowHours float64 `json:"window_hours"`
}

// GetOplogWindows 获取各副本集节点独立的 Oplog 保存窗口
func (c *MongoPoolClient) GetOplogWindows(ctx context.Context) ([]OplogWindowResult, error) {
	status, err := c.GetReplSetStatus(ctx)
	if err != nil {
		return nil, err
	}

	var results []OplogWindowResult
	for _, m := range status.Members {
		// 只查询健康的数据节点，其余状态（ARBITER、DOWN、RECOVERING、(not reachable/healthy) 等）一律跳过
		if m.StateStr != "PRIMARY" && m.StateStr != "SECONDARY" {
			continue
		}

		windowHours, err := c.getSingleNodeOplogWindow(ctx, m.Name)
		if err != nil {
			log.Printf("[MongoMonitor] 获取节点 %s oplog_window 失败: %v", m.Name, err)
			continue
		}

		results = append(results, OplogWindowResult{
			MemberName:  m.Name,
			StateStr:    m.StateStr,
			WindowHours: windowHours,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("未能获取到任何节点的 Oplog Window")
	}

	return results, nil
}

func (c *MongoPoolClient) getSingleNodeOplogWindow(ctx context.Context, memberName string) (float64, error) {
	cacheKey := fmt.Sprintf("%s:%s", c.instance.ID, memberName)

	// 尝试复用缓存的直连客户端
	directPoolLock.Lock()
	tmpClient, cached := directClientPool[cacheKey]
	directPoolLock.Unlock()

	if !cached {
		opts := options.Client().ApplyURI(c.instance.URI)
		if c.instance.Username != "" && c.instance.Password != "" {
			credential := options.Credential{
				Username:   c.instance.Username,
				Password:   c.instance.Password,
				AuthSource: c.instance.AuthDB,
			}
			opts.SetAuth(credential)
		}
		if c.instance.TLS {
			opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: c.instance.SkipSSL})
		}

		opts.SetHosts([]string{memberName})
		opts.SetDirect(true)
		opts.SetReplicaSet("") // 禁用 ReplicaSet 发现，直接连接单个节点
		opts.SetConnectTimeout(5 * time.Second)
		opts.SetServerSelectionTimeout(5 * time.Second)

		var err error
		tmpClient, err = mongo.Connect(opts)
		if err != nil {
			return 0, fmt.Errorf("创建直连客户端失败: %w", err)
		}

		directPoolLock.Lock()
		directClientPool[cacheKey] = tmpClient
		directPoolLock.Unlock()
	}

	localDB := tmpClient.Database("local")
	oplogColl := localDB.Collection("oplog.rs")

	// 获取最旧的 Oplog 记录
	oldestOpts := options.FindOne().SetSort(bson.D{{Key: "$natural", Value: 1}})
	var oldest bson.M
	if err := oplogColl.FindOne(ctx, bson.D{}, oldestOpts).Decode(&oldest); err != nil {
		// 查询失败时清除缓存，下次重建连接
		directPoolLock.Lock()
		delete(directClientPool, cacheKey)
		directPoolLock.Unlock()
		_ = tmpClient.Disconnect(context.Background())
		return 0, fmt.Errorf("查询最旧 Oplog 失败: %w", err)
	}

	// 获取最新的 Oplog 记录
	newestOpts := options.FindOne().SetSort(bson.D{{Key: "$natural", Value: -1}})
	var newest bson.M
	if err := oplogColl.FindOne(ctx, bson.D{}, newestOpts).Decode(&newest); err != nil {
		directPoolLock.Lock()
		delete(directClientPool, cacheKey)
		directPoolLock.Unlock()
		_ = tmpClient.Disconnect(context.Background())
		return 0, fmt.Errorf("查询最新 Oplog 失败: %w", err)
	}

	// 解析 Timestamp
	oldestTs, ok := oldest["ts"].(bson.Timestamp)
	if !ok {
		return 0, fmt.Errorf("无法解析 ts")
	}
	newestTs, ok := newest["ts"].(bson.Timestamp)
	if !ok {
		return 0, fmt.Errorf("无法解析 ts")
	}

	diffSeconds := float64(newestTs.T) - float64(oldestTs.T)
	if diffSeconds < 0 {
		diffSeconds = 0
	}
	windowHours := diffSeconds / 3600.0

	return windowHours, nil
}

// ReplicaStatusDetail 用于序列化到 LastDetail 的副本集状态详情
type ReplicaStatusDetail struct {
	SetName string                    `json:"set_name"`
	Members []ReplicaStatusMemberInfo `json:"members"`
}

// ReplicaStatusMemberInfo 成员信息（用于 JSON 序列化）
type ReplicaStatusMemberInfo struct {
	Name     string  `json:"name"`
	StateStr string  `json:"state_str"`
	Health   float64 `json:"health"`
}

// CheckReplicaStatus 检查副本集状态，返回是否异常 + 详情 + 异常描述
func (c *MongoPoolClient) CheckReplicaStatus(ctx context.Context) (abnormal bool, detail *ReplicaStatusDetail, errMsg string, err error) {
	status, err := c.GetReplSetStatus(ctx)
	if err != nil {
		return true, nil, "", err
	}

	// 正常状态集合
	normalStates := map[string]bool{
		"PRIMARY":   true,
		"SECONDARY": true,
		"ARBITER":   true,
	}

	detail = &ReplicaStatusDetail{
		SetName: status.Set,
	}

	var abnormalMembers []string
	for _, m := range status.Members {
		detail.Members = append(detail.Members, ReplicaStatusMemberInfo{
			Name:     m.Name,
			StateStr: m.StateStr,
			Health:   m.Health,
		})

		if !normalStates[m.StateStr] || m.Health != 1 {
			abnormalMembers = append(abnormalMembers, fmt.Sprintf("%s(%s)", m.Name, m.StateStr))
		}
	}

	if len(abnormalMembers) > 0 {
		sort.Strings(abnormalMembers)
		return true, detail, fmt.Sprintf("异常成员: %v", abnormalMembers), nil
	}

	return false, detail, "", nil
}

// CloseMongoClient 关闭指定实例的连接（包括主连接和直连客户端缓存）。
// 由 handler 在 instance 更新 / 删除时调用。
func CloseMongoClient(instanceID string) {
	// 1. 关闭主连接池中的客户端
	mongoPoolLock.Lock()
	if entry, ok := mongoClientPool[instanceID]; ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := entry.client.Disconnect(ctx); err != nil {
			log.Printf("[MongoDB] 关闭实例 %s 连接失败: %v", instanceID, err)
		}
		cancel()
		delete(mongoClientPool, instanceID)
	}
	mongoPoolLock.Unlock()

	// 2. 清理直连客户端缓存
	evictDirectClients(instanceID)
}

// evictDirectClients 关闭并删除给定 instanceID 对应的所有副本节点直连客户端。
// 调用方应已释放 mongoPoolLock（本函数内部获取 directPoolLock）。
func evictDirectClients(instanceID string) {
	prefix := fmt.Sprintf("%s:", instanceID)

	var toClose []*mongo.Client
	directPoolLock.Lock()
	for key, client := range directClientPool {
		if strings.HasPrefix(key, prefix) {
			if client != nil {
				toClose = append(toClose, client)
			}
			delete(directClientPool, key)
		}
	}
	directPoolLock.Unlock()

	for _, client := range toClose {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = client.Disconnect(ctx)
		cancel()
	}
}
