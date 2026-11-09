package collname

import (
	"os"
	"testing"
)

func TestNameDefaultPrefix(t *testing.T) {
	os.Unsetenv("ENV_MONGODB_COLLECTION_PREFIX")
	resetForTest()
	if got := Name("alert_config"); got != "mdis_alert_config" {
		t.Fatalf("默认前缀错误: got %q want mdis_alert_config", got)
	}
}

func TestNameCustomPrefix(t *testing.T) {
	os.Setenv("ENV_MONGODB_COLLECTION_PREFIX", "ops_")
	resetForTest()
	defer os.Unsetenv("ENV_MONGODB_COLLECTION_PREFIX")
	if got := Name("alert_config"); got != "ops_alert_config" {
		t.Fatalf("自定义前缀错误: got %q want ops_alert_config", got)
	}
}
