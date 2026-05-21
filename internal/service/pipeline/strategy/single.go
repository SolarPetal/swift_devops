package strategy

import "context"

// Single 单主机串行 fail-fast 策略：
//   - 按 plan.Deps 顺序逐台部署
//   - 任一主机失败立即停止；剩余主机标 skipped
//   - 任一主机部署期间 ctx 取消（包括下一台进入前），剩余标 skipped
//
// 这是 Sprint 2.4 时的默认行为，Sprint 3.1 抽出来作为后续 Rolling / Rollback 的对照基线。
type Single struct{}

func (Single) Name() string { return "single" }

func (Single) Run(ctx context.Context, env Env, plan *Plan, hooks Hooks) ([]HostOutcome, []StepResult) {
	var (
		outcomes []HostOutcome
		allSteps []StepResult
	)

	stopReason := ""
	for i := range plan.Deps {
		dep := &plan.Deps[i]

		// 上一台失败或上下文已取消 / 超时 → 剩余主机全标 skipped
		if stopReason != "" {
			outcomes = append(outcomes, skipHost(dep, stopReason, hooks))
			continue
		}
		if err := ctx.Err(); err != nil {
			stopReason = "ctx done before start: " + err.Error()
			outcomes = append(outcomes, skipHost(dep, stopReason, hooks))
			continue
		}

		outcome, steps := deployHost(ctx, env, plan, dep, plan.Artifact, hooks)
		outcomes = append(outcomes, outcome)
		allSteps = append(allSteps, steps...)

		if outcome.Status == HostStatusFailed {
			stopReason = "previous host failed at " + outcome.CurrentStage
		}
	}
	return outcomes, allSteps
}
