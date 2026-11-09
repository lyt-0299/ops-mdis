package service

import (
	"crypto/sha256"
	"crypto/sha512"
	"hash"

	"github.com/xdg-go/scram"
)

// SCRAMClient 实现 sarama.SCRAMClient 接口，用于 SCRAM-SHA-256/512 认证
type SCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	HashGeneratorFcn scram.HashGeneratorFcn
}

// Begin 初始化 SCRAM 会话
func (x *SCRAMClient) Begin(userName, password, authzID string) error {
	client, err := x.HashGeneratorFcn.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.Client = client
	x.ClientConversation = x.Client.NewConversation()
	return nil
}

// Step 推进 SCRAM 握手
func (x *SCRAMClient) Step(challenge string) (string, error) {
	return x.ClientConversation.Step(challenge)
}

// Done SCRAM 握手是否完成
func (x *SCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}

// SHA256HashFn SCRAM-SHA-256 的哈希生成器
var SHA256HashFn scram.HashGeneratorFcn = func() hash.Hash { return sha256.New() }

// SHA512HashFn SCRAM-SHA-512 的哈希生成器
var SHA512HashFn scram.HashGeneratorFcn = func() hash.Hash { return sha512.New() }
