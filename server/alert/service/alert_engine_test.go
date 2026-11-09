package service

import "testing"

// GetAlertEngine 必须永远返回可用实例。
//
// 1.4.0 从 InfraPulse 移植时只搬了 InitAlertEngine 的定义、没搬调用，这里一直
// 返回 nil；七个调度器发通知前都要 GetAlertEngine().getAlertTitle()，于是每次
// 真要发告警的那一刻就空指针 panic——通知发不出去，排在发送之后的
// SaveAlertHistory 也一并被带走，表现为「告警历史永远是空的」。
func TestGetAlertEngineNeverNil(t *testing.T) {
	e := GetAlertEngine()
	if e == nil {
		t.Fatal("GetAlertEngine 返回 nil：告警通知与历史落库都会 panic")
	}
	if got := e.getAlertTitle(); got != defaultAlertTitle {
		t.Fatalf("getAlertTitle() = %q，期望默认标题 %q", got, defaultAlertTitle)
	}
}
