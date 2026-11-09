package common

import (
	"encoding/json"
	"io"
	"sync"
	"testing"
)

// TestGlobalArgsConcurrentAccess 复现并守住 1.5.10 修掉的那次进程级崩溃。
//
// 现场：2026-08-03 17:14:52，在数据源页删一条 MongoDB 源，ops-server 直接
//
//	fatal error: concurrent map writes
//	  common/mongodb.go updateGlobalArgs() → startScheduledTask() → InitSlowQueryMongo()
//	  ← alert/api.reloadSlowQueryTargets()
//
// fatal error 不可 recover，进程连同网关一起退出（48881 全挂）。成因是
// GlobalArgs.ProfileStatus 这个裸 map 有四条并发路径：HTTP /api/global-args、
// ExecuteTaskHandler、采集 ticker、以及数据源增删改触发的热重建。
//
// 本测试不连 MongoDB，直接压 updateGlobalArgs 的写入端（setProfileStatus）与
// 读取端（snapshotGlobalArgs / JSON 序列化）。必须用 -race 跑才有意义：
//
//	go test -race ./common/ -run TestGlobalArgsConcurrent
func TestGlobalArgsConcurrentAccess(t *testing.T) {
	const workers = 8
	const rounds = 200

	var wg sync.WaitGroup

	// 写端：模拟热重建 + ticker + 手动执行同时刷新状态表
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				next := map[string]ProfileDetail{
					"mdwsrows": {Status: "slow", SlowMS: int64(id*1000 + i)},
					"other":    {Status: "off", SlowMS: 0},
				}
				setProfileStatus(next)
				setDefaultSourceName("mongodb_1")
			}
		}(w)
	}

	// 读端：模拟 /api/global-args 并发序列化
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				snap := snapshotGlobalArgs()
				// 快照必须能脱离锁安全遍历/序列化
				for k := range snap.ProfileStatus {
					_ = k
				}
				if err := json.NewEncoder(io.Discard).Encode(snap); err != nil {
					t.Errorf("序列化快照失败: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// 收尾自检：替换语义生效（整体替换而非累积），且快照是深拷贝
	setProfileStatus(map[string]ProfileDetail{"only": {Status: "slow", SlowMS: 100}})
	snap := snapshotGlobalArgs()
	if len(snap.ProfileStatus) != 1 {
		t.Fatalf("期望整体替换后只剩 1 个 key，实际 %d 个", len(snap.ProfileStatus))
	}
	snap.ProfileStatus["mutated"] = ProfileDetail{}
	if len(snapshotGlobalArgs().ProfileStatus) != 1 {
		t.Fatal("快照不是深拷贝：改动快照污染了 GlobalArgs")
	}
}
