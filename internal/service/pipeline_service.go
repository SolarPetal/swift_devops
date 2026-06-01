package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
	"swift-devops/internal/service/pipeline/strategy"
)

// 旧 API 的类型别名 —— 把策略包里定义的类型在 service 包里暴露，
// 让 handler / 测试代码不用大改 import。
type (
	StepResult  = strategy.StepResult
	RunSnapshot = strategy.RunSnapshot
)

// 部署阶段名（向后兼容旧 import: service.StageDial 等）
const (
	StageDial    = strategy.StageDial
	StageUpload  = strategy.StageUpload
	StageUnit    = strategy.StageUnit
	StageRestart = strategy.StageRestart
	StageHealth  = strategy.StageHealth
)

// Pipeline run 整体状态
const (
	RunStatusRunning   = "running"
	RunStatusSuccess   = "success"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

// PipelineRunView 流水线响应视图
type PipelineRunView struct {
	ID            uint   `json:"id"`
	AppID         uint   `json:"app_id"`
	ArtifactID    uint   `json:"artifact_id"`
	Strategy      string `json:"strategy"`
	Status        string `json:"status"`
	StateSnapshot string `json:"state_snapshot"`
	TriggeredBy   string `json:"triggered_by"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// PipelineService 流水线编排（策略派发 + 互斥 + Cancel + 实时事件）
type PipelineService struct {
	db      *gorm.DB
	hostSvc *HostService
	artSvc  *ArtifactService
	sshOpts sshpkg.DialOptions
	pub     Publisher

	mu            sync.Mutex
	runningAppIDs map[uint]struct{}           // 同 app 互斥
	cancelFns     map[uint]context.CancelFunc // runID → cancel
}

// Publisher 推送实时事件到外部（WebSocket Hub）。
// Publish 必须非阻塞——满了丢帧，不能拖死 pipeline。
type Publisher interface {
	Publish(topic string, msg []byte) int
}

// PipelineTopic 单个 run 的 WS 主题命名约定。
func PipelineTopic(runID uint) string {
	return fmt.Sprintf("pipeline:%d", runID)
}

// PipelineEvent WS 帧标准 schema（前端按 type 分发）。
type PipelineEvent struct {
	Type   string                `json:"type"` // "step" | "status" | "snapshot"
	RunID  uint                  `json:"run_id"`
	Status string                `json:"status,omitempty"`
	Step   *strategy.StepResult  `json:"step,omitempty"`
	Snap   *strategy.RunSnapshot `json:"snapshot,omitempty"`
	Ts     string                `json:"ts"`
}

func NewPipelineService(db *gorm.DB, hostSvc *HostService, artSvc *ArtifactService, sshTimeout time.Duration) *PipelineService {
	if sshTimeout == 0 {
		sshTimeout = 10 * time.Second
	}
	return &PipelineService{
		db: db, hostSvc: hostSvc, artSvc: artSvc,
		sshOpts:       sshpkg.DialOptions{Timeout: sshTimeout},
		runningAppIDs: map[uint]struct{}{},
		cancelFns:     map[uint]context.CancelFunc{},
	}
}

func (s *PipelineService) SetPublisher(p Publisher) { s.pub = p }

// TriggerOptions Trigger 的可选参数容器。
// 未来 BlueGreen / Canary 的额外字段都加这里，避免 Trigger 签名继续膨胀。
type TriggerOptions struct {
	Strategy  string // "single" / "rolling" / "rollback"（"" 默认 single）
	BatchSize int    // rolling 专用：批大小（>=1）
	// Sprint X.6：BundleID > 0 → 走多 service Bundle 链路（artifactID 必填 0 或忽略）
	// 0 → 走老 Artifact 链路（artifactID 必填）
	BundleID uint
}

// Trigger 触发一次部署。
//   - opts.Strategy 决定走哪个策略；空串默认 single
//   - opts.BatchSize 仅 rolling 使用，<=0 时 rolling 拒绝
//   - 同 app 互斥；前一次未结束则返回 409
func (s *PipelineService) Trigger(appID, artifactID uint, actor string, opts TriggerOptions) (PipelineRunView, error) {
	if opts.Strategy == "" {
		opts.Strategy = "single"
	}
	strat, err := pickStrategy(opts)
	if err != nil {
		return PipelineRunView{}, err
	}

	// 1. 校验 app
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PipelineRunView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	// 2. 校验产物：Sprint X.6 优先走 Bundle 路径；BundleID==0 时回 Artifact 兼容
	var (
		art           *model.Artifact
		bundle        *model.ArtifactBundle
		itemByService map[string]*model.ArtifactItem
	)
	if opts.BundleID > 0 {
		b, items, berr := s.artSvc.GetBundle(opts.BundleID)
		if berr != nil {
			return PipelineRunView{}, berr
		}
		if b.AppID != appID {
			return PipelineRunView{}, apperr.New("BAD_REQUEST",
				fmt.Sprintf("bundle %d 属于应用 %d，不是 %d", opts.BundleID, b.AppID, appID), 400)
		}
		bundle = b
		itemByService = make(map[string]*model.ArtifactItem, len(items))
		for i := range items {
			itemByService[items[i].ServiceCode] = &items[i]
		}
	} else {
		a, aerr := s.artSvc.GetModel(artifactID)
		if aerr != nil {
			return PipelineRunView{}, aerr
		}
		if a.AppID != appID {
			return PipelineRunView{}, apperr.New("BAD_REQUEST",
				fmt.Sprintf("artifact %d 属于应用 %d，不是 %d", artifactID, a.AppID, appID), 400)
		}
		art = a
	}

	// 3. deployment 至少一个
	var deps []model.Deployment
	if err := s.db.Where("app_id = ?", appID).Order("id ASC").Find(&deps).Error; err != nil {
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "list deployments", 500)
	}
	if len(deps) == 0 {
		return PipelineRunView{}, apperr.New("BAD_REQUEST",
			"应用尚未绑定任何主机，无法部署", 400)
	}
	// 4. 同 app 互斥
	s.mu.Lock()
	if _, busy := s.runningAppIDs[appID]; busy {
		s.mu.Unlock()
		return PipelineRunView{}, apperr.New("CONFLICT",
			fmt.Sprintf("应用 %d 已有流水线在跑，请等待结束", appID), 409)
	}
	s.runningAppIDs[appID] = struct{}{}
	s.mu.Unlock()

	// 5. 解析 env vars（forward 路径专属：rollback 也用同样的 env）
	envMap, envErr := deploy.ParseEnvVarsJSON(app.EnvVars)
	if envErr != nil {
		s.releaseLock(appID)
		return PipelineRunView{}, apperr.Wrap(envErr, "BAD_REQUEST",
			"env_vars 解析失败: "+envErr.Error(), 400)
	}

	// 6. 落库 running
	now := time.Now()
	snap := strategy.RunSnapshot{Strategy: opts.Strategy, ArtifactID: artifactID, StartedAt: now.Format(time.RFC3339)}
	snapJSON, _ := json.Marshal(snap)
	run := &model.PipelineRun{
		AppID: appID, ArtifactID: artifactID,
		Strategy: opts.Strategy, Status: RunStatusRunning,
		StateSnapshot: string(snapJSON),
		TriggeredBy:   actor,
		StartedAt:     &now,
	}
	if bundle != nil {
		run.BundleID = bundle.ID
	}
	if err := s.db.Create(run).Error; err != nil {
		s.releaseLock(appID)
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "create run", 500)
	}

	// 7. 为每个 deployment 创建 host 级 pending 行
	s.seedRunHosts(run.ID, deps)

	// 8. 异步执行（带 cancel ctx）
	plan := &strategy.Plan{
		RunID: run.ID, App: &app, Artifact: art, Deps: deps, EnvMap: envMap,
		BatchSize: opts.BatchSize,
	}
	if bundle != nil {
		plan.Bundle = bundle
		plan.ItemByServiceCode = itemByService
	}

	// 蓝绿专属：自动选目标组 + 注入 NginxApply 闭包
	if opts.Strategy == "blue_green" {
		if err := s.injectBlueGreenPlan(&app, plan); err != nil {
			s.releaseLock(appID)
			// 标记 run 为 failed（已落库）；不写 snapshot 避免误导前端
			now := time.Now()
			_ = s.db.Model(&model.PipelineRun{}).Where("id = ?", run.ID).Updates(map[string]any{
				"status": RunStatusFailed, "finished_at": &now,
			}).Error
			return PipelineRunView{}, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.registerCancel(run.ID, cancel)
	go s.execute(ctx, run.ID, &app, plan, strat)

	// 9. 立刻推 status=running
	s.publishStatus(run.ID, RunStatusRunning)

	return toRunView(run), nil
}

// Cancel 请求取消一个正在跑的 run。
//   - run 不存在 → NOT_FOUND
//   - run 已结束（success/failed/cancelled）→ CONFLICT
//   - cancel 触发后异步生效，立刻返回；最终 status 由 execute 收口决定
//   - 进程重启后旧 running run 没有 cancelFn → 直接落 DB 标 cancelled 兜底
func (s *PipelineService) Cancel(runID uint) error {
	var r model.PipelineRun
	if err := s.db.First(&r, runID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound
		}
		return apperr.Wrap(err, "INTERNAL", "get run", 500)
	}
	if r.Status != RunStatusRunning {
		return apperr.New("CONFLICT", fmt.Sprintf("run 已结束（%s），无法取消", r.Status), 409)
	}
	s.mu.Lock()
	cancel, ok := s.cancelFns[runID]
	s.mu.Unlock()
	if !ok {
		return s.markCancelledOrphan(runID)
	}
	cancel()
	return nil
}

// List / Get 不变
func (s *PipelineService) List(appID uint) ([]PipelineRunView, error) {
	var rs []model.PipelineRun
	q := s.db.Order("id DESC").Limit(200)
	if appID > 0 {
		q = q.Where("app_id = ?", appID)
	}
	if err := q.Find(&rs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list runs", 500)
	}
	vs := make([]PipelineRunView, len(rs))
	for i := range rs {
		vs[i] = toRunView(&rs[i])
	}
	return vs, nil
}

// Rollback 触发一键回滚。
//   - 每个 deployment 退到自己的 previous_artifact_item_id (Sprint X.7 优先) 或 previous_artifact_id (旧路径兼容)
//   - 至少一台主机有 previous → 才能触发；全无 → BAD_REQUEST
//   - 制品已被清理（GetModel ErrNotFound）的 dep 仍可执行，由 strategy 标 skipped
//   - 同 app 互斥
//
// Sprint X.7：整组 Bundle 回滚语义（Q2 决策 A）：
//   - 所有 dep 都按 previous_artifact_item_id 取本 service 的 prev item
//   - 各 service 退到自己的上一个 item，配合 wave 调度按 startup_order 回退
//   - 当某 dep 没有 item 历史（X.6 之前数据）→ fallback 老 artifact 路径
func (s *PipelineService) Rollback(appID uint, actor string) (PipelineRunView, error) {
	// 1. 校验 app
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PipelineRunView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	// 2. 查 deployments
	var deps []model.Deployment
	if err := s.db.Where("app_id = ?", appID).Order("id ASC").Find(&deps).Error; err != nil {
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "list deployments", 500)
	}
	if len(deps) == 0 {
		return PipelineRunView{}, apperr.New("BAD_REQUEST", "应用尚未绑定任何主机，无法回滚", 400)
	}

	// 3. 预查 previous：优先 ArtifactItem (X.7)，落空回 Artifact (兼容旧数据)
	//    至少一台主机能回滚才能触发。
	itemByDep := map[uint]*model.ArtifactItem{}
	artByDep := map[uint]*model.Artifact{}
	var previousBundleID uint
	for i := range deps {
		dep := &deps[i]
		// 优先：ArtifactItem 链路
		if dep.PreviousArtifactItemID > 0 {
			var item model.ArtifactItem
			if err := s.db.First(&item, dep.PreviousArtifactItemID).Error; err == nil {
				itemByDep[dep.ID] = &item
				if previousBundleID == 0 {
					previousBundleID = item.BundleID
				}
				continue
			} else {
				slog.Warn("rollback: previous item missing",
					"dep_id", dep.ID, "previous_item_id", dep.PreviousArtifactItemID, "err", err)
			}
		}
		// fallback：旧 Artifact 链路
		if dep.PreviousArtifactID > 0 {
			art, err := s.artSvc.GetModel(dep.PreviousArtifactID)
			if err != nil {
				slog.Warn("rollback: previous artifact missing",
					"dep_id", dep.ID, "previous_id", dep.PreviousArtifactID, "err", err)
				continue
			}
			artByDep[dep.ID] = art
		}
	}
	if len(itemByDep) == 0 && len(artByDep) == 0 {
		return PipelineRunView{}, apperr.New("BAD_REQUEST",
			"应用所有主机都没有可回滚的历史版本", 400)
	}

	// 4. env vars
	envMap, envErr := deploy.ParseEnvVarsJSON(app.EnvVars)
	if envErr != nil {
		return PipelineRunView{}, apperr.Wrap(envErr, "BAD_REQUEST",
			"env_vars 解析失败: "+envErr.Error(), 400)
	}

	// 5. 同 app 互斥
	s.mu.Lock()
	if _, busy := s.runningAppIDs[appID]; busy {
		s.mu.Unlock()
		return PipelineRunView{}, apperr.New("CONFLICT",
			fmt.Sprintf("应用 %d 已有流水线在跑，请等待结束", appID), 409)
	}
	s.runningAppIDs[appID] = struct{}{}
	s.mu.Unlock()

	// 6. 落库 running（rollback artifact_id=0 标记，具体 art per-dep 在 plan 里）
	now := time.Now()
	snap := strategy.RunSnapshot{Strategy: "rollback", StartedAt: now.Format(time.RFC3339)}
	snapJSON, _ := json.Marshal(snap)
	run := &model.PipelineRun{
		AppID: appID, ArtifactID: 0,
		PreviousBundleID: previousBundleID, // Sprint X.7：回滚目标 Bundle 留痕
		Strategy:         "rollback", Status: RunStatusRunning,
		StateSnapshot: string(snapJSON),
		TriggeredBy:   actor,
		StartedAt:     &now,
	}
	if err := s.db.Create(run).Error; err != nil {
		s.releaseLock(appID)
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "create run", 500)
	}

	// 7. seed host rows + 异步执行
	s.seedRunHosts(run.ID, deps)
	plan := &strategy.Plan{
		RunID: run.ID, App: &app, Deps: deps, EnvMap: envMap,
		ArtifactByDepID: artByDep,
		ItemByDepID:     itemByDep,
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.registerCancel(run.ID, cancel)
	go s.execute(ctx, run.ID, &app, plan, strategy.Rollback{})

	s.publishStatus(run.ID, RunStatusRunning)
	return toRunView(run), nil
}

func (s *PipelineService) Get(id uint) (PipelineRunView, error) {
	var r model.PipelineRun
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PipelineRunView{}, apperr.ErrNotFound
		}
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "get run", 500)
	}
	return toRunView(&r), nil
}

// --- 异步执行 ---

// execute 通用流水线执行器。caller（Trigger / Rollback）负责组装 plan 和挑 strat。
//   - app 用于 releaseLock(app.ID) 和总超时计算
//   - plan.Deps 决定总超时和 PipelineRunHost 数量
//
// Sprint X.3：当 app 已配置 AppService 时按 wave 调度（多 service 并发部署）；
// 未配置时退化为单次 strat.Run，保持旧链路完全兼容。
func (s *PipelineService) execute(ctx context.Context, runID uint, app *model.Application, plan *strategy.Plan, strat strategy.Strategy) {
	defer s.releaseLock(app.ID)
	defer s.unregisterCancel(runID)

	snap := strategy.RunSnapshot{
		Strategy:  strat.Name(),
		StartedAt: time.Now().Format(time.RFC3339),
	}
	if plan.Artifact != nil {
		snap.ArtifactID = plan.Artifact.ID
	}

	// 总超时：每 host 给 2 分钟，加 30 秒余量；多 service 时不放大（同 host 多 service 顺序部署）
	totalTimeout := time.Duration(len(plan.Deps))*2*time.Minute + 30*time.Second
	if totalTimeout < 2*time.Minute {
		totalTimeout = 2 * time.Minute
	}
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, totalTimeout)
	defer cancelTimeout()

	env := strategy.Env{HostSvc: s.hostSvc, SSHOpts: s.sshOpts}
	hooks := &runHooks{svc: s, runID: runID}

	var (
		outcomes []strategy.HostOutcome
		steps    []strategy.StepResult
	)

	// Sprint X.3：查 app 是否已配置 AppService
	services, svcErr := s.loadEnabledServices(app.ID)
	if svcErr != nil {
		slog.Warn("load app services failed, fallback to legacy single-jar path", "app_id", app.ID, "err", svcErr)
	}

	if len(services) == 0 {
		// 旧链路：单 jar 模式直接调 strat.Run，plan.Service == nil 触发 resolveServiceConfig 走 app 字段
		outcomes, steps = strat.Run(timeoutCtx, env, plan, hooks)
	} else {
		// 多 service：按 startup_order 分波并发
		waves := strategy.PartitionWaves(services)
		build := s.buildSubPlanFunc(plan, strat, services)
		outcomes, steps = strategy.RunWaves(timeoutCtx, env, waves, build, hooks)
	}
	snap.Steps = steps

	// 用户取消 vs 自然结束：看父 ctx 是不是被显式 Cancel 过
	cancelled := errors.Is(ctx.Err(), context.Canceled)
	finalStatus := strategy.AggregateStatus(outcomes, cancelled)

	s.finishRun(runID, &snap, finalStatus)
	s.publishStatus(runID, finalStatus)
}

// loadEnabledServices 查 app 已启用的 AppService（按 startup_order ASC）。
// 返回空切片 + nil err 表示 app 没配 service（走旧链路）。
func (s *PipelineService) loadEnabledServices(appID uint) ([]model.AppService, error) {
	var services []model.AppService
	if err := s.db.Where("app_id = ? AND enabled = ?", appID, true).
		Order("startup_order ASC, id ASC").Find(&services).Error; err != nil {
		return nil, err
	}
	return services, nil
}

// buildSubPlanFunc 构造 SubPlanBuilder，给 RunWaves 用。
//
// 每个 service 拿到自己的 sub-plan：
//   - sub.Service / sub.Deps（按 service_code 过滤）只属于该 service
//   - forward 单 jar 旧链路：sub.Artifact 仍指向 plan.Artifact（兼容期）
//   - forward 多 service Bundle 链路：sub.Artifact 由 plan.Bundle 的 service 对应 item 包装
//   - rollback 多 service Item 链路：sub.ArtifactByDepID 由 plan.ItemByDepID 按本 service 过滤后逐 dep 包装 (X.7)
//   - rollback 单 jar 旧链路：sub.ArtifactByDepID 直接按本 service dep 过滤 (X.7 fallback)
func (s *PipelineService) buildSubPlanFunc(plan *strategy.Plan, strat strategy.Strategy, services []model.AppService) strategy.SubPlanBuilder {
	return func(svc *model.AppService) (*strategy.Plan, strategy.Strategy, error) {
		sub := *plan // 浅拷贝
		sub.Service = svc
		sub.Deps = depsForService(plan.Deps, svc.ServiceCode)
		if len(sub.Deps) == 0 {
			// 没有任何 host 绑定该 service → 跳过
			return nil, nil, nil
		}
		// 解析该 service 的 EnvVars（per-service 优先）
		if envMap, err := deploy.ParseEnvVarsJSON(svc.EnvVars); err == nil && len(envMap) > 0 {
			sub.EnvMap = envMap
		}
		// Sprint X.7：rollback 优先 ItemByDepID（每 dep 包装临时 Artifact 透传给 strategy.Rollback）
		if len(plan.ItemByDepID) > 0 {
			subMap := map[uint]*model.Artifact{}
			subItemMap := map[uint]*model.ArtifactItem{}
			for _, d := range sub.Deps {
				if it, ok := plan.ItemByDepID[d.ID]; ok {
					subItemMap[d.ID] = it
					subMap[d.ID] = &model.Artifact{
						ID:       0,
						AppID:    plan.App.ID,
						FileName: it.FileName,
						FilePath: it.FilePath,
						FileMD5:  it.FileMD5,
						FileSize: it.FileSize,
					}
				}
			}
			// 旧 ArtifactByDepID 作为 fallback（item 缺时回老路径）
			for _, d := range sub.Deps {
				if _, has := subMap[d.ID]; has {
					continue
				}
				if a, ok := plan.ArtifactByDepID[d.ID]; ok {
					subMap[d.ID] = a
				}
			}
			if len(subMap) == 0 {
				return nil, nil, nil // 该 service 没有可回滚的历史
			}
			sub.ArtifactByDepID = subMap
			sub.ItemByDepID = subItemMap
			return &sub, strat, nil
		}
		// rollback 旧链路：plan.ArtifactByDepID
		if len(plan.ArtifactByDepID) > 0 {
			subMap := map[uint]*model.Artifact{}
			for _, d := range sub.Deps {
				if a, ok := plan.ArtifactByDepID[d.ID]; ok {
					subMap[d.ID] = a
				}
			}
			sub.ArtifactByDepID = subMap
			if len(subMap) == 0 {
				return nil, nil, nil // 该 service 没有 previous artifact
			}
			return &sub, strat, nil
		}
		// Sprint X.6：forward 走 Bundle 链路 —— 用本 service 对应的 ArtifactItem
		if plan.Bundle != nil && plan.ItemByServiceCode != nil {
			item, ok := plan.ItemByServiceCode[svc.ServiceCode]
			if !ok {
				// 该 service 在 Bundle 里没有产物 → 跳过（构建侧应该已经报错，这里防御）
				return nil, nil, nil
			}
			sub.Item = item
			// 包装成临时 Artifact 透传给 strategy（strategy 内部 dispatcher.Upload 只读 FilePath/FileMD5）
			sub.Artifact = &model.Artifact{
				ID:       0, // 用 0 表示这是 Bundle Item 适配,不指向真实 artifacts 行
				AppID:    plan.App.ID,
				FileName: item.FileName,
				FilePath: item.FilePath,
				FileMD5:  item.FileMD5,
				FileSize: item.FileSize,
			}
			return &sub, strat, nil
		}
		// forward 兼容：plan.Artifact 仍指向旧 Artifact（单 service "default" 路径）
		return &sub, strat, nil
	}
}

// depsForService 按 service_code 过滤 deps。
// 兼容矩阵：
//   - 多 service 期间 svcCode 非空 → 严格匹配 dep.ServiceCode
//   - svcCode 为 "default" → 匹配 dep.ServiceCode == "" || "default"（兼容旧数据）
func depsForService(deps []model.Deployment, svcCode string) []model.Deployment {
	out := make([]model.Deployment, 0, len(deps))
	isDefault := svcCode == "" || svcCode == "default"
	for i := range deps {
		c := deps[i].ServiceCode
		if isDefault {
			if c == "" || c == "default" {
				out = append(out, deps[i])
			}
		} else {
			if c == svcCode {
				out = append(out, deps[i])
			}
		}
	}
	return out
}

// seedRunHosts 为本次 run 的每台主机预建一行 PipelineRunHost（status=pending）。
// 失败仅记日志：表少几行不影响主流程。
func (s *PipelineService) seedRunHosts(runID uint, deps []model.Deployment) {
	rows := make([]model.PipelineRunHost, 0, len(deps))
	for i := range deps {
		rows = append(rows, model.PipelineRunHost{
			RunID:        runID,
			HostID:       deps[i].HostID,
			DeploymentID: deps[i].ID,
			Status:       strategy.HostStatusPending,
		})
	}
	if len(rows) == 0 {
		return
	}
	if err := s.db.Create(&rows).Error; err != nil {
		slog.Warn("seed run hosts", "run_id", runID, "err", err)
	}
}

func (s *PipelineService) releaseLock(appID uint) {
	s.mu.Lock()
	delete(s.runningAppIDs, appID)
	s.mu.Unlock()
}

func (s *PipelineService) registerCancel(runID uint, cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancelFns[runID] = cancel
	s.mu.Unlock()
}

func (s *PipelineService) unregisterCancel(runID uint) {
	s.mu.Lock()
	delete(s.cancelFns, runID)
	s.mu.Unlock()
}

// markCancelledOrphan 处理一个没有 cancelFn 的 running run（通常是进程重启后的孤儿）。
// 直接落 DB 标 cancelled。
func (s *PipelineService) markCancelledOrphan(runID uint) error {
	now := time.Now()
	return s.db.Model(&model.PipelineRun{}).Where("id = ? AND status = ?", runID, RunStatusRunning).
		Updates(map[string]any{
			"status":      RunStatusCancelled,
			"finished_at": &now,
		}).Error
}

func (s *PipelineService) finishRun(runID uint, snap *strategy.RunSnapshot, status string) {
	snap.FinishedAt = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(snap)
	if err != nil {
		slog.Error("marshal snapshot", "err", err)
		data = []byte(`{"error":"marshal_snapshot_failed"}`)
	}
	now := time.Now()
	if err := s.db.Model(&model.PipelineRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status":         status,
		"state_snapshot": string(data),
		"finished_at":    &now,
	}).Error; err != nil {
		slog.Error("finish run", "id", runID, "err", err)
	}
}

// publishStep / publishStatus / SnapshotEventBytes 保持原行为
func (s *PipelineService) publishStep(runID uint, st strategy.StepResult) {
	if s.pub == nil {
		return
	}
	data, err := json.Marshal(PipelineEvent{
		Type: "step", RunID: runID, Step: &st,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("marshal pipeline step event", "err", err)
		return
	}
	s.pub.Publish(PipelineTopic(runID), data)
}

// appendSnapshotStep 把实时 step 同步追加到 PipelineRun.StateSnapshot。
//
// 这不是为了最终结果（finishRun 会写完整 snapshot），而是为了「晚连上的」前端：
// 部署触发后 dial/env_check/upload 可能很快完成，Drawer 的 WS 连接稍晚才建立；
// 如果 DB snapshot 仍是空，首帧 snapshot 就会显示“还没有步骤记录”，直到下一个 step 才刷新。
// 逐步落库后，任何时刻连上都能看到已完成阶段。
func (s *PipelineService) appendSnapshotStep(runID uint, st strategy.StepResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var r model.PipelineRun
	if err := s.db.Select("id", "strategy", "artifact_id", "state_snapshot", "started_at").First(&r, runID).Error; err != nil {
		slog.Warn("append snapshot step: load run", "run_id", runID, "err", err)
		return
	}
	var snap strategy.RunSnapshot
	if strings.TrimSpace(r.StateSnapshot) != "" {
		_ = json.Unmarshal([]byte(r.StateSnapshot), &snap)
	}
	if snap.Strategy == "" {
		snap.Strategy = r.Strategy
	}
	if snap.ArtifactID == 0 {
		snap.ArtifactID = r.ArtifactID
	}
	if snap.StartedAt == "" {
		if r.StartedAt != nil {
			snap.StartedAt = r.StartedAt.Format(time.RFC3339)
		} else {
			snap.StartedAt = time.Now().Format(time.RFC3339)
		}
	}
	snap.Steps = append(snap.Steps, st)
	data, err := json.Marshal(&snap)
	if err != nil {
		slog.Warn("append snapshot step: marshal", "run_id", runID, "err", err)
		return
	}
	if err := s.db.Model(&model.PipelineRun{}).Where("id = ?", runID).
		Update("state_snapshot", string(data)).Error; err != nil {
		slog.Warn("append snapshot step: update", "run_id", runID, "err", err)
	}
}

func (s *PipelineService) publishStatus(runID uint, status string) {
	if s.pub == nil {
		return
	}
	data, err := json.Marshal(PipelineEvent{
		Type: "status", RunID: runID, Status: status,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("marshal pipeline status event", "err", err)
		return
	}
	s.pub.Publish(PipelineTopic(runID), data)
}

func (s *PipelineService) SnapshotEventBytes(runID uint) []byte {
	v, err := s.Get(runID)
	if err != nil {
		return nil
	}
	var snap strategy.RunSnapshot
	if v.StateSnapshot != "" {
		_ = json.Unmarshal([]byte(v.StateSnapshot), &snap)
	}
	data, err := json.Marshal(PipelineEvent{
		Type: "snapshot", RunID: runID, Status: v.Status, Snap: &snap,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return nil
	}
	return data
}

func toRunView(r *model.PipelineRun) PipelineRunView {
	v := PipelineRunView{
		ID: r.ID, AppID: r.AppID, ArtifactID: r.ArtifactID,
		Strategy: r.Strategy, Status: r.Status,
		StateSnapshot: r.StateSnapshot,
		TriggeredBy:   r.TriggeredBy,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
	}
	if r.StartedAt != nil {
		v.StartedAt = r.StartedAt.Format(time.RFC3339)
	}
	if r.FinishedAt != nil {
		v.FinishedAt = r.FinishedAt.Format(time.RFC3339)
	}
	return v
}

// injectBlueGreenPlan 在 Trigger 内部为 blue_green 策略组装 plan：
//   - 校验 App 已配 NginxHostID + NginxUpstreamName
//   - 自动选目标组：active="blue" → target="green"，反之亦然，空 → "blue"（首次部署）
//   - 预查目标组每台主机的 IP:Port（effectivePort），组装成 NginxBackend 列表
//   - 组装 NginxApply 闭包（dial nginx host → NewNginxApplier → Apply）
//
// 返回 *apperr.Error；调用方在错误时已 releaseLock + 收口 run。
func (s *PipelineService) injectBlueGreenPlan(app *model.Application, plan *strategy.Plan) error {
	if app.NginxHostID == 0 || strings.TrimSpace(app.NginxUpstreamName) == "" {
		return apperr.New("BAD_REQUEST",
			"应用未配置 nginx_host_id / nginx_upstream_name，无法蓝绿部署", 400)
	}
	target := pickTargetGroup(app.ActiveGroup)
	plan.TargetGroup = target

	// 预查目标组主机 IP / effective port，用于 NginxApply 闭包
	type hostRow struct {
		ID   uint
		IP   string
		Port int
	}
	var rows []hostRow
	for i := range plan.Deps {
		d := &plan.Deps[i]
		if d.GroupTag != target {
			continue
		}
		var h model.Host
		if err := s.db.Select("id", "ip").First(&h, d.HostID).Error; err != nil {
			return apperr.Wrap(err, "INTERNAL", "lookup host for blue_green", 500)
		}
		port := d.Port
		if port == 0 {
			port = app.Port
		}
		rows = append(rows, hostRow{ID: h.ID, IP: h.IP, Port: port})
	}
	if len(rows) == 0 {
		return apperr.New("BAD_REQUEST",
			fmt.Sprintf("目标组 %q 没有绑定主机；请先在主机绑定页给目标组添加主机", target), 400)
	}

	backends := make([]deploy.NginxBackend, 0, len(rows))
	for _, r := range rows {
		backends = append(backends, deploy.NginxBackend{IP: r.IP, Port: r.Port})
	}
	upstream := deploy.NginxUpstream{
		AppCode: app.AppCode, UpstreamName: app.NginxUpstreamName, Servers: backends,
	}
	nginxHostID := app.NginxHostID
	dialOpts := s.sshOpts

	plan.NginxApply = func(ctx context.Context) error {
		t, auth, _, err := s.hostSvc.LoadAuth(nginxHostID)
		if err != nil {
			return fmt.Errorf("load nginx host auth: %w", err)
		}
		c, err := sshpkg.Dial(t, auth, dialOpts)
		if err != nil {
			return fmt.Errorf("dial nginx host: %w", err)
		}
		defer c.Close()
		return deploy.NewNginxApplier(c).Apply(ctx, upstream)
	}
	return nil
}

// pickTargetGroup 根据当前活跃组选目标组：active=blue → green，active=green → blue，空 → blue（首次部署）
func pickTargetGroup(active string) string {
	switch strings.TrimSpace(active) {
	case "green":
		return "blue"
	case "blue":
		return "green"
	default:
		return "blue"
	}
}
func pickStrategy(opts TriggerOptions) (strategy.Strategy, error) {
	switch opts.Strategy {
	case "single":
		return strategy.Single{}, nil
	case "rolling":
		if opts.BatchSize < 1 {
			return nil, apperr.New("BAD_REQUEST",
				"strategy=rolling 时 batch_size 必须 >= 1", 400)
		}
		return strategy.Rolling{}, nil
	case "blue_green":
		return strategy.BlueGreen{}, nil
	default:
		return nil, apperr.New("BAD_REQUEST",
			fmt.Sprintf("strategy=%s 未实现（当前支持 single / rolling / blue_green；rollback 见 /apps/:id/rollback）", opts.Strategy), 400)
	}
}

// --- Hooks 实现 ---

// runHooks 把策略产出的副作用桥接到 service：推 WS / 写 PipelineRunHost / 更新 deployment。
type runHooks struct {
	svc   *PipelineService
	runID uint
}

func (h *runHooks) OnStep(st strategy.StepResult) {
	h.svc.appendSnapshotStep(h.runID, st)
	h.svc.publishStep(h.runID, st)
}

func (h *runHooks) OnHostStatus(deploymentID, hostID uint, status, stage, errMsg string) {
	now := time.Now()
	updates := map[string]any{
		"status":        status,
		"current_stage": stage,
		"error":         errMsg,
		"updated_at":    now,
	}
	switch status {
	case strategy.HostStatusRunning:
		updates["started_at"] = &now
	case strategy.HostStatusSuccess, strategy.HostStatusFailed, strategy.HostStatusSkipped:
		updates["ended_at"] = &now
	}
	if err := h.svc.db.Model(&model.PipelineRunHost{}).
		Where("run_id = ? AND host_id = ?", h.runID, hostID).
		Updates(updates).Error; err != nil {
		slog.Warn("update run host", "run_id", h.runID, "host_id", hostID, "err", err)
	}
}

func (h *runHooks) OnDeploymentSuccess(dep *model.Deployment, newArtifactID, newArtifactItemID uint) {
	updates := map[string]any{
		"status": "running",
	}
	// 旧字段（X.8 删）：单 service 链路或兼容期
	if newArtifactID > 0 {
		updates["previous_artifact_id"] = dep.CurrentArtifactID
		updates["current_artifact_id"] = newArtifactID
	}
	// Sprint X.6 新字段：多 service Bundle 链路
	if newArtifactItemID > 0 {
		updates["previous_artifact_item_id"] = dep.CurrentArtifactItemID
		updates["current_artifact_item_id"] = newArtifactItemID
	}
	if err := h.svc.db.Model(&model.Deployment{}).Where("id = ?", dep.ID).Updates(updates).Error; err != nil {
		slog.Warn("update deployment artifact", "dep_id", dep.ID, "err", err)
	}
}

// OnGroupSwitched 蓝绿切流成功后调用，把 App.active_group 更新为目标组。
// 仅对 strategy=blue_green 有效；其他策略不调用此 hook。
func (h *runHooks) OnGroupSwitched(group string) {
	if err := h.svc.db.Model(&model.Application{}).
		Where("id = (?)",
			h.svc.db.Model(&model.PipelineRun{}).Select("app_id").Where("id = ?", h.runID)).
		Update("active_group", group).Error; err != nil {
		slog.Warn("update app active_group", "run_id", h.runID, "group", group, "err", err)
	}
}
