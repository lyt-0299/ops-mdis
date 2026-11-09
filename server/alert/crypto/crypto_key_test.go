package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTripFromEnvKey(t *testing.T) {
	t.Setenv("ENV_ALERT_CRYPTO_KEY", strings.Repeat("x", 32))
	if err := InitKeyFromEnv(); err != nil {
		t.Fatalf("InitKeyFromEnv 失败: %v", err)
	}
	enc, err := Encrypt("round-trip-fixture")
	if err != nil {
		t.Fatalf("Encrypt 失败: %v", err)
	}
	if dec, err := Decrypt(enc); err != nil || dec != "round-trip-fixture" {
		t.Fatalf("round trip 失败: dec=%q err=%v", dec, err)
	}
}
