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
//   - 任一 failed → failed
//   - 否则（含全 success / success + skipped）→ success
//
// Sprint 3.1 起 cancelled 概念由 service 层的 Cancel 机制注入；此处只看 outcomes 本身。
func AggregateStatus(outcomes []HostOutcome) string {
	for _, o := range outcomes {
		if o.Status == HostStatusFailed {
			return "failed"
		}
	}
	return "success"
}
