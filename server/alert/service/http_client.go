package service

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

var (
	httpClientCache = make(map[string]*http.Client)
	cacheMutex      sync.RWMutex
)

// NewHTTPClient 获取或创建 HTTP 客户端（支持正向代理，带缓存复用）
func NewHTTPClient(proxyURL string) *http.Client {
	// 1. 尝试从缓存获取
	cacheMutex.RLock()
	if client, ok := httpClientCache[proxyURL]; ok {
		cacheMutex.RUnlock()
		return client
	}
	cacheMutex.RUnlock()

	// 2. 创建新客户端
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	// 双重检查
	if client, ok := httpClientCache[proxyURL]; ok {
		return client
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 如果配置了代理，则使用代理
	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err == nil {
			// 使用自定义 Transport，但启用默认的连接池配置
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = http.ProxyURL(proxy)
			// 可以根据需要调整 MaxIdleConns 等参数，这里沿用 DefaultTransport 的默认值
			client.Transport = transport
		}
	} else {
		// 无代理时直接复用 DefaultClient 的 Transport (也是单例)
		// 或者显式使用 DefaultTransport
		// client.Transport = http.DefaultTransport // Optional, client uses it by default if nil
	}

	httpClientCache[proxyURL] = client
	return client
}
