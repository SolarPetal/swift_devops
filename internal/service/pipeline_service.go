package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	Type     string                `json:"type"` // "step" | "status" | "snapshot"
	RunID    uint                  `json:"run_id"`
	Status   string                `json:"status,omitempty"`
	Step     *strategy.StepResult  `json:"step,omitempty"`
	Snap     *strategy.RunSnapshot `json:"snapshot,omitempty"`
	Ts       string                `json:"ts"`
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

// Trigger 触发一次部署。当前支持 strategy=single；rolling/rollback 留 Sprint 3.2/3.3。
func (s *PipelineService) Trigger(appID, artifactID uint, actor, strategyName string) (PipelineRunView, error) {
	if strategyName == "" {
		strategyName = "single"
	}
	strat, err := pickStrategy(strategyName)
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
	// 2. 校验 artifact
	art, err := s.artSvc.GetModel(artifactID)
	if err != nil {
		return PipelineRunView{}, err
	}
	if art.AppID != appID {
		return PipelineRunView{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("artifact %d 属于应用 %d，不是 %d", artifactID, art.AppID, appID), 400)
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

	// 5. 落库 running
	now := time.Now()
	snap := strategy.RunSnapshot{Strategy: strategyName, ArtifactID: artifactID, StartedAt: now.Format(time.RFC3339)}
	snapJSON, _ := json.Marshal(snap)
	run := &model.PipelineRun{
		AppID: appID, ArtifactID: artifactID,
		Strategy: strategyName, Status: RunStatusRunning,
		StateSnapshot: string(snapJSON),
		TriggeredBy:   actor,
		StartedAt:     &now,
	}
	if err := s.db.Create(run).Error; err != nil {
		s.releaseLock(appID)
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "create run", 500)
	}

	// 6. 为每个 deployment 创建 host 级 pending 行
	s.seedRunHosts(run.ID, deps)

	// 7. 异步执行（带 cancel ctx）
	ctx, cancel := context.WithCancel(context.Background())
	s.registerCancel(run.ID, cancel)
	go s.execute(ctx, run.ID, &app, art, deps, strat)

	// 8. 立刻推 status=running
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

func (s *PipelineService) execute(ctx context.Context, runID uint, app *model.Application, art *model.Artifact, deps []model.Deployment, strat strategy.Strategy) {
	defer s.releaseLock(app.ID)
	defer s.unregisterCancel(runID)

	envMap, envErr := deploy.ParseEnvVarsJSON(app.EnvVars)
	snap := strategy.RunSnapshot{
		Strategy:   strat.Name(),
		ArtifactID: art.ID,
		StartedAt:  time.Now().Format(time.RFC3339),
	}
	if envErr != nil {
		snap.Error = "env_vars 解析失败: " + envErr.Error()
		s.finishRun(runID, &snap, RunStatusFailed)
		s.publishStatus(runID, RunStatusFailed)
		return
	}

	// 总超时：每 host 给 2 分钟，加 30 秒余量
	totalTimeout := time.Duration(len(deps))*2*time.Minute + 30*time.Second
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, totalTimeout)
	defer cancelTimeout()

	plan := &strategy.Plan{
		RunID: runID, App: app, Artifact: art, Deps: deps, EnvMap: envMap,
	}
	env := strategy.Env{HostSvc: s.hostSvc, SSHOpts: s.sshOpts}
	hooks := &runHooks{svc: s, runID: runID}

	outcomes, steps := strat.Run(timeoutCtx, env, plan, hooks)
	snap.Steps = steps

	// 用户取消 vs 自然结束：看父 ctx 是不是被显式 Cancel 过
	cancelled := errors.Is(ctx.Err(), context.Canceled)
	finalStatus := strategy.AggregateStatus(outcomes, cancelled)

	s.finishRun(runID, &snap, finalStatus)
	s.publishStatus(runID, finalStatus)
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

// pickStrategy 名称 → 实例。未实现的策略一律拒绝。
func pickStrategy(name string) (strategy.Strategy, error) {
	switch name {
	case "single":
		return strategy.Single{}, nil
	default:
		return nil, apperr.New("BAD_REQUEST",
			fmt.Sprintf("strategy=%s 未实现（当前仅支持 single；rolling/rollback 见 Sprint 3.2/3.3）", name), 400)
	}
}

// --- Hooks 实现 ---

// runHooks 把策略产出的副作用桥接到 service：推 WS / 写 PipelineRunHost / 更新 deployment。
type runHooks struct {
	svc   *PipelineService
	runID uint
}

func (h *runHooks) OnStep(st strategy.StepResult) {
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

func (h *runHooks) OnDeploymentSuccess(dep *model.Deployment, newArtifactID uint) {
	if err := h.svc.db.Model(&model.Deployment{}).Where("id = ?", dep.ID).Updates(map[string]any{
		"previous_artifact_id": dep.CurrentArtifactID,
		"current_artifact_id":  newArtifactID,
		"status":               "running",
	}).Error; err != nil {
		slog.Warn("update deployment artifact", "dep_id", dep.ID, "err", err)
	}
}
