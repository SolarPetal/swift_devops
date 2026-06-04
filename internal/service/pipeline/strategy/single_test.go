package strategy_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"swift-devops/internal/model"
	sshpkg "swift-devops/internal/pkg/ssh"
	"swift-devops/internal/service/pipeline/strategy"
)

// fakeHostLoader 受控的 HostLoader：fail=true 时 LoadAuth 直接报错（模拟 host 不存在 / 凭据查不到）。
type fakeHostLoader struct{ fail bool }

func (f *fakeHostLoader) LoadAuth(id uint) (sshpkg.HostTarget, sshpkg.AuthMethod, *model.Host, error) {
	if f.fail {
		return sshpkg.HostTarget{}, sshpkg.AuthMethod{}, nil, errors.New("loadauth boom")
	}
	return sshpkg.HostTarget{User: "u", IP: "127.0.0.1", Port: 1},
		sshpkg.AuthMethod{Type: "password", Password: "x"},
		&model.Host{ID: id, Name: "h", IP: "127.0.0.1"},
		nil
}
func (f *fakeHostLoader) RecordHostKey(uint, string) {}

// captureHooks 收集策略发出的所有事件，供断言。
type captureHooks struct {
	mu       sync.Mutex
	steps    []strategy.StepResult
	statuses []hostStatus
	deploys  []uint // deployment IDs that got OnDeploymentSuccess
}

type hostStatus struct {
	DeploymentID, HostID uint
	Status, Stage, Err   string
}

func (h *captureHooks) OnStep(s strategy.StepResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, s)
}
func (h *captureHooks) OnHostStatus(depID, hostID uint, status, stage, errMsg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statuses = append(h.statuses, hostStatus{depID, hostID, status, stage, errMsg})
}
func (h *captureHooks) OnDeploymentSuccess(dep *model.Deployment, _, _ uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deploys = append(h.deploys, dep.ID)
}

func makePlan(hostCount int) *strategy.Plan {
	deps := make([]model.Deployment, hostCount)
	for i := range deps {
		deps[i] = model.Deployment{ID: uint(i + 1), HostID: uint(100 + i), AppID: 1}
	}
	return &strategy.Plan{
		RunID:    1,
		App:      &model.Application{ID: 1, AppCode: "demo", DeployPath: "/srv", Port: 8080},
		Artifact: &model.Artifact{ID: 9, AppID: 1, FilePath: "/tmp/x.jar", FileMD5: "abc"},
		Deps:     deps,
		EnvMap:   map[string]string{},
	}
}

// 第一台主机 dial 阶段就失败（LoadAuth 返回 err），后续主机应全部 skipped。
func TestSingle_FailFast(t *testing.T) {
	hooks := &captureHooks{}
	plan := makePlan(3)
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}

	outcomes, _ := strategy.Single{}.Run(context.Background(), env, plan, hooks)

	if len(outcomes) != 3 {
		t.Fatalf("outcomes count: %d", len(outcomes))
	}
	if outcomes[0].Status != strategy.HostStatusFailed {
		t.Errorf("host[0] status: %s, want failed", outcomes[0].Status)
	}
	if outcomes[0].CurrentStage != strategy.StageDial {
		t.Errorf("host[0] stage: %s, want dial", outcomes[0].CurrentStage)
	}
	for i := 1; i < 3; i++ {
		if outcomes[i].Status != strategy.HostStatusSkipped {
			t.Errorf("host[%d] status: %s, want skipped (fail-fast)", i, outcomes[i].Status)
		}
	}
	if got := strategy.AggregateStatus(outcomes, false); got != "failed" {
		t.Errorf("aggregate status: %s, want failed", got)
	}
	if len(hooks.deploys) != 0 {
		t.Errorf("unexpected OnDeploymentSuccess: %v", hooks.deploys)
	}
}

// ctx 在进入第一台前已 done —— 所有主机应 skipped。
func TestSingle_CtxDoneBeforeStart(t *testing.T) {
	hooks := &captureHooks{}
	plan := makePlan(3)
	env := strategy.Env{HostSvc: &fakeHostLoader{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcomes, _ := strategy.Single{}.Run(ctx, env, plan, hooks)

	for i, o := range outcomes {
		if o.Status != strategy.HostStatusSkipped {
			t.Errorf("host[%d] status: %s, want skipped", i, o.Status)
		}
	}
	// Single 的 outcomes 无 failed + cancelled=true → cancelled
	if got := strategy.AggregateStatus(outcomes, true); got != "cancelled" {
		t.Errorf("aggregate: %s, want cancelled", got)
	}
}

// 空 deps —— Run 不崩，outcomes 也空。
func TestSingle_NoDeps(t *testing.T) {
	hooks := &captureHooks{}
	plan := &strategy.Plan{
		RunID: 1, App: &model.Application{}, Artifact: &model.Artifact{}, Deps: nil,
	}
	outcomes, steps := strategy.Single{}.Run(context.Background(), strategy.Env{}, plan, hooks)
	if len(outcomes) != 0 || len(steps) != 0 {
		t.Errorf("expect empty, got %d outcomes / %d steps", len(outcomes), len(steps))
	}
	if got := strategy.AggregateStatus(outcomes, false); got != "success" {
		t.Errorf("aggregate of empty: %s, want success (no failures)", got)
	}
}

// AggregateStatus 的几种组合
func TestAggregateStatus(t *testing.T) {
	cases := []struct {
		name      string
		outcomes  []strategy.HostOutcome
		cancelled bool
		want      string
	}{
		{"all success", []strategy.HostOutcome{
			{Status: strategy.HostStatusSuccess},
			{Status: strategy.HostStatusSuccess},
		}, false, "success"},
		{"any failed", []strategy.HostOutcome{
			{Status: strategy.HostStatusSuccess},
			{Status: strategy.HostStatusFailed},
		}, false, "failed"},
		{"success + skipped (no cancel) → success", []strategy.HostOutcome{
			{Status: strategy.HostStatusSuccess},
			{Status: strategy.HostStatusSkipped},
		}, false, "success"},
		{"success + skipped + cancelled flag → cancelled", []strategy.HostOutcome{
			{Status: strategy.HostStatusSuccess},
			{Status: strategy.HostStatusSkipped},
		}, true, "cancelled"},
		{"failed wins over cancel", []strategy.HostOutcome{
			{Status: strategy.HostStatusFailed},
			{Status: strategy.HostStatusSkipped},
		}, true, "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strategy.AggregateStatus(c.outcomes, c.cancelled); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// 烟雾测：Single 应该在合理时间内对受控失败的多主机收口。
func TestSingle_TimesOutBoundedly(t *testing.T) {
	plan := makePlan(5)
	env := strategy.Env{HostSvc: &fakeHostLoader{fail: true}}
	hooks := &captureHooks{}

	done := make(chan struct{})
	go func() {
		strategy.Single{}.Run(context.Background(), env, plan, hooks)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Single.Run 没在 3 秒内对 fail-fast 收口")
	}
}
