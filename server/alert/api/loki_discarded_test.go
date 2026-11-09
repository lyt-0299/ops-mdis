package api

import "testing"

// Loki 的 /metrics 是 prometheus 文本格式，计数大了会写成科学计数法
// （实测 local_50 上就是 9.416187e+06）——按整数解析会直接丢掉这条，
// 于是「推了但被拒收」又被误判回「根本没推过来」。
func TestParseLokiDiscarded(t *testing.T) {
	body := []byte(`# HELP loki_discarded_samples_total Discarded samples
loki_discarded_samples_total{policy="",reason="too_far_behind",tenant="fake"} 105343
loki_discarded_samples_total{policy="",reason="rate_limited",tenant="fake"} 1.2e+03
loki_discarded_bytes_total{policy="",reason="too_far_behind",tenant="fake"} 9.416187e+06
loki_ingester_streams 42
`)
	n, reason := parseLokiDiscarded(body)
	if n != 105343+1200 {
		t.Fatalf("总数 = %d，期望 %d（含科学计数法那条）", n, 105343+1200)
	}
	if reason != "too_far_behind" {
		t.Fatalf("原因 = %q，期望占比最大的 too_far_behind", reason)
	}

	if n, r := parseLokiDiscarded([]byte("loki_ingester_streams 42\n")); n != 0 || r != "" {
		t.Fatalf("没有丢弃计数时应返回 0/空，实际 %d/%q", n, r)
	}
}
