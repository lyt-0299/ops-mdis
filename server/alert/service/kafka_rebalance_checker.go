package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

type KafkaRebalanceCheckResult struct {
	State   string
	Message string
}

type KafkaRebalanceChecker struct {
	instance              model.KafkaInstance
	consumerGroup         string
	topic                 string
	kafkaVersion          string
	commandTimeoutSeconds int
}

func NewKafkaRebalanceChecker(instance model.KafkaInstance, consumerGroup, topic, kafkaVersion string, commandTimeoutSeconds int) *KafkaRebalanceChecker {
	if kafkaVersion == "" {
		kafkaVersion = "2.0.0"
	}
	if commandTimeoutSeconds <= 0 {
		commandTimeoutSeconds = 50
	}
	return &KafkaRebalanceChecker{
		instance:              instance,
		consumerGroup:         strings.TrimSpace(consumerGroup),
		topic:                 strings.TrimSpace(topic),
		kafkaVersion:          strings.TrimSpace(kafkaVersion),
		commandTimeoutSeconds: commandTimeoutSeconds,
	}
}

func (c *KafkaRebalanceChecker) Check(ctx context.Context) KafkaRebalanceCheckResult {
	timeout := time.Duration(c.commandTimeoutSeconds) * time.Second
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-checkCtx.Done():
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: checkCtx.Err().Error()}
	default:
	}

	cfg := sarama.NewConfig()
	version, err := sarama.ParseKafkaVersion(c.kafkaVersion)
	if err != nil {
		version = sarama.V2_0_0_0
	}
	cfg.Version = version
	cfg.Admin.Timeout = timeout
	cfg.Net.DialTimeout = timeout
	cfg.Net.ReadTimeout = timeout
	cfg.Net.WriteTimeout = timeout

	if err := ApplyKafkaSASL(cfg, c.instance.SASL); err != nil {
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: err.Error()}
	}

	admin, err := sarama.NewClusterAdmin(c.instance.Brokers, cfg)
	if err != nil {
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("连接 Kafka 集群失败: %v", err)}
	}
	defer admin.Close()

	return c.checkWithAdmin(admin)
}

func (c *KafkaRebalanceChecker) checkWithAdmin(admin sarama.ClusterAdmin) KafkaRebalanceCheckResult {
	groups, err := admin.DescribeConsumerGroups([]string{c.consumerGroup})
	if err != nil {
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("查询消费组失败: %v", err)}
	}
	if len(groups) == 0 {
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("Kafka 未返回消费组 %s 的描述信息", c.consumerGroup)}
	}

	desc := groups[0]
	if desc.Err != sarama.ErrNoError {
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("Kafka 返回消费组错误: %v", desc.Err)}
	}

	stateMessage := fmt.Sprintf("Group State: %s", desc.State)
	switch desc.State {
	case "Stable":
		hasOffsets, err := c.hasOffsets(admin)
		if err != nil {
			return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("查询消费组 offset 失败: %v", err)}
		}
		if len(desc.Members) == 0 && hasOffsets {
			return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateEmpty, Message: "消费组 Stable 但没有活跃成员"}
		}
		orphaned, info, err := c.checkForOrphans(admin, desc)
		if err != nil {
			return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: fmt.Sprintf("检查未分配分区失败: %v", err)}
		}
		if orphaned {
			return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateOrphaned, Message: info}
		}
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateNormal, Message: stateMessage}
	case "PreparingRebalance", "CompletingRebalance":
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateRebalancing, Message: stateMessage}
	case "Empty":
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateEmpty, Message: stateMessage}
	case "Dead":
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateError, Message: stateMessage + " (Group is Dead)"}
	default:
		return KafkaRebalanceCheckResult{State: model.KafkaRebalanceStateNormal, Message: stateMessage + " (未知状态按正常处理)"}
	}
}

func (c *KafkaRebalanceChecker) hasOffsets(admin sarama.ClusterAdmin) (bool, error) {
	offsets, err := admin.ListConsumerGroupOffsets(c.consumerGroup, nil)
	if err != nil {
		return false, err
	}
	if offsets == nil || len(offsets.Blocks) == 0 {
		return false, nil
	}
	for topic, blocks := range offsets.Blocks {
		if c.topic != "" && topic != c.topic {
			continue
		}
		if len(blocks) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (c *KafkaRebalanceChecker) checkForOrphans(admin sarama.ClusterAdmin, desc *sarama.GroupDescription) (bool, string, error) {
	offsets, err := admin.ListConsumerGroupOffsets(c.consumerGroup, nil)
	if err != nil {
		return false, "", err
	}
	if offsets == nil || len(offsets.Blocks) == 0 {
		return false, "", nil
	}

	subscribedTopics := make(map[string]bool)
	assignedPartitions := make(map[string]map[int32]bool)
	for _, member := range desc.Members {
		topics, err := parseKafkaMemberMetadata(member.MemberMetadata)
		if err != nil {
			return false, "", fmt.Errorf("解析 member metadata 失败: %w", err)
		}
		for _, topic := range topics {
			subscribedTopics[topic] = true
		}

		assignments, err := parseKafkaMemberAssignment(member.MemberAssignment)
		if err != nil {
			return false, "", fmt.Errorf("解析 member assignment 失败: %w", err)
		}
		for topic, partitions := range assignments {
			if assignedPartitions[topic] == nil {
				assignedPartitions[topic] = make(map[int32]bool)
			}
			for _, partition := range partitions {
				assignedPartitions[topic][partition] = true
			}
		}
	}

	var orphaned []string
	for topic, partitionOffsets := range offsets.Blocks {
		if c.topic != "" && topic != c.topic {
			continue
		}
		for partition := range partitionOffsets {
			if !assignedPartitions[topic][partition] {
				orphaned = append(orphaned, fmt.Sprintf("%s-%d", topic, partition))
			}
		}
	}

	if len(desc.Members) > 0 && len(subscribedTopics) == 0 {
		hasOffsets, err := c.hasOffsets(admin)
		if err != nil {
			return false, "", err
		}
		if hasOffsets {
			return true, "消费组有成员和 offset，但没有活跃订阅，疑似僵尸成员", nil
		}
	}

	if len(orphaned) > 0 {
		details := strings.Join(orphaned, ", ")
		if len([]rune(details)) > 200 {
			runes := []rune(details)
			details = string(runes[:200]) + "..."
		}
		return true, fmt.Sprintf("%d 个分区未分配: %s", len(orphaned), details), nil
	}

	return false, "", nil
}

func IsKafkaRebalanceAbnormalState(state string) bool {
	switch state {
	case model.KafkaRebalanceStateRebalancing, model.KafkaRebalanceStateEmpty, model.KafkaRebalanceStateOrphaned:
		return true
	}
	return false
}

func KafkaRebalanceStateText(state string) string {
	switch state {
	case model.KafkaRebalanceStateNormal:
		return "正常"
	case model.KafkaRebalanceStateRebalancing:
		return "重平衡中"
	case model.KafkaRebalanceStateEmpty:
		return "无活跃成员"
	case model.KafkaRebalanceStateOrphaned:
		return "分区未分配"
	case model.KafkaRebalanceStateError:
		return "检测失败"
	default:
		return "未知状态"
	}
}

func parseKafkaMemberMetadata(data []byte) ([]string, error) {
	buf := bytes.NewBuffer(data)
	var version int16
	if err := binary.Read(buf, binary.BigEndian, &version); err != nil {
		return nil, err
	}

	var topicCount int32
	if err := binary.Read(buf, binary.BigEndian, &topicCount); err != nil {
		return nil, err
	}
	if topicCount < 0 || topicCount > 10000 {
		return nil, fmt.Errorf("无效的 topicCount: %d", topicCount)
	}

	topics := make([]string, topicCount)
	for i := 0; i < int(topicCount); i++ {
		topic, err := readKafkaProtocolString(buf)
		if err != nil {
			return nil, err
		}
		topics[i] = topic
	}
	return topics, nil
}

func parseKafkaMemberAssignment(data []byte) (map[string][]int32, error) {
	buf := bytes.NewBuffer(data)
	var version int16
	if err := binary.Read(buf, binary.BigEndian, &version); err != nil {
		return nil, err
	}

	var topicCount int32
	if err := binary.Read(buf, binary.BigEndian, &topicCount); err != nil {
		return nil, err
	}
	if topicCount < 0 || topicCount > 10000 {
		return nil, fmt.Errorf("无效的 topicCount: %d", topicCount)
	}

	assignment := make(map[string][]int32, topicCount)
	for i := 0; i < int(topicCount); i++ {
		topic, err := readKafkaProtocolString(buf)
		if err != nil {
			return nil, err
		}

		var partitionCount int32
		if err := binary.Read(buf, binary.BigEndian, &partitionCount); err != nil {
			return nil, err
		}
		if partitionCount < 0 || partitionCount > 100000 {
			return nil, fmt.Errorf("无效的 partitionCount: %d", partitionCount)
		}
		partitions := make([]int32, partitionCount)
		if err := binary.Read(buf, binary.BigEndian, &partitions); err != nil {
			return nil, err
		}
		assignment[topic] = partitions
	}
	return assignment, nil
}

func readKafkaProtocolString(buf *bytes.Buffer) (string, error) {
	var strLen int16
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return "", err
	}
	if strLen < 0 {
		return "", fmt.Errorf("invalid string length %d", strLen)
	}
	strBytes := make([]byte, strLen)
	if _, err := io.ReadFull(buf, strBytes); err != nil {
		return "", err
	}
	return string(strBytes), nil
}
