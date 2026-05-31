package strategy_test

import (
	"context"
	"testing"

	"swift-devops/internal/model"
	"swift-devops/internal/service/pipeline/strategy"
)

// makeBlueGreenPlan：3 个 dep，前 2 个 group_tag=blue，第 3 个 green。
// fakeHostLoader 默认让 LoadAuth 成功但 ssh dial 会真的去连（无真 sshd 时失败）；
// 单测层面只覆盖"前置校验"和"host 失败 / ctx 取消"分支。
// 全 success 路径需要真 SSH，留 Sprint 4.5 集成测覆盖。
func makeBlueGreenPlan(target string, applyFn func(context.Context) error) *strategy.Plan {
	deps := []model.Deployment{
		{ID: 1, HostID: 101, AppID: 1, GroupTag: "blue"},
		{ID: 2, HostID: 102, AppID: 1, GroupTag: "blue"},
		{ID: 3, HostID: 103, AppID: 1, GroupTag: "green"},
	}
	return &strategy.Plan{
		RunID: 1,
		App: &model.Application{
			ID: 1, AppCode: "demo", DeployPath: "/srv", Port: 8080,
			NginxHostID: 999, NginxUpstreamName: "demo-up",
		},
		Artifact:    &model.Artifact{ID: 9, AppID: 1, FilePath: "/tmp/x.jar", FileMD5: "abc"},
		Deps:        deps,
		EnvMap:      map[string]string{},
		TargetGroup: target,
		NginxApply:  applyFn,
	}
}

// 目标组 = "purple" → 直接虚拟 failed
func TestBlueGreen_InvalidTargetGroup(t *testing.T) {
	hooks := &captureHooks{}
	plan := makeBlueGreenPlan("purple", nil)
	outcomes, steps := strategy.BlueGreen{}.Run(context.Background(),
		strategy.Env{HostSvc: &fakeHostLoader{}}, plan, hooks)
	if len(outcomes) != 1 || outcomes[0].Status != strategy.HostStatusFailed {
		t.Fatalf("应返回单条 failed outcome：%+v", outcomes)
	}
	if outcomes[0].CurrentStage != strategy.StageNginxApply {
		t.Errorf("stage 应是 nginx_apply: %s", outcomes[0].CurrentStage)
	}
	if len(steps) != 1 || steps[0].Stage != strategy.StageNginxApply {
		t.Errorf("应推一条 nginx_apply step: %+v", steps)
	}
	if got := strategy.AggregateStatus(outcomes, false); got != "failed" {
		t.Errorf("aggregate: %s, want failed", got)
	}
}

// target=green 但 plan 里 deps 全是 blue → green 组无主机 → 虚拟 failed
func TestBlueGreen_NoTargetDeployments(t *testing.T) {
	hooks := &captureHooks{}
	plan := &strategy.Plan{
		RunID: 1, App: &model.Application{ID: 1, AppCode: "demo"},
		Deps: []model.Deployment{
			{ID: 1, HostID: 101, AppID: 1, GroupTag: "blue"},
			{ID: 2, HostID: 102, AppID: 1, GroupTag: "blue"},
		},
		TargetGroup: "green",
	}
	outcomes, _ := strategy.BlueGreen{}.Run(context.Background(),
		strategy.Env{HostSvc: &fakeHostLoader{}}, plan, hooks)
	if len(outcomes) != 1 || outcomes[0].Status != strategy.HostStatusFailed {
		t.Fatalf("应返回单条 failed outcome：%+v", outcomes)
	}
}

// 部署目标组主机时第一台 dial 就失败 → fail-fast，不调 NginxApply / OnGroupSwitched
// 顺带验证 filterByGroup：outcomes 只包含 blue 组 2 台主机（green 那台不在 outcomes 里）
func TestBlueGreen_HostFailureSkipsNginx(t *testing.T) {
	hooks := &captureHooks{}
	applyCalled := false
	plan := makeBlueGreenPlan("blue", func(context.Context) error {
		applyCalled = true
		return nil
	})
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}

	outcomes, _ := strategy.BlueGreen{}.Run(context.Background(), env, plan, hooks)

	if len(outcomes) != 2 {
		t.Fatalf("outcomes count: %d (filterByGroup 应只摘 blue 组 2 台)", len(outcomes))
	}
	if outcomes[0].Status != strategy.HostStatusFailed {
		t.Errorf("blue[0] 应 failed: %s", outcomes[0].Status)
	}
	if outcomes[1].Status != strategy.HostStatusSkipped {
		t.Errorf("blue[1] 应 skipped: %s", outcomes[1].Status)
	}
	if applyCalled {
		t.Error("host 失败时不应调 NginxApply")
	}
	if hooks.groupSwitched != "" {
		t.Error("不应触发 OnGroupSwitched")
	}
}

// ctx 进入前已取消 → 不调 NginxApply / OnGroupSwitched
func TestBlueGreen_CtxDoneSkipsApply(t *testing.T) {
	hooks := &captureHooks{}
	applyCalled := false
	plan := makeBlueGreenPlan("blue", func(context.Context) error {
		applyCalled = true
		return nil
	})
	env := strategy.Env{HostSvc: &fakeHostLoader{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	strategy.BlueGreen{}.Run(ctx, env, plan, hooks)

	if applyCalled {
		t.Error("ctx 已取消时不应调 NginxApply")
	}
	if hooks.groupSwitched != "" {
		t.Error("不应触发 OnGroupSwitched")
	}
}

// Success 路径（host 全成 → NginxApply 调用 → OnGroupSwitched 触发）
// 由 Sprint 4.5 集成测覆盖（真 SSH + nginx 容器）。
