package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// 本文件提供「连接测试」专用的一次性连接函数：开新连接、验证认证、立即关闭，
// 全程不进入各监控引擎的连接池，避免污染线上监控连接(参见 NewEphemeralPrometheusClient)。

// TestMongoConnection 一次性连接并 Ping 目标 MongoDB(含认证)。
func TestMongoConnection(inst *model.MongoInstance) error {
	client, err := buildMongoClient(inst) // 内部已 Connect + Ping，失败即返回 error
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Disconnect(ctx)
	return nil
}

// TestMySQLConnection 逐节点一次性连接并 Ping 目标 MySQL(含认证)。
func TestMySQLConnection(inst *model.MySQLInstance) error {
	nodes := make([]string, 0, len(inst.Nodes))
	for _, n := range inst.Nodes {
		if n = strings.TrimSpace(n); n != "" {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return fmt.Errorf("无可测试的节点")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, node := range nodes {
		db, err := openMySQL(inst, node)
		if err != nil {
			return fmt.Errorf("%s: %w", node, err)
		}
		err = db.PingContext(ctx)
		_ = db.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", node, err)
		}
	}
	return nil
}

// TestKafkaConnection 一次性连接目标 Kafka(含 SASL 认证)，并拉取元数据验证可用。
func TestKafkaConnection(inst *model.KafkaInstance) error {
	brokers := make([]string, 0, len(inst.Brokers))
	for _, b := range inst.Brokers {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	if len(brokers) == 0 {
		return fmt.Errorf("无可测试的 broker")
	}
	config := sarama.NewConfig()
	config.Version = sarama.V2_6_0_0
	config.Net.DialTimeout = 5 * time.Second
	config.Net.ReadTimeout = 10 * time.Second
	config.Net.WriteTimeout = 10 * time.Second
	// SASL 认证(含 TLS、Mechanism 校验、SCRAM Client Generator)
	if err := ApplyKafkaSASL(config, inst.SASL); err != nil {
		return err
	}
	// NewClient 会建立连接并拉取一次元数据：SASL 认证失败时此处即报错。
	client, err := sarama.NewClient(brokers, config)
	if err != nil {
		return fmt.Errorf("连接 Kafka 失败: %w", err)
	}
	defer client.Close()
	if _, err := client.Topics(); err != nil { // 再触发一次元数据请求，进一步确认可用
		return fmt.Errorf("获取元数据失败: %w", err)
	}
	return nil
}

// TestRedisConnection 一次性 TCP 连接 Redis，使用 RESP 数组帧发送命令，
// 可选 AUTH（password 非空）+ SELECT（db != 0）+ PING，期望 +PONG。8s deadline。
// 不依赖 go-redis 客户端库，使用标准库原生连接。
func TestRedisConnection(host string, port int, password string, db int, useTLS, skipSSL bool) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 5 * time.Second}
	var conn net.Conn
	var err error
	if useTLS {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{InsecureSkipVerify: skipSSL}) //nolint:gosec
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	rd := bufio.NewReader(conn)

	if password != "" {
		if err := redisCmd(conn, rd, "AUTH", password); err != nil {
			return fmt.Errorf("认证失败: %v", err)
		}
	}
	if db != 0 {
		if err := redisCmd(conn, rd, "SELECT", strconv.Itoa(db)); err != nil {
			return err
		}
	}
	return redisCmd(conn, rd, "PING")
}

// redisCmd 发送一条 RESP 数组命令并读取一行回复；回复以 '-' 开头视为服务端错误。
func redisCmd(conn net.Conn, rd *bufio.Reader, args ...string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := conn.Write([]byte(b.String())); err != nil {
		return err
	}
	line, err := rd.ReadString('\n')
	if err != nil {
		return err
	}
	line = strings.TrimRight(line, "\r\n")
	if strings.HasPrefix(line, "-") {
		return fmt.Errorf("%s", strings.TrimPrefix(line, "-"))
	}
	return nil
}

// TestESConnection 一次性 HTTP GET 根路径做真实认证探测（可选 Basic Auth）。
// 401/403 → "认证失败 (HTTP <code>)"；其余 4xx/5xx → "HTTP <code>"；8s 超时。
func TestESConnection(rawURL, username, password string, skipSSL bool) error {
	client := &http.Client{Timeout: 8 * time.Second}
	if skipSSL {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("认证失败 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
