// Package collname 集中管理 MDIS 自有 mongo 集合的统一前缀。
package collname

import (
	"os"
	"sync"
)

const defaultPrefix = "mdis_"

var (
	once   sync.Once
	prefix string
)

func load() {
	once.Do(func() {
		if p := os.Getenv("ENV_MONGODB_COLLECTION_PREFIX"); p != "" {
			prefix = p
		} else {
			prefix = defaultPrefix
		}
	})
}

// Name 返回带前缀的集合名，如 Name("alert_config") => "mdis_alert_config"。
func Name(base string) string { load(); return prefix + base }

func AlertConfig() string      { return Name("alert_config") }
func AlertState() string       { return Name("alert_state") }
func AlertHistory() string     { return Name("alert_history") }
func SlowQueryHistory() string { return Name("slowquery_history") }

func resetForTest() { once = sync.Once{}; prefix = "" }
