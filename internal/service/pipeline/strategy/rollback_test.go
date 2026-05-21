package strategy_test

import (
	"context"
	"testing"

	"swift-devops/internal/model"
	"swift-devops/internal/service/pipeline/strategy"
)

// 构造 rollback 用的 plan：n 主机，artByDep map 决定哪几个有 previous。
func makeRollbackPlan(n int, withPrevious []int) *strategy.Plan {
	deps := make([]model.Deployment, n)
	artByDep := map[uint]*model.Artifact{}
	for i := range deps {
		deps[i] = model.Deployment{ID: uint(i + 1), HostID: uint(100 + i), AppID: 1}
	}
	for _, i := range withPrevious {
		artByDep[deps[i].ID] = &model.Artifact{
			ID: uint(900 + i), AppID: 1,
			FilePath: "/tmp/rollback.jar", FileMD5: "deadbeef",
		}
	}
	return &strategy.Plan{
		RunID: 1,
		App: &model.Application{
			ID: 1, AppCode: "demo", DeployPath: "/srv", Port: 8080,
		},
		Deps:            deps,
		ArtifactByDepID: artByDep,
		EnvMap:          map[string]string{},
	}
}

// 部分 dep 有 previous，部分没有：没有的 skipped 但不中断；
// 有的进入 deployHost（fakeHostLoader 让 LoadAuth 失败，所以会 failed → 后续 fail-fast）。
func TestRollback_MixedPreviousAndNot(t *testing.T) {
	hooks := &captureHooks{}
	plan := makeRollbackPlan(4, []int{1, 2}) // dep[1]、dep[2] 有 previous
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}

	outcomes, _ := strategy.Rollback{}.Run(context.Background(), env, plan, hooks)

	if len(outcomes) != 4 {
		t.Fatalf("outcomes count: %d", len(outcomes))
	}
	// dep[0] 没有 previous → skipped
	if outcomes[0].Status != strategy.HostStatusSkipped {
		t.Errorf("dep[0] 应 skipped(no previous): %s", outcomes[0].Status)
	}
	// dep[1] 有 previous，进入 deployHost，LoadAuth 报错 → failed
	if outcomes[1].Status != strategy.HostStatusFailed {
		t.Errorf("dep[1] 应 failed(dial): %s", outcomes[1].Status)
	}
	// dep[1] 失败 → dep[2] / dep[3] 都 skipped（fail-fast）
	for i := 2; i < 4; i++ {
		if outcomes[i].Status != strategy.HostStatusSkipped {
			t.Errorf("dep[%d] 应 skipped(fail-fast): %s", i, outcomes[i].Status)
		}
	}
	if got := strategy.AggregateStatus(outcomes, false); got != "failed" {
		t.Errorf("aggregate: %s, want failed", got)
	}
}

// 所有 dep 都没 previous（service 层正常拒绝，但策略层兜底要能跑）
func TestRollback_AllNoPrevious(t *testing.T) {
	hooks := &captureHooks{}
	plan := makeRollbackPlan(3, nil) // 没有任何 dep 有 previous
	env := strategy.Env{HostSvc: &fakeHostLoader{}}

	outcomes, _ := strategy.Rollback{}.Run(context.Background(), env, plan, hooks)

	for i, o := range outcomes {
		if o.Status != strategy.HostStatusSkipped {
			t.Errorf("dep[%d] 应 skipped: %s", i, o.Status)
		}
		if o.Err != "no previous artifact recorded" {
			t.Errorf("dep[%d] err msg: %q", i, o.Err)
		}
	}
	// 全 skipped 且未 cancelled → success（实际无操作；rollback 在 service 层会被 NO_PREVIOUS 截断）
	if got := strategy.AggregateStatus(outcomes, false); got != "success" {
		t.Errorf("aggregate: %s, want success", got)
	}
}

// ctx 已 done → 所有 dep skipped（即使有 previous）
func TestRollback_CtxDoneBeforeStart(t *testing.T) {
	hooks := &captureHooks{}
	plan := makeRollbackPlan(3, []int{0, 1, 2}) // 全有 previous
	env := strategy.Env{HostSvc: &fakeHostLoader{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcomes, _ := strategy.Rollback{}.Run(ctx, env, plan, hooks)
	for i, o := range outcomes {
		if o.Status != strategy.HostStatusSkipped {
			t.Errorf("dep[%d] 应 skipped: %s", i, o.Status)
		}
	}
	if got := strategy.AggregateStatus(outcomes, true); got != "cancelled" {
		t.Errorf("aggregate: %s, want cancelled", got)
	}
}

// 空 deps —— Rollback 不崩
func TestRollback_NoDeps(t *testing.T) {
	hooks := &captureHooks{}
	plan := &strategy.Plan{
		RunID: 1, App: &model.Application{},
		Deps: nil, ArtifactByDepID: map[uint]*model.Artifact{},
	}
	outcomes, steps := strategy.Rollback{}.Run(context.Background(), strategy.Env{}, plan, hooks)
	if len(outcomes) != 0 || len(steps) != 0 {
		t.Errorf("expect empty, got %d outcomes / %d steps", len(outcomes), len(steps))
	}
}
