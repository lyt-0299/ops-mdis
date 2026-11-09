package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	prefixMark = "$AES256$"
)

var encryptionKey []byte

// InitKeyFromEnv 用 ENV_ALERT_CRYPTO_KEY 初始化对称密钥(sha256 归一到 32 字节)。
// 未设置时返回错误，启动阶段应 fatal，避免用空密钥跑。
func InitKeyFromEnv() error {
	raw := os.Getenv("ENV_ALERT_CRYPTO_KEY")
	if raw == "" {
		return errors.New("ENV_ALERT_CRYPTO_KEY 未设置")
	}
	sum := sha256.Sum256([]byte(raw))
	encryptionKey = sum[:]
	return nil
}

// Encrypt 使用 AES-256-GCM 加密明文，返回带前缀标记的十六进制密文。
// 空字符串或已带前缀的值直接返回，不做重复加密。
func Encrypt(plaintext string) (string, error) {
	if encryptionKey == nil {
		return "", errors.New("加密密钥未初始化")
	}
	if plaintext == "" || strings.HasPrefix(plaintext, prefixMark) {
		return plaintext, nil
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}

	ciphertext := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefixMark + hex.EncodeToString(ciphertext), nil
}

// Decrypt 解密带前缀标记的密文，返回明文。
// 空字符串或不带前缀的值直接返回，不做解密。
func Decrypt(value string) (string, error) {
	if encryptionKey == nil {
		return "", errors.New("加密密钥未初始化")
	}
	if value == "" || !strings.HasPrefix(value, prefixMark) {
		return value, nil
	}

	data, err := hex.DecodeString(value[len(prefixMark):])
	if err != nil {
		return "", fmt.Errorf("密文格式无效: %w", err)
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("创建 GCM 失败: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("密文数据过短")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败，密钥可能不匹配: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted 判断值是否为加密后的密文格式。
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, prefixMark)
}

// SetTestKey 用于测试环境设置固定密钥，避免文件 I/O。
func SetTestKey() {
	encryptionKey = make([]byte, 32)
}
