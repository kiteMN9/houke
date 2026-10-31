package protocol

import (
	"sync"
	"time"
)

// =========================================================================
// 初始化阶段（InitStage）由 APIClient 在抓选课参数时自己记账
//
// 和 LoginStage 同一套思路：阶段语义在 protocol 内部本来就已知（取首页、
// 等选课开放、解析页签、取课程列表参数、解析规则），上层只订阅变化，
// 不需要靠 ProtocolHook 拿 URL 再猜。
//
// 约束：
//   - 只在初始化主链路（GetIndexParams / GetCourseListPre）上调用；
//   - observer 同步调用，实现必须快速返回、不得阻塞；
//   - 阶段是运行态，不落盘。
// =========================================================================

// InitPhase 是选课初始化过程中"用户视角"的阶段标识。
type InitPhase string

const (
	// InitPhaseIndex 请求选课首页。
	InitPhaseIndex InitPhase = "init.index"
	// InitPhaseIndexWait 首页显示当前不在选课阶段，等待开放（仅 WaitForOpen）。
	InitPhaseIndexWait InitPhase = "init.index-wait"
	// InitPhaseIndexParse 解析首页里的页签与选课开关。
	InitPhaseIndexParse InitPhase = "init.index-parse"
	// InitPhaseListPre 请求课程列表前置参数。
	InitPhaseListPre InitPhase = "init.list-pre"
	// InitPhaseListPreParse 解析学分限制、轮次等选课规则。
	InitPhaseListPreParse InitPhase = "init.list-pre-parse"
)

// InitStage 是一次初始化阶段快照。
type InitStage struct {
	Phase  InitPhase // 阶段标识；空串表示还没开始过初始化
	Detail string    // 可选补充说明（如等待原因），不参与逻辑判断
	At     time.Time // 进入该阶段的时间
}

// initStageState 是 APIClient 内的初始化阶段记账。
type initStageState struct {
	mu       sync.Mutex
	stage    InitStage
	observer func(InitStage)
}

// InitStage 返回当前（或最近一次）初始化阶段快照。
func (a *APIClient) InitStage() InitStage {
	if a == nil {
		return InitStage{}
	}
	a.initStage.mu.Lock()
	defer a.initStage.mu.Unlock()
	return a.initStage.stage
}

// SetInitObserver 挂载初始化阶段变化回调；传 nil 取消。
// 回调在初始化流程的 goroutine 上同步执行，必须快速返回。
func (a *APIClient) SetInitObserver(f func(InitStage)) {
	if a == nil {
		return
	}
	a.initStage.mu.Lock()
	a.initStage.observer = f
	a.initStage.mu.Unlock()
}

// reportInitStage 记录一次阶段变化并通知 observer。
// 阶段与补充说明都没变时不重复通知：等待选课开放时每秒都会轮询，
// 不能每秒往时间线里塞一条重复事件。
func (a *APIClient) reportInitStage(phase InitPhase, detail string) {
	if a == nil {
		return
	}
	a.initStage.mu.Lock()
	prev := a.initStage.stage
	if prev.Phase == phase && prev.Detail == detail {
		a.initStage.mu.Unlock()
		return
	}
	st := InitStage{Phase: phase, Detail: detail, At: time.Now()}
	a.initStage.stage = st
	obs := a.initStage.observer
	a.initStage.mu.Unlock()

	// observer 在锁外调用：实现里可能要读 client 状态，不能和阶段锁嵌套。
	if obs != nil {
		obs(st)
	}
}

// resetInitStage 在每次初始化开始时清掉上一次的残留阶段。
func (a *APIClient) resetInitStage() {
	if a == nil {
		return
	}
	a.initStage.mu.Lock()
	a.initStage.stage = InitStage{}
	a.initStage.mu.Unlock()
}
