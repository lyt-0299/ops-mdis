package api

import (
	"log"
	"sync"
)

// 慢查询目标库(purposes 含 diagnose 的 MongoDB 实例)的热重建。
//
// 背景：慢查询/索引功能用的 MongoDB 连接由 main.go 在启动时读注册中心建立一次。
// 数据源页勾上「诊断」用途本身立刻落库，但那份连接不会跟着变——于是「页面上明明
// 勾了、慢查询页却一直说没配」，只能重启 gateway，UI 上没有任何东西提示这件事。
// 实测客户环境就卡在这里：源已是 purposes=[metrics alert diagnose]，接口仍 503。
//
// 这里留一个由 main.go 注入的回调，MongoDB 实例增删改/启停后触发重建。放在 api 包
// 而不是直接调 common：resolveSlowQueryConfig 在 main 包，alert 不该反向依赖它。
var (
	slowQueryReloadMu sync.Mutex
	slowQueryReloader func()
)

// SetSlowQueryReloader 由 main.go 在初始化慢查询目标后注入重建函数。传 nil 等于关闭热重建。
func SetSlowQueryReloader(fn func()) {
	slowQueryReloadMu.Lock()
	defer slowQueryReloadMu.Unlock()
	slowQueryReloader = fn
}

// reloadSlowQueryTargets 在 MongoDB 数据源变更后重建慢查询连接。
//
// 异步执行：重建要连目标库，慢的话不该拖住数据源保存的响应。失败只打日志——
// 数据源本身已经存好了，重建失败最多退回「重启才生效」的老行为，不能反过来让保存报错。
func reloadSlowQueryTargets(reason string) {
	slowQueryReloadMu.Lock()
	fn := slowQueryReloader
	slowQueryReloadMu.Unlock()
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[slowquery] 热重建 panic(%s): %v", reason, rec)
			}
		}()
		log.Printf("[slowquery] 数据源变更(%s)，重建慢查询目标连接", reason)
		fn()
	}()
}
