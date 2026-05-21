package strategy

import (
	"context"
	"sync"
)

// Rolling 分批并行部署策略：
//   - 按 plan.BatchSize 把 deps 切成连续批次（不足整批的尾批照常跑）
//   - 批内：goroutine 并行 deployHost；任一失败 → 整批视为失败
//   - 批间：上一批失败 / ctx done → 剩余批次全部标 skipped
//
// 设计取舍（Sprint 3.2 决策记录）：
//   - 批内并行（goroutine + WaitGroup），不引入 errgroup 避免错误"早返回"打断同批兄弟
//   - 不加 grace_period —— deployHost 自带 60s 健康探针，单次探针通过即视为该主机就绪
//   - 同 host 的步骤事件按时间序发出；不同 host 的事件是交织的（前端按 host 聚合即可）
//   - hooks 实现需保证 goroutine 安全（runHooks 透过 publish 走 channel、GORM 并发写满足）
type Rolling struct{}

func (Rolling) Name() string { return "rolling" }

func (Rolling) Run(ctx context.Context, env Env, plan *Plan, hooks Hooks) ([]HostOutcome, []StepResult) {
	if len(plan.Deps) == 0 {
		return nil, nil
	}
	batch := plan.BatchSize
	if batch <= 0 {
		batch = 1 // 防御性退化；service 层 pickStrategy 已经卡住 <1 的情况
	}

	var (
		outcomes   []HostOutcome
		allSteps   []StepResult
		stopReason string
	)

	for start := 0; start < len(plan.Deps); start += batch {
		end := start + batch
		if end > len(plan.Deps) {
			end = len(plan.Deps)
		}
		batchSlice := plan.Deps[start:end]

		// 上一批失败 / ctx 取消 → 本批整体 skipped
		if stopReason != "" {
			for i := range batchSlice {
				outcomes = append(outcomes, skipHost(&batchSlice[i], stopReason, hooks))
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			stopReason = "ctx done before batch: " + err.Error()
			for i := range batchSlice {
				outcomes = append(outcomes, skipHost(&batchSlice[i], stopReason, hooks))
			}
			continue
		}

		// 批内并行：每个 goroutine 写自己的 index，无 race
		batchOutcomes := make([]HostOutcome, len(batchSlice))
		batchSteps := make([][]StepResult, len(batchSlice))
		var wg sync.WaitGroup
		for i := range batchSlice {
			i := i
			dep := &batchSlice[i]
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcome, steps := deployHost(ctx, env, plan, dep, plan.Artifact, hooks)
				batchOutcomes[i] = outcome
				batchSteps[i] = steps
			}()
		}
		wg.Wait()

		// 汇聚批结果，保持 dep 顺序
		anyFailed := false
		for i := range batchOutcomes {
			outcomes = append(outcomes, batchOutcomes[i])
			allSteps = append(allSteps, batchSteps[i]...)
			if batchOutcomes[i].Status == HostStatusFailed {
				anyFailed = true
			}
		}
		if anyFailed {
			stopReason = "previous batch had failed host(s)"
		}
	}
	return outcomes, allSteps
}
