package strategy

import (
	"context"
	"fmt"
	"time"

	"swift-devops/internal/model"
)

// BlueGreen 蓝绿部署策略：
//   - 仅部署 plan.TargetGroup 组（blue/green）的主机
//   - 组内顺序 fail-fast（复用 Single）；不并行，因为蓝绿本身已是流量隔离，组内激进无意义
//   - 全 success → 调 plan.NginxApply 切流（service 注入闭包：ssh 到 nginx 主机 + Applier.Apply）
//   - 切流成功 → hooks.OnGroupSwitched 通知 service 更新 App.ActiveGroup
//   - 任一阶段失败 → 加一条 host_id=0/stage=nginx_apply 的虚拟 outcome，
//     让 AggregateStatus 算 failed；已部署的 jar 不回滚（蓝绿的安全设计：失败也只影响目标组）
type BlueGreen struct{}

func (BlueGreen) Name() string { return "blue_green" }

func (BlueGreen) Run(ctx context.Context, env Env, plan *Plan, hooks Hooks) ([]HostOutcome, []StepResult) {
	if plan.TargetGroup != "blue" && plan.TargetGroup != "green" {
		return failVirtual(StageNginxApply, "invalid target_group: "+plan.TargetGroup, hooks)
	}

	// 1. 过滤目标组主机
	targetDeps := filterByGroup(plan.Deps, plan.TargetGroup)
	if len(targetDeps) == 0 {
		return failVirtual(StageNginxApply,
			fmt.Sprintf("target group %q has no deployments", plan.TargetGroup), hooks)
	}

	// 2. 复用 Single 顺序 fail-fast 部署目标组
	subPlan := *plan
	subPlan.Deps = targetDeps
	outcomes, steps := Single{}.Run(ctx, env, &subPlan, hooks)

	// 3. 任一 host failed → 不切流
	for _, o := range outcomes {
		if o.Status == HostStatusFailed {
			return outcomes, steps
		}
	}

	// 4. ctx 已取消 → 不切流（已有 skipped 的 outcome 反映这一点）
	if err := ctx.Err(); err != nil {
		return outcomes, steps
	}

	// 5. 调 nginx apply（service 注入的闭包）
	nginxStart := time.Now()
	if plan.NginxApply == nil {
		failStep := mkStepTimed(0, "nginx", "", StageNginxApply, false,
			"", "nginx apply hook not configured (service bug)", nginxStart)
		hooks.OnStep(failStep)
		steps = append(steps, failStep)
		outcomes = append(outcomes, HostOutcome{
			HostID: 0, Status: HostStatusFailed, CurrentStage: StageNginxApply,
			Err: "nginx apply hook missing",
		})
		return outcomes, steps
	}
	if err := plan.NginxApply(ctx); err != nil {
		failStep := mkStepTimed(0, "nginx", "", StageNginxApply, false, "", err.Error(), nginxStart)
		hooks.OnStep(failStep)
		steps = append(steps, failStep)
		outcomes = append(outcomes, HostOutcome{
			HostID: 0, Status: HostStatusFailed, CurrentStage: StageNginxApply, Err: err.Error(),
		})
		return outcomes, steps
	}

	// 6. 切流成功 → 加虚拟 step + 通知 service 更新 active_group
	okStep := mkStepTimed(0, "nginx", "", StageNginxApply, true,
		fmt.Sprintf("upstream switched to %s group", plan.TargetGroup), "", nginxStart)
	hooks.OnStep(okStep)
	steps = append(steps, okStep)
	hooks.OnGroupSwitched(plan.TargetGroup)

	return outcomes, steps
}

// filterByGroup 把 group_tag == target 的 dep 摘出来。
// dep.GroupTag 在 Sprint 2.2 已经设计好（blue/green/空）；蓝绿要求绑定主机时填了 group_tag。
func filterByGroup(deps []model.Deployment, target string) []model.Deployment {
	out := make([]model.Deployment, 0, len(deps))
	for i := range deps {
		if deps[i].GroupTag == target {
			out = append(out, deps[i])
		}
	}
	return out
}

// failVirtual 前置校验失败的统一返回路径：一条 host_id=0/stage=nginx_apply 的虚拟 outcome。
// 让 AggregateStatus 算 failed，前端能看到 error 信息。
func failVirtual(stage, msg string, hooks Hooks) ([]HostOutcome, []StepResult) {
	now := time.Now()
	step := mkStepTimed(0, "nginx", "", stage, false, "", msg, now)
	if hooks != nil {
		hooks.OnStep(step)
	}
	return []HostOutcome{{
			HostID: 0, Status: HostStatusFailed, CurrentStage: stage, Err: msg,
		}},
		[]StepResult{step}
}
