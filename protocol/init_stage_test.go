package protocol

import (
	"sync"
	"sync/atomic"
	"testing"
)

// 阶段变化要通知 observer；同一阶段/说明重复上报不重复通知
// （等待选课开放时每秒轮询一次，不能每秒塞一条事件）。
func TestInitStageReportsChanges(t *testing.T) {
	a := newStateTestClient(t, "http://127.0.0.1:1")

	var mu sync.Mutex
	var got []InitStage
	a.SetInitObserver(func(st InitStage) {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
	})

	a.reportInitStage(InitPhaseIndex, "")
	a.reportInitStage(InitPhaseIndex, "") // 同一阶段重复上报忽略
	a.reportInitStage(InitPhaseIndexWait, "当前不属于选课阶段")

	if len(got) != 2 {
		t.Fatalf("通知次数 = %d, want 2: %+v", len(got), got)
	}
	if got[0].Phase != InitPhaseIndex {
		t.Errorf("第 1 次通知 = %+v", got[0])
	}
	if got[1].Phase != InitPhaseIndexWait || got[1].Detail == "" {
		t.Errorf("第 2 次通知 = %+v", got[1])
	}
	if st := a.InitStage(); st.Phase != InitPhaseIndexWait {
		t.Errorf("InitStage() = %+v", st)
	}
	if a.InitStage().At.IsZero() {
		t.Error("阶段时间戳为空")
	}
}

// 每次初始化开始都要清掉上一次的残留阶段。
func TestInitStageReset(t *testing.T) {
	a := newStateTestClient(t, "http://127.0.0.1:1")
	a.reportInitStage(InitPhaseListPre, "")
	a.resetInitStage()
	if st := a.InitStage(); st.Phase != "" {
		t.Fatalf("reset 后阶段 = %+v, want 空", st)
	}
}

// nil client 上调用不应 panic（tui / 测试里 client 可能尚未构造）。
func TestInitStageNilSafety(t *testing.T) {
	var a *APIClient
	a.SetInitObserver(func(InitStage) {})
	if st := a.InitStage(); st.Phase != "" {
		t.Errorf("nil client 阶段 = %+v", st)
	}
	a.reportInitStage(InitPhaseIndex, "")
	a.resetInitStage()
}

// 并发上报不能 panic、不能漏锁（-race 下跑）。
func TestInitStageConcurrent(t *testing.T) {
	a := newStateTestClient(t, "http://127.0.0.1:1")
	var n atomic.Int64
	a.SetInitObserver(func(InitStage) { n.Add(1) })

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				// 每轮换一个阶段，确保 observer 真的被调用
				phase := InitPhaseIndex
				if i%2 == 1 {
					phase = InitPhaseIndexParse
				}
				a.reportInitStage(phase, "")
				_ = a.InitStage()
			}
		}()
	}
	wg.Wait()
	if n.Load() == 0 {
		t.Error("并发上报没有触发通知")
	}
}
