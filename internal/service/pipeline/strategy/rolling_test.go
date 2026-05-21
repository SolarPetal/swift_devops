package strategy_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"swift-devops/internal/model"
	sshpkg "swift-devops/internal/pkg/ssh"
	"swift-devops/internal/service/pipeline/strategy"
)

// concurrencyLoader 用 atomic 计数同时在 LoadAuth 中的 goroutine，
// 测试结束后 max() 反映出策略的并发度上限。
// sleep 模拟拨号耗时，让并行的 goroutine 在临界区里逗留得足够长。
type concurrencyLoader struct {
	inflight    atomic.Int64
	maxInflight atomic.Int64
	sleep       time.Duration
}

func (c *concurrencyLoader) LoadAuth(id uint) (sshpkg.HostTarget, sshpkg.AuthMethod, *model.Host, error) {
	n := c.inflight.Add(1)
	for {
		old := c.maxInflight.Load()
		if n <= old || c.maxInflight.CompareAndSwap(old, n) {
			break
		}
	}
	time.Sleep(c.sleep)
	c.inflight.Add(-1)
	return sshpkg.HostTarget{}, sshpkg.AuthMethod{}, nil, errors.New("loadauth boom (intended)")
}
func (c *concurrencyLoader) RecordHostKey(uint, string) {}

// 5 主机 batch=2 → 3 批 [2,2,1]：
//   - 第一批失败（dial 失败）后，第二批和第三批的 4 个 host 都标 skipped
func TestRolling_FailFastAcrossBatches(t *testing.T) {
	hooks := &captureHooks{}
	plan := makePlan(5)
	plan.BatchSize = 2
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}

	outcomes, _ := strategy.Rolling{}.Run(context.Background(), env, plan, hooks)

	if len(outcomes) != 5 {
		t.Fatalf("outcomes count: %d", len(outcomes))
	}
	// 第一批两台都 failed（并行执行，都进 dial）
	for i := 0; i < 2; i++ {
		if outcomes[i].Status != strategy.HostStatusFailed {
			t.Errorf("host[%d] status: %s, want failed", i, outcomes[i].Status)
		}
	}
	// 后两批 3 台 skipped
	for i := 2; i < 5; i++ {
		if outcomes[i].Status != strategy.HostStatusSkipped {
			t.Errorf("host[%d] status: %s, want skipped", i, outcomes[i].Status)
		}
	}
	if got := strategy.AggregateStatus(outcomes, false); got != "failed" {
		t.Errorf("aggregate: %s, want failed", got)
	}
}

// 验证批内确实并行：3 主机 batch=3，loader sleep 50ms。
// 并行时 maxInflight 应达到 3；串行时只会 1。
func TestRolling_BatchInParallel(t *testing.T) {
	loader := &concurrencyLoader{sleep: 50 * time.Millisecond}
	hooks := &captureHooks{}
	plan := makePlan(3)
	plan.BatchSize = 3
	env := strategy.Env{HostSvc: loader}

	strategy.Rolling{}.Run(context.Background(), env, plan, hooks)

	if got := loader.maxInflight.Load(); got != 3 {
		t.Fatalf("batch 内并发度 = %d, want 3（串行执行说明 wg/goroutine 没启动）", got)
	}
}

// batch_size > host count：所有主机一批跑完，行为等价于全并行
func TestRolling_BatchLargerThanHostCount(t *testing.T) {
	loader := &concurrencyLoader{sleep: 30 * time.Millisecond}
	hooks := &captureHooks{}
	plan := makePlan(3)
	plan.BatchSize = 10 // 超过 host 数
	env := strategy.Env{HostSvc: loader}

	strategy.Rolling{}.Run(context.Background(), env, plan, hooks)

	if got := loader.maxInflight.Load(); got != 3 {
		t.Fatalf("应一批跑完 3 台，并发度 = %d, want 3", got)
	}
}

// ctx 在进入第一批前已 done → 所有主机 skipped
func TestRolling_CtxDoneBeforeStart(t *testing.T) {
	hooks := &captureHooks{}
	plan := makePlan(4)
	plan.BatchSize = 2
	env := strategy.Env{HostSvc: &fakeHostLoader{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcomes, _ := strategy.Rolling{}.Run(ctx, env, plan, hooks)

	for i, o := range outcomes {
		if o.Status != strategy.HostStatusSkipped {
			t.Errorf("host[%d] status: %s, want skipped", i, o.Status)
		}
	}
	if got := strategy.AggregateStatus(outcomes, true); got != "cancelled" {
		t.Errorf("aggregate: %s, want cancelled", got)
	}
}

// 空 deps —— Run 不崩，outcomes 空
func TestRolling_NoDeps(t *testing.T) {
	hooks := &captureHooks{}
	plan := &strategy.Plan{
		RunID: 1, App: &model.Application{}, Artifact: &model.Artifact{},
		Deps: nil, BatchSize: 2,
	}
	outcomes, _ := strategy.Rolling{}.Run(context.Background(), strategy.Env{}, plan, hooks)
	if len(outcomes) != 0 {
		t.Errorf("expect empty, got %d outcomes", len(outcomes))
	}
}

// BatchSize <= 0 退化为单批（防御性）
func TestRolling_ZeroBatchDegradesToOnePerBatch(t *testing.T) {
	loader := &concurrencyLoader{sleep: 30 * time.Millisecond}
	hooks := &captureHooks{}
	plan := makePlan(3)
	plan.BatchSize = 0 // 防御性
	env := strategy.Env{HostSvc: loader}

	strategy.Rolling{}.Run(context.Background(), env, plan, hooks)

	// batch=1 → 串行 → 任意时刻最多 1 个 inflight
	if got := loader.maxInflight.Load(); got != 1 {
		t.Fatalf("batch=0 应退化为单台单批，maxInflight=%d, want 1", got)
	}
}

// 烟雾测：Rolling 应在合理时间内完成 fail-fast
func TestRolling_TimesOutBoundedly(t *testing.T) {
	plan := makePlan(6)
	plan.BatchSize = 2
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}
	hooks := &captureHooks{}

	done := make(chan struct{})
	go func() {
		strategy.Rolling{}.Run(context.Background(), env, plan, hooks)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Rolling.Run 没在 3 秒内对 fail-fast 收口")
	}
}
