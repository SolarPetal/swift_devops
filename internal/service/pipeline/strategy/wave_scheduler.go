package strategy

import (
	"context"
	"sort"
	"sync"
	"time"

	"swift-devops/internal/model"
)

// Wave 一组同 startup_order 的 AppService —— Sprint X.3。
//
// 设计：
//   - 同 Wave 内 service 并发部署（互不依赖）
//   - Wave 之间串行：前 Wave 任一非 Optional service 失败 → 后续 Wave 全 skipped
//   - 单体 App（无 AppService 行）退化为 1 个 Wave + 1 个隐式 "default" service
type Wave struct {
	Order    int
	Services []model.AppService
}

// PartitionWaves 把 services 按 startup_order 分波。
//
// 同值的进同一波，波内顺序按 ID ASC 稳定（便于 UI 渲染稳定）。
func PartitionWaves(services []model.AppService) []Wave {
	if len(services) == 0 {
		return nil
	}
	sorted := make([]model.AppService, len(services))
	copy(sorted, services)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StartupOrder != sorted[j].StartupOrder {
			return sorted[i].StartupOrder < sorted[j].StartupOrder
		}
		return sorted[i].ID < sorted[j].ID
	})

	var waves []Wave
	cur := Wave{Order: sorted[0].StartupOrder}
	for _, s := range sorted {
		if s.StartupOrder != cur.Order {
			waves = append(waves, cur)
			cur = Wave{Order: s.StartupOrder}
		}
		cur.Services = append(cur.Services, s)
	}
	waves = append(waves, cur)
	return waves
}

// SubPlanBuilder 构造单 service sub-plan 的回调（由 pipeline_service 注入）。
// 返回 nil 表示该 service 在本次 run 里不需要部署（如 rollback 时没有 previous item）。
type SubPlanBuilder func(svc *model.AppService) (*Plan, Strategy, error)

// WaveResult 单波执行结果。
type WaveResult struct {
	Outcomes []HostOutcome
	Steps    []StepResult
	// FailedRequired 该 wave 是否有 "非 optional" service 失败 → 影响后续 wave 是否继续
	FailedRequired bool
}

// RunWaves 串行执行 waves，波内并发跑每个 service 的 strategy.Run。
//
// 参数：
//   - build: 给定 service 返回它的 sub-plan + 选定 strategy（pipeline_service 决定 single/rolling/bluegreen/rollback）
//   - 若 build 返回 nil plan 且 err == nil，视为"该 service 无需部署"，跳过
//
// 失败语义（决策 Q3 答案 A）：
//   - service.Optional=true 失败 → 不阻塞后续 wave，但 outcomes 仍记 failed
//   - service.Optional=false 失败 → 后续 wave 整体 skipped
func RunWaves(ctx context.Context, env Env, waves []Wave, build SubPlanBuilder, hooks Hooks) ([]HostOutcome, []StepResult) {
	var (
		allOutcomes []HostOutcome
		allSteps    []StepResult
		stopReason  string
	)

	for _, wave := range waves {
		if stopReason != "" {
			// 前 wave 失败 → 本 wave 直接跳过：每个 service 的所有 dep 标 skipped
			for i := range wave.Services {
				svc := wave.Services[i]
				sub, _, err := build(&svc)
				if err != nil || sub == nil {
					continue
				}
				for j := range sub.Deps {
					allOutcomes = append(allOutcomes, skipHost(&sub.Deps[j], "previous wave failed: "+stopReason, hooks))
				}
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			stopReason = "ctx done: " + err.Error()
			continue
		}

		// 波内并发：每个 service 一个 goroutine
		type slot struct {
			svc      *model.AppService
			outcomes []HostOutcome
			steps    []StepResult
			failed   bool
			required bool
		}
		slots := make([]slot, len(wave.Services))
		var wg sync.WaitGroup
		for i := range wave.Services {
			i := i
			svc := wave.Services[i]
			wg.Add(1)
			go func() {
				defer wg.Done()
				sub, strat, err := build(&svc)
				if err != nil {
					// build 失败 → 当成 service 失败
					slots[i] = slot{
						svc:      &svc,
						required: !svc.Optional,
						failed:   true,
						outcomes: []HostOutcome{{
							HostID: 0, Status: HostStatusFailed,
							CurrentStage: "plan", Err: err.Error(),
						}},
						steps: []StepResult{mkStepTimed(0, "(plan)", "", "plan", false, "", err.Error(), time.Now())},
					}
					return
				}
				if sub == nil {
					// 无需部署（例如 rollback 时该 service 没有 previous item）
					slots[i] = slot{svc: &svc}
					return
				}
				outcomes, steps := strat.Run(ctx, env, sub, hooks)
				anyFail := false
				for _, o := range outcomes {
					if o.Status == HostStatusFailed {
						anyFail = true
						break
					}
				}
				slots[i] = slot{
					svc:      &svc,
					outcomes: outcomes,
					steps:    steps,
					failed:   anyFail,
					required: !svc.Optional,
				}
			}()
		}
		wg.Wait()

		// 汇聚 + 决定是否阻塞后续 wave
		for _, sl := range slots {
			allOutcomes = append(allOutcomes, sl.outcomes...)
			allSteps = append(allSteps, sl.steps...)
			if sl.failed && sl.required {
				stopReason = "service " + sl.svc.ServiceCode + " (required) failed"
			}
		}
	}

	return allOutcomes, allSteps
}
