package service

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

type MySQLNodeCheck struct {
	Address           string  `json:"address"`
	Reachable         bool    `json:"reachable"`
	Role              string  `json:"role"`
	ReadOnly          bool    `json:"read_only"`
	SuperReadOnly     bool    `json:"super_read_only"`
	ServerUUID        string  `json:"server_uuid"`
	Hostname          string  `json:"hostname,omitempty"`
	Port              int     `json:"port,omitempty"`
	ReplicationIORun  string  `json:"replication_io_running,omitempty"`
	ReplicationSQLRun string  `json:"replication_sql_running,omitempty"`
	ReplicationLag    *int64  `json:"replication_lag,omitempty"`
	MGRMemberState    string  `json:"mgr_member_state,omitempty"`
	MGRMemberRole     string  `json:"mgr_member_role,omitempty"`
	MGRTransactionsQ  float64 `json:"mgr_transactions_in_queue,omitempty"`
	MGRApplierQueue   float64 `json:"mgr_applier_queue,omitempty"`
	Error             string  `json:"error,omitempty"`
}

type MySQLCheckResult struct {
	Architecture string           `json:"architecture"`
	Nodes        []MySQLNodeCheck `json:"nodes"`
	Summary      string           `json:"summary"`
}

type MySQLClient struct {
	instance *model.MySQLInstance
}

// mysqlPoolEntry 缓存单个实例 + 节点的 *sql.DB 句柄。
type mysqlPoolEntry struct {
	db        *sql.DB
	updatedAt time.Time
}

var (
	mysqlClientPool = make(map[string]*mysqlPoolEntry)
	mysqlPoolLock   sync.Mutex
	// mysqlOpenFn 暴露给测试用以注入伪造的 *sql.DB；生产路径默认走 openMySQL。
	mysqlOpenFn = openMySQL
)

func mysqlPoolKey(instanceID string, node string) string {
	return fmt.Sprintf("%s:%s", instanceID, node)
}

// closeMySQLPoolEntry 不持锁地关闭一个池条目。
func closeMySQLPoolEntry(entry *mysqlPoolEntry) {
	if entry != nil && entry.db != nil {
		_ = entry.db.Close()
	}
}

func NewMySQLClient(instance *model.MySQLInstance) *MySQLClient {
	return &MySQLClient{instance: instance}
}

func (c *MySQLClient) TestConnection(ctx context.Context) error {
	for _, node := range c.instance.Nodes {
		db, cleanup, err := c.acquire(node)
		if err != nil {
			return err
		}
		err = db.PingContext(ctx)
		cleanup()
		if err != nil {
			return fmt.Errorf("%s 连接失败: %w", node, err)
		}
	}
	return nil
}

func (c *MySQLClient) Check(ctx context.Context, monitorType string, lagThreshold, mgrQueueThreshold int) (bool, float64, MySQLCheckResult) {
	switch monitorType {
	case model.MySQLMonitorTypeReplicationLag:
		return c.checkReplication(ctx, lagThreshold, true, false)
	case model.MySQLMonitorTypeReplicationStatus:
		return c.checkReplication(ctx, lagThreshold, false, true)
	case model.MySQLMonitorTypeMGRNodeStatus:
		return c.checkMGR(ctx, mgrQueueThreshold, true, false)
	case model.MySQLMonitorTypeMGRSyncStatus:
		return c.checkMGR(ctx, mgrQueueThreshold, false, true)
	default:
		return c.checkConnectivity(ctx)
	}
}

func (c *MySQLClient) checkConnectivity(ctx context.Context) (bool, float64, MySQLCheckResult) {
	result := MySQLCheckResult{Architecture: c.instance.Architecture}
	triggered := false
	for _, node := range c.instance.Nodes {
		check := MySQLNodeCheck{Address: node}
		db, cleanup, err := c.acquire(node)
		if err != nil {
			check.Error = err.Error()
			triggered = true
			result.Nodes = append(result.Nodes, check)
			continue
		}
		if err := db.PingContext(ctx); err != nil {
			check.Error = err.Error()
			triggered = true
		} else {
			check.Reachable = true
			c.fillServerInfo(ctx, db, &check)
		}
		cleanup()
		result.Nodes = append(result.Nodes, check)
	}
	result.Summary = buildMySQLSummary(result.Nodes, "连通性检查")
	return triggered, 0, result
}

func (c *MySQLClient) checkReplication(ctx context.Context, lagThreshold int, checkLag, checkStatus bool) (bool, float64, MySQLCheckResult) {
	result := MySQLCheckResult{Architecture: model.MySQLArchitectureReplication}
	triggered := false
	maxLag := float64(0)
	replicaCount := 0
	primaryCount := 0

	for _, node := range c.instance.Nodes {
		check := MySQLNodeCheck{Address: node}
		db, cleanup, err := c.acquire(node)
		if err != nil {
			check.Error = err.Error()
			triggered = true
			result.Nodes = append(result.Nodes, check)
			continue
		}
		if err := db.PingContext(ctx); err != nil {
			check.Error = err.Error()
			triggered = true
			cleanup()
			result.Nodes = append(result.Nodes, check)
			continue
		}
		check.Reachable = true
		c.fillServerInfo(ctx, db, &check)
		replRows, err := showReplicaStatus(ctx, db)
		if err != nil {
			check.Error = appendCheckError(check.Error, err.Error())
			triggered = true
		} else if len(replRows) > 0 {
			replicaCount++
			check.Role = "REPLICA"
			row := replRows[0]
			check.ReplicationIORun = firstNonEmpty(row["Replica_IO_Running"], row["Slave_IO_Running"])
			check.ReplicationSQLRun = firstNonEmpty(row["Replica_SQL_Running"], row["Slave_SQL_Running"])
			if lag, ok := parseNullableInt(firstNonEmpty(row["Seconds_Behind_Source"], row["Seconds_Behind_Master"])); ok {
				check.ReplicationLag = &lag
				if float64(lag) > maxLag {
					maxLag = float64(lag)
				}
				if checkLag && lagThreshold > 0 && lag > int64(lagThreshold) {
					triggered = true
				}
			} else if checkLag {
				triggered = true
				check.Error = appendCheckError(check.Error, "复制延迟为空")
			}
			if checkStatus && (!isReplicationRunning(check.ReplicationIORun) || !isReplicationRunning(check.ReplicationSQLRun)) {
				triggered = true
			}
		} else {
			if !check.ReadOnly && !check.SuperReadOnly {
				primaryCount++
				check.Role = "PRIMARY"
			} else {
				check.Role = "UNKNOWN"
			}
		}
		cleanup()
		result.Nodes = append(result.Nodes, check)
	}

	missingReplica := replicaCount == 0
	missingPrimary := primaryCount == 0
	if missingReplica || missingPrimary {
		triggered = true
	}
	label := replicationCheckLabel(checkLag, checkStatus)
	summary := fmt.Sprintf("%s：主节点 %d，从节点 %d，最大延迟 %.0fs", label, primaryCount, replicaCount, maxLag)
	if missingPrimary {
		summary += "（缺少主节点）"
	}
	if missingReplica {
		summary += "（未发现从节点）"
	}
	result.Summary = summary
	return triggered, maxLag, result
}

func (c *MySQLClient) checkMGR(ctx context.Context, queueThreshold int, checkNode, checkSync bool) (bool, float64, MySQLCheckResult) {
	result := MySQLCheckResult{Architecture: model.MySQLArchitectureMGR}
	triggered := false
	maxQueue := float64(0)
	primaryCount := 0
	secondaryCount := 0
	roleInvalid := false
	countedMembers := make(map[string]bool)
	membersLoaded := false
	queueDataLoaded := false

	// 跨节点合并视图：成员清单与统计数据均按 MEMBER_ID 先到先得，
	// 避免抖动节点的不完整视图覆盖更新节点视图，也避免遗漏只在新视图中可见的成员。
	mergedMembers := make(map[string]map[string]string)
	memberOrder := make([]string, 0)
	mergedStats := make(map[string]map[string]string)

	for _, node := range c.instance.Nodes {
		db, cleanup, err := c.acquire(node)
		if err != nil {
			if checkNode {
				result.Nodes = append(result.Nodes, MySQLNodeCheck{Address: node, Error: err.Error()})
				triggered = true
			}
			continue
		}
		if err := db.PingContext(ctx); err != nil {
			if checkNode {
				result.Nodes = append(result.Nodes, MySQLNodeCheck{Address: node, Error: err.Error()})
				triggered = true
			}
			cleanup()
			continue
		}
		nodeMembers, err := queryMGRMembers(ctx, db)
		if err != nil {
			if checkNode {
				result.Nodes = append(result.Nodes, MySQLNodeCheck{Address: node, Reachable: true, Error: err.Error()})
				triggered = true
			}
			cleanup()
			continue
		}
		stats, statsErr := queryMGRStats(ctx, db)
		if statsErr == nil && len(stats) > 0 {
			queueDataLoaded = true
			for memberID, stat := range stats {
				if _, exists := mergedStats[memberID]; !exists {
					mergedStats[memberID] = stat
				}
			}
		}
		cleanup()
		membersLoaded = true

		for _, m := range nodeMembers {
			id := m["MEMBER_ID"]
			if id == "" {
				id = m["MEMBER_HOST"] + ":" + m["MEMBER_PORT"]
			}
			if _, exists := mergedMembers[id]; !exists {
				mergedMembers[id] = m
				memberOrder = append(memberOrder, id)
			}
		}
	}

	membersByHost := make(map[string]MySQLNodeCheck)
	for _, id := range memberOrder {
		member := mergedMembers[id]
		address := member["MEMBER_HOST"] + ":" + member["MEMBER_PORT"]
		check := MySQLNodeCheck{
			Address:        address,
			Reachable:      true,
			Role:           member["MEMBER_ROLE"],
			MGRMemberState: member["MEMBER_STATE"],
			MGRMemberRole:  member["MEMBER_ROLE"],
			ServerUUID:     member["MEMBER_ID"],
		}
		if checkNode {
			primaryDelta, secondaryDelta, invalid := countMGRMemberRole(countedMembers, member["MEMBER_ID"], address, check.MGRMemberRole)
			primaryCount += primaryDelta
			secondaryCount += secondaryDelta
			if invalid {
				roleInvalid = true
				check.Error = appendCheckError(check.Error, "角色异常")
			}
		}
		if stat, ok := mergedStats[member["MEMBER_ID"]]; ok {
			check.MGRTransactionsQ = parseFloat(stat["COUNT_TRANSACTIONS_IN_QUEUE"])
			check.MGRApplierQueue = parseFloat(stat["COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE"])
			if check.MGRTransactionsQ > maxQueue {
				maxQueue = check.MGRTransactionsQ
			}
			if check.MGRApplierQueue > maxQueue {
				maxQueue = check.MGRApplierQueue
			}
		}
		if checkNode && check.MGRMemberState != "ONLINE" {
			triggered = true
		}
		if checkSync && queueThreshold > 0 && (check.MGRTransactionsQ > float64(queueThreshold) || check.MGRApplierQueue > float64(queueThreshold)) {
			triggered = true
		}
		membersByHost[address] = check
	}

	if checkNode && len(membersByHost) == 0 {
		triggered = true
	}
	if checkNode && (!membersLoaded || mgrRoleTopologyInvalid(primaryCount, secondaryCount, len(membersByHost), roleInvalid)) {
		triggered = true
	}
	if checkSync && mgrSyncDataUnavailable(queueDataLoaded, len(membersByHost)) {
		triggered = true
		if len(membersByHost) > 0 {
			for key, member := range membersByHost {
				member.Error = appendCheckError(member.Error, "队列数据不可用")
				membersByHost[key] = member
			}
		} else if len(result.Nodes) == 0 {
			address := "MGR"
			if len(c.instance.Nodes) > 0 {
				address = c.instance.Nodes[0]
			}
			result.Nodes = append(result.Nodes, MySQLNodeCheck{Address: address, Error: "无法读取 MGR 成员或队列数据"})
		}
	}
	if len(membersByHost) > 0 {
		for _, member := range membersByHost {
			result.Nodes = append(result.Nodes, member)
		}
	}
	if checkSync && mgrSyncDataUnavailable(queueDataLoaded, len(membersByHost)) {
		result.Summary = fmt.Sprintf("%s：成员或队列数据不可用", mgrCheckLabel(checkNode, checkSync))
	} else if checkNode {
		result.Summary = fmt.Sprintf("%s：成员 %d，PRIMARY %d，SECONDARY %d，最大队列 %.0f", mgrCheckLabel(checkNode, checkSync), len(result.Nodes), primaryCount, secondaryCount, maxQueue)
	} else {
		result.Summary = fmt.Sprintf("%s：成员 %d，最大队列 %.0f", mgrCheckLabel(checkNode, checkSync), len(result.Nodes), maxQueue)
	}
	return triggered, maxQueue, result
}

// acquire 返回 *sql.DB 与 cleanup 函数。
// 池化句柄的 cleanup 是 no-op；瞬时实例（ID==""，例如测试连接）会在 cleanup 中关闭。
//
// 并发模型：删除 stale entry 时不释放锁，将旧句柄异步关闭，避免"释放锁→关旧句柄→重获锁"
// 期间出现 (1) 别的 goroutine 抢着新建 entry、(2) 自己后续覆盖对方 entry 导致句柄泄漏。
func (c *MySQLClient) acquire(node string) (*sql.DB, func(), error) {
	if c.instance.ID == "" {
		db, err := mysqlOpenFn(c.instance, node)
		if err != nil {
			return nil, func() {}, err
		}
		return db, func() { _ = db.Close() }, nil
	}

	key := mysqlPoolKey(c.instance.ID, node)

	mysqlPoolLock.Lock()
	defer mysqlPoolLock.Unlock()

	if entry, ok := mysqlClientPool[key]; ok {
		if entry.updatedAt.Equal(c.instance.UpdatedAt) {
			return entry.db, func() {}, nil
		}
		// 配置变化：从 map 移除并交后台关闭，自己继续在锁内重建。
		delete(mysqlClientPool, key)
		go closeMySQLPoolEntry(entry)
	}

	db, err := mysqlOpenFn(c.instance, node)
	if err != nil {
		return nil, func() {}, err
	}
	mysqlClientPool[key] = &mysqlPoolEntry{db: db, updatedAt: c.instance.UpdatedAt}
	return db, func() {}, nil
}

// EvictMySQLClientPool 在实例更新或删除时清理对应连接池条目。
func EvictMySQLClientPool(instanceID string) {
	if instanceID == "" {
		return
	}
	prefix := fmt.Sprintf("%s:", instanceID)
	mysqlPoolLock.Lock()
	var toClose []*mysqlPoolEntry
	for key, entry := range mysqlClientPool {
		if strings.HasPrefix(key, prefix) {
			toClose = append(toClose, entry)
			delete(mysqlClientPool, key)
		}
	}
	mysqlPoolLock.Unlock()
	for _, entry := range toClose {
		closeMySQLPoolEntry(entry)
	}
}

func openMySQL(instance *model.MySQLInstance, node string) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = instance.Username
	cfg.Passwd = instance.Password
	cfg.Net = "tcp"
	cfg.Addr = node
	cfg.DBName = instance.Database
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 8 * time.Second
	cfg.WriteTimeout = 8 * time.Second
	if instance.TLS {
		if instance.SkipSSL {
			cfg.TLSConfig = "skip-verify"
		} else {
			cfg.TLSConfig = "true"
		}
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func (c *MySQLClient) fillServerInfo(ctx context.Context, db *sql.DB, check *MySQLNodeCheck) {
	var uuid, hostname string
	var port int
	var readOnly, superReadOnly int
	row := db.QueryRowContext(ctx, "SELECT @@server_uuid, @@hostname, @@port, @@read_only, @@super_read_only")
	if err := row.Scan(&uuid, &hostname, &port, &readOnly, &superReadOnly); err != nil {
		check.Error = appendCheckError(check.Error, "读取 server 状态失败: "+err.Error())
		return
	}
	check.ServerUUID = uuid
	check.Hostname = hostname
	check.Port = port
	check.ReadOnly = readOnly == 1
	check.SuperReadOnly = superReadOnly == 1
}

func showReplicaStatus(ctx context.Context, db *sql.DB) ([]map[string]string, error) {
	rows, err := queryRowsAsMaps(ctx, db, "SHOW REPLICA STATUS")
	if err == nil {
		return rows, nil
	}
	return queryRowsAsMaps(ctx, db, "SHOW SLAVE STATUS")
}

func queryMGRMembers(ctx context.Context, db *sql.DB) ([]map[string]string, error) {
	return queryRowsAsMaps(ctx, db, "SELECT MEMBER_ID, MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, MEMBER_ROLE FROM performance_schema.replication_group_members")
}

func queryMGRStats(ctx context.Context, db *sql.DB) (map[string]map[string]string, error) {
	rows, err := queryRowsAsMaps(ctx, db, "SELECT MEMBER_ID, COUNT_TRANSACTIONS_IN_QUEUE, COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE FROM performance_schema.replication_group_member_stats")
	if err != nil {
		return nil, err
	}
	stats := make(map[string]map[string]string)
	for _, row := range rows {
		stats[row["MEMBER_ID"]] = row
	}
	return stats, nil
}

func queryRowsAsMaps(ctx context.Context, db *sql.DB, query string) ([]map[string]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]string
	for rows.Next() {
		values := make([]sql.NullString, len(cols))
		scanArgs := make([]interface{}, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		row := make(map[string]string, len(cols))
		for i, col := range cols {
			if values[i].Valid {
				row[col] = values[i].String
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseNullableInt(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "NULL") {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil
}

func parseFloat(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(value, 64)
	return f
}

func isReplicationRunning(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "yes" || value == "on" || value == "1"
}

func appendCheckError(existing, extra string) string {
	if existing == "" {
		return extra
	}
	return existing + "; " + extra
}

func buildMySQLSummary(nodes []MySQLNodeCheck, prefix string) string {
	ok := 0
	for _, node := range nodes {
		if node.Reachable && node.Error == "" {
			ok++
		}
	}
	return fmt.Sprintf("%s：%d/%d 节点可达", prefix, ok, len(nodes))
}

func replicationCheckLabel(checkLag, checkStatus bool) string {
	if checkLag && !checkStatus {
		return "主从延迟检查"
	}
	if checkStatus && !checkLag {
		return "主从复制状态检查"
	}
	return "主从检查"
}

func mgrCheckLabel(checkNode, checkSync bool) string {
	if checkNode && !checkSync {
		return "MGR 节点状态检查"
	}
	if checkSync && !checkNode {
		return "MGR 同步队列检查"
	}
	return "MGR 检查"
}

func mgrSyncDataUnavailable(queueDataLoaded bool, memberCount int) bool {
	return !queueDataLoaded || memberCount == 0
}

func mgrRoleTopologyInvalid(primaryCount, secondaryCount, memberCount int, roleInvalid bool) bool {
	if roleInvalid || memberCount == 0 || primaryCount == 0 {
		return true
	}
	if secondaryCount > 0 {
		return false
	}
	return primaryCount != memberCount
}

func countMGRMemberRole(counted map[string]bool, memberID, address, role string) (primaryDelta, secondaryDelta int, invalid bool) {
	key := strings.TrimSpace(memberID)
	if key == "" {
		key = strings.TrimSpace(address)
	}
	if key == "" || counted[key] {
		return 0, 0, false
	}
	counted[key] = true

	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "PRIMARY":
		return 1, 0, false
	case "SECONDARY":
		return 0, 1, false
	default:
		return 0, 0, true
	}
}
