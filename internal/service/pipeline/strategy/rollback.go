package strategy

import "context"

// Rollback 一键回滚策略：
//   - 每个 deployment 退到自己的 previous_artifact_id（plan.ArtifactByDepID 已预查）
//   - previous_artifact_id == 0（首次部署 / 已被清理）→ 该 dep 标 skipped（no previous）
//   - 顺序串行 fail-fast：rollback 安全优先，不并行，避免回滚途中产生不一致状态
//   - 已成功的 dep 通过 hooks.OnDeploymentSuccess 把"上一个版本"和"当前版本"再次互换，
//     形成天然的"回滚再回滚"=前进语义（避免回滚链丢失）
type Rollback struct{}

func (Rollback) Name() string { return "rollback" }

func (Rollback) Run(ctx context.Context, env Env, plan *Plan, hooks Hooks) ([]HostOutcome, []StepResult) {
	var (
		outcomes []HostOutcome
		allSteps []StepResult
	)

	stopReason := ""
	for i := range plan.Deps {
		dep := &plan.Deps[i]

		if stopReason != "" {
			outcomes = append(outcomes, skipHost(dep, stopReason, hooks))
			continue
		}
		if err := ctx.Err(); err != nil {
			stopReason = "ctx done before start: " + err.Error()
			outcomes = append(outcomes, skipHost(dep, stopReason, hooks))
			continue
		}

		// 查 dep 的 previous artifact；缺则跳过（不是失败）
		art, ok := plan.ArtifactByDepID[dep.ID]
		if !ok || art == nil {
			outcomes = append(outcomes, skipHost(dep, "no previous artifact recorded", hooks))
			continue
		}

		outcome, steps := deployHost(ctx, env, plan, dep, art, hooks)
		outcomes = append(outcomes, outcome)
		allSteps = append(allSteps, steps...)

		if outcome.Status == HostStatusFailed {
			stopReason = "previous host failed at " + outcome.CurrentStage
		}
	}
	return outcomes, allSteps
}
