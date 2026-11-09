package service

import (
	"crypto/tls"
	"fmt"

	"github.com/IBM/sarama"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// ApplyKafkaSASL 将 SASL 配置应用到 sarama.Config。
// 返回错误时表示机制不受支持。
func ApplyKafkaSASL(config *sarama.Config, sasl *model.SASL) error {
	if sasl == nil || !sasl.Enabled {
		return nil
	}

	if !sasl.IsValidMechanism() {
		return fmt.Errorf("不支持的 SASL 机制: %q (仅支持 PLAIN/SCRAM-SHA-256/SCRAM-SHA-512)", sasl.Mechanism)
	}

	config.Net.SASL.Enable = true
	config.Net.SASL.User = sasl.Username
	config.Net.SASL.Password = sasl.Password
	config.Net.SASL.Handshake = true

	switch sasl.Mechanism {
	case "PLAIN":
		config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	case "SCRAM-SHA-256":
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &SCRAMClient{HashGeneratorFcn: SHA256HashFn}
		}
	case "SCRAM-SHA-512":
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &SCRAMClient{HashGeneratorFcn: SHA512HashFn}
		}
	}

	// 启用 TLS（SASL_SSL）— 避免 SASL 凭据明文传输
	if sasl.TLS {
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = &tls.Config{
			InsecureSkipVerify: sasl.SkipTLS,
		}
	}

	return nil
}
