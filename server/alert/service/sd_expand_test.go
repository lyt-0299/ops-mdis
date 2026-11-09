package service

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"sourcecode.mingdao.net/private/MDIS/server/alert/model"
)

// 端口在整个列表上线性递增，且 agent(SourcesForAgent) 与 prometheus(PromFileSD)
// 走的是同一个 expandTargets——两边算出的端口必须一致，否则 target 指向的端口上
// 根本没有 exporter 在听，症状是 Prometheus 全是 connection refused。
func TestExpandTargets(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		docs     []bson.M
		wantPort []int
		wantHost []string
		wantNode []string
	}{
		{
			name: "MySQL 单节点：hostname 保持源名，不加后缀",
			kind: model.KindMySQLInstance,
			docs: []bson.M{{"_id": "a", "name": "生产库", "nodes": []any{"10.0.0.1:3306"}}},
			wantPort: []int{19100}, wantHost: []string{"生产库"}, wantNode: []string{"10.0.0.1:3306"},
		},
		{
			name: "MySQL 三节点：各占一个端口，hostname 带节点地址",
			kind: model.KindMySQLInstance,
			docs: []bson.M{{"_id": "a", "name": "集群", "nodes": []any{"h1:3306", "h2:3306", "h3:3306"}}},
			wantPort: []int{19100, 19101, 19102},
			wantHost: []string{"集群/h1:3306", "集群/h2:3306", "集群/h3:3306"},
			wantNode: []string{"h1:3306", "h2:3306", "h3:3306"},
		},
		{
			name: "多条源混排：端口跨源连续，不重叠",
			kind: model.KindMySQLInstance,
			docs: []bson.M{
				{"_id": "a", "name": "A", "nodes": []any{"h1:3306", "h2:3306"}},
				{"_id": "b", "name": "B", "nodes": []any{"h3:3306"}},
			},
			wantPort: []int{19100, 19101, 19102},
			wantHost: []string{"A/h1:3306", "A/h2:3306", "B"},
			wantNode: []string{"h1:3306", "h2:3306", "h3:3306"},
		},
		{
			name: "Redis 旧数据只有 host/port：回退成单节点",
			kind: model.KindRedisInstance,
			docs: []bson.M{{"_id": "r", "name": "缓存", "host": "10.0.0.9", "port": 6380}},
			wantPort: []int{19300}, wantHost: []string{"缓存"}, wantNode: []string{"10.0.0.9:6380"},
		},
		{
			name: "MongoDB 副本集不展开：多 host 属于同一个逻辑实例",
			kind: model.KindMongoInstance,
			docs: []bson.M{{"_id": "m", "name": "副本集", "uri": "mongodb://h1:27017,h2:27017/"}},
			wantPort: []int{19400}, wantHost: []string{"副本集"}, wantNode: []string{""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := typePortBase[map[string]string{
				model.KindMySQLInstance: "mysql", model.KindRedisInstance: "redis",
				model.KindMongoInstance: "mongodb", model.KindESInstance: "elasticsearch",
			}[c.kind]]
			got := expandTargets(c.kind, c.docs, base)
			if len(got) != len(c.wantPort) {
				t.Fatalf("展开出 %d 个目标，期望 %d", len(got), len(c.wantPort))
			}
			for i, g := range got {
				if g.port != c.wantPort[i] {
					t.Errorf("[%d] port = %d，期望 %d", i, g.port, c.wantPort[i])
				}
				if g.hostname != c.wantHost[i] {
					t.Errorf("[%d] hostname = %q，期望 %q", i, g.hostname, c.wantHost[i])
				}
				if g.node != c.wantNode[i] {
					t.Errorf("[%d] node = %q，期望 %q", i, g.node, c.wantNode[i])
				}
			}
		})
	}
}

// 展开出的节点必须写回条目，否则 agent 仍按整条源的地址集合去起 exporter，
// 多节点等于白配（MySQL 此前就是只采 nodes[0]）。
func TestApplyNodeOverride(t *testing.T) {
	e := map[string]any{}
	applyNodeOverride(e, model.KindRedisInstance, "10.0.0.9:6380")
	if e["host"] != "10.0.0.9" || e["redisPort"] != 6380 {
		t.Fatalf("Redis 节点未正确拆分：host=%v port=%v", e["host"], e["redisPort"])
	}

	e = map[string]any{}
	applyNodeOverride(e, model.KindRedisInstance, "redis-host")
	if e["host"] != "redis-host" || e["redisPort"] != 6379 {
		t.Fatalf("Redis 无端口时应回退 6379：host=%v port=%v", e["host"], e["redisPort"])
	}

	e = map[string]any{"nodes": []string{"h1", "h2", "h3"}}
	applyNodeOverride(e, model.KindMySQLInstance, "h2:3306")
	if ns, _ := e["nodes"].([]string); len(ns) != 1 || ns[0] != "h2:3306" {
		t.Fatalf("MySQL 应只保留当前节点，实际 %v", e["nodes"])
	}
}

func TestMetricCollectorArgsFromStoredSource(t *testing.T) {
	settings := &model.MetricCollection{Collectors: []string{"collections", "indexes"}, Scope: []string{"app.events"}}
	raw, err := bson.Marshal(model.MongoInstance{MetricCollection: settings})
	if err != nil {
		t.Fatal(err)
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	want := []string{"--collector.collstats", "--collector.indexstats", "--mongodb.collstats-colls=app.events", "--mongodb.indexstats-colls=app.events"}
	if got := metricCollectorArgs(model.KindMongoInstance, doc); !slices.Equal(got, want) {
		t.Fatalf("collectorArgs = %v, want %v", got, want)
	}
	delete(doc, "metricCollection")
	if got := metricCollectorArgs(model.KindMongoInstance, doc); len(got) != 0 {
		t.Fatalf("legacy source received extra collector args: %v", got)
	}
}
