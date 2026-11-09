package model

import "testing"

// nodes 与 host/port 必须互相兜底：YAML 批量导入只写 nodes，UI 表单只可靠地给
// nodes（首节点回填 host/port），存量数据则只有 host/port。任一方向漏补，
// 要么被创建接口的必填校验挡掉，要么在 SD 展开时被当成「没配节点」跳过采集。
func TestRedisNormalizeNodes(t *testing.T) {
	// 只有 nodes（批量导入）
	r := &RedisInstance{Nodes: []string{"h1:6380", "h2:6380"}}
	r.NormalizeNodes()
	if r.Host != "h1" || r.Port != 6380 {
		t.Fatalf("应从首节点回填 host/port，实际 %s:%d", r.Host, r.Port)
	}

	// 只有 host/port（存量数据）
	r = &RedisInstance{Host: "old", Port: 6379}
	r.NormalizeNodes()
	if len(r.Nodes) != 1 || r.Nodes[0] != "old:6379" {
		t.Fatalf("应补出 nodes，实际 %v", r.Nodes)
	}

	// host 无端口时用默认
	r = &RedisInstance{Nodes: []string{"only-host"}}
	r.NormalizeNodes()
	if r.Host != "only-host" || r.Port != 6379 {
		t.Fatalf("无端口应回退 6379，实际 %s:%d", r.Host, r.Port)
	}
}

func TestESNormalizeNodes(t *testing.T) {
	e := &ESInstance{Nodes: []string{"http://es1:9200", "http://es2:9200"}}
	e.NormalizeNodes()
	if e.URL != "http://es1:9200" {
		t.Fatalf("应从首节点回填 url，实际 %q", e.URL)
	}

	e = &ESInstance{URL: "http://old:9200"}
	e.NormalizeNodes()
	if len(e.Nodes) != 1 || e.Nodes[0] != "http://old:9200" {
		t.Fatalf("应补出 nodes，实际 %v", e.Nodes)
	}
}
