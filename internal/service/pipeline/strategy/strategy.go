package strategy

import "context"

// Strategy 一种部署节奏。
//
// 输入：Plan + Env + Hooks；输出：所有主机的 outcome 数组 + steps 数组（用于聚合到 RunSnapshot）。
// 策略对 ctx.Done() 必须及时响应——上层用 cancel 触发取消，剩余主机标 skipped。
type Strategy interface {
	// Name 策略名，对应 PipelineRun.Strategy 字段。
	Name() string

	// Run 执行计划。
	//   返回 (outcomes, steps)；调用方不依赖 error，错误已在 outcomes/steps 中体现。
	//   若 ctx 被取消，已开始的主机正常结束（不打断 SSH 会话），未开始的主机返回 skipped outcome。
	Run(ctx context.Context, env Env, plan *Plan, hooks Hooks) ([]HostOutcome, []StepResult)
}

// AggregateStatus 根据 outcomes 决定整个 run 的最终状态。
//   - 任一 failed → failed（即使 cancelled=true，failed 优先，便于排障）
//   - cancelled=true 且无 failed → cancelled（被用户/系统取消）
//   - 否则 → success
func AggregateStatus(outcomes []HostOutcome, cancelled bool) string {
	for _, o := range outcomes {
		if o.Status == HostStatusFailed {
			return "failed"
		}
	}
	if cancelled {
		return "cancelled"
	}
	return "success"
}
