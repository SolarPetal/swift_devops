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
)

// 部署阶段名（StateSnapshot 里固定字符串，前端按此渲染）
const (
	StageDial     = "dial"
	StageUpload   = "upload"
	StageUnit     = "write_unit"
	StageRestart  = "restart"
	StageHealth   = "health"
)

// PipelineRunView 流水线响应视图
type PipelineRunView struct {
	ID            uint   `json:"id"`
	AppID         uint   `json:"app_id"`
	ArtifactID    uint   `json:"artifact_id"`
	Strategy      string `json:"strategy"`
	Status        string `json:"status"`
	StateSnapshot string `json:"state_snapshot"` // JSON
	TriggeredBy   string `json:"triggered_by"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// StepResult 单步骤结果
type StepResult struct {
	HostID    uint   `json:"host_id"`
	HostName  string `json:"host_name"`
	HostIP    string `json:"host_ip"`
	Stage     string `json:"stage"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
}

// RunSnapshot 流水线快照（写到 PipelineRun.StateSnapshot）
type RunSnapshot struct {
	Strategy   string       `json:"strategy"`
	ArtifactID uint         `json:"artifact_id"`
	StartedAt  string       `json:"started_at"`
	FinishedAt string       `json:"finished_at,omitempty"`
	Steps      []StepResult `json:"steps"`
	Error      string       `json:"error,omitempty"`
}

// PipelineService 流水线编排
type PipelineService struct {
	db      *gorm.DB
	hostSvc *HostService
	artSvc  *ArtifactService
	sshOpts sshpkg.DialOptions

	// 异步任务用：限制同 app 同时只能跑一个 pipeline，避免冲突
	mu       sync.Mutex
	runningAppIDs map[uint]struct{}
}

// NewPipelineService 构造。sshTimeout 单次拨号超时；总执行超时见 Trigger 内部。
func NewPipelineService(db *gorm.DB, hostSvc *HostService, artSvc *ArtifactService, sshTimeout time.Duration) *PipelineService {
	if sshTimeout == 0 {
		sshTimeout = 10 * time.Second
	}
	return &PipelineService{
		db: db, hostSvc: hostSvc, artSvc: artSvc,
		sshOpts:       sshpkg.DialOptions{Timeout: sshTimeout},
		runningAppIDs: map[uint]struct{}{},
	}
}

// Trigger 触发一次单主机部署。
//   - 校验 app / artifact 一致；至少有一个 deployment
//   - 同 app 已有 pipeline running → 拒绝
//   - 异步执行；同步返回 run（status=running）
func (s *PipelineService) Trigger(appID, artifactID uint, actor, strategy string) (PipelineRunView, error) {
	if strategy == "" {
		strategy = "single"
	}
	if strategy != "single" {
		return PipelineRunView{}, apperr.New("BAD_REQUEST", "Sprint 2.4 仅支持 strategy=single", 400)
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
	// 3. 至少一个 deployment
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

	// 5. 落库 pending
	now := time.Now()
	snap := RunSnapshot{Strategy: strategy, ArtifactID: artifactID, StartedAt: now.Format(time.RFC3339)}
	snapJSON, _ := json.Marshal(snap)
	run := &model.PipelineRun{
		AppID: appID, ArtifactID: artifactID,
		Strategy: strategy, Status: "running",
		StateSnapshot: string(snapJSON),
		TriggeredBy:   actor,
		StartedAt:     &now,
	}
	if err := s.db.Create(run).Error; err != nil {
		s.releaseLock(appID)
		return PipelineRunView{}, apperr.Wrap(err, "INTERNAL", "create run", 500)
	}

	// 6. 异步执行
	go s.execute(run.ID, &app, art, deps, strategy)

	return toRunView(run), nil
}

// List 流水线列表（最新优先）。appID=0 列全部。
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

// Get 取单个流水线
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

func (s *PipelineService) releaseLock(appID uint) {
	s.mu.Lock()
	delete(s.runningAppIDs, appID)
	s.mu.Unlock()
}

func (s *PipelineService) execute(runID uint, app *model.Application, art *model.Artifact, deps []model.Deployment, strategy string) {
	defer s.releaseLock(app.ID)

	// 解析 env vars（一次性，复用给所有 host）
	envMap, envErr := deploy.ParseEnvVarsJSON(app.EnvVars)
	var snap RunSnapshot
	snap.Strategy = strategy
	snap.ArtifactID = art.ID
	snap.StartedAt = time.Now().Format(time.RFC3339)

	// 总执行超时：所有 host 串行，给每个 host 2 分钟，外加 30 秒余量
	totalTimeout := time.Duration(len(deps))*2*time.Minute + 30*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()

	if envErr != nil {
		snap.Error = "env_vars 解析失败: " + envErr.Error()
		s.finishRun(runID, &snap, "failed")
		return
	}

	finalStatus := "success"
	for i := range deps {
		dep := &deps[i]
		steps := s.deployOne(ctx, app, art, dep, envMap)
		snap.Steps = append(snap.Steps, steps...)
		if !lastStepOK(steps) {
			finalStatus = "failed"
			// 单主机策略：任一失败立刻停，避免对后续主机继续做无效工作
			break
		}
		// host 成功 → 更新 deployment 状态与制品指针
		s.db.Model(&model.Deployment{}).Where("id = ?", dep.ID).Updates(map[string]any{
			"previous_artifact_id": dep.CurrentArtifactID,
			"current_artifact_id":  art.ID,
			"status":               "running",
		})
	}

	s.finishRun(runID, &snap, finalStatus)
}

// deployOne 部署到单台 host，返回该 host 的 step 序列（按阶段顺序）。
// 任一阶段失败立刻返回，不再走后续阶段。
func (s *PipelineService) deployOne(ctx context.Context, app *model.Application, art *model.Artifact, dep *model.Deployment, envMap map[string]string) []StepResult {
	var steps []StepResult

	// 阶段 1：拨号
	target, auth, host, err := s.hostSvc.LoadAuth(dep.HostID)
	if err != nil {
		// host 都查不到，直接构造一个 dial 失败步骤
		return append(steps, mkStep(dep.HostID, "", "", StageDial, false, "", "load auth: "+err.Error()))
	}
	dialStart := time.Now()
	client, err := sshpkg.Dial(target, auth, s.sshOpts)
	if err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageDial, false, "", err.Error(), dialStart))
	}
	defer client.Close()
	// TOFU：把首次学到的 host key 落库
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		s.hostSvc.RecordHostKey(host.ID, client.LearnedHostKey())
	}
	steps = append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageDial, true,
		fmt.Sprintf("connected %s@%s:%d", target.User, target.IP, target.Port), "", dialStart))

	dispatcher := deploy.NewDispatcher(client.SSHClient())
	sysctl := deploy.NewSystemctl(client)

	spec := deploy.AppSpec{
		AppCode:        app.AppCode,
		DeployPath:     app.DeployPath,
		JvmArgs:        app.JvmArgs,
		Port:           effectivePort(dep, app),
		HealthCheckURL: app.HealthCheckURL,
		EnvVars:        envMap,
		User:           app.SystemdUser,
	}

	// 阶段 2：上传 jar
	upStart := time.Now()
	md5sum, err := dispatcher.Upload(art.FilePath, spec.JarPath())
	if err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUpload, false, "", err.Error(), upStart))
	}
	if md5sum != art.FileMD5 {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUpload, false, "",
			fmt.Sprintf("md5 mismatch: local=%s remote=%s", art.FileMD5, md5sum), upStart))
	}
	steps = append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUpload, true,
		fmt.Sprintf("uploaded %s (%d bytes, md5=%s)", spec.JarPath(), art.FileSize, md5sum), "", upStart))

	// 阶段 3：写 unit 文件
	unitStart := time.Now()
	unitText, err := deploy.RenderUnit(spec)
	if err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUnit, false, "", "render: "+err.Error(), unitStart))
	}
	if err := dispatcher.WriteFile(spec.UnitPath(), unitText, 0o644); err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUnit, false, "", "write unit: "+err.Error(), unitStart))
	}
	steps = append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageUnit, true, spec.UnitPath(), "", unitStart))

	// 阶段 4：daemon-reload + restart
	rsStart := time.Now()
	if err := sysctl.DaemonReload(ctx); err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageRestart, false, "", "daemon-reload: "+err.Error(), rsStart))
	}
	if err := sysctl.EnableAndRestart(ctx, spec.UnitName()); err != nil {
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageRestart, false, "", err.Error(), rsStart))
	}
	steps = append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageRestart, true, spec.UnitName(), "", rsStart))

	// 阶段 5：health probe
	hStart := time.Now()
	probeURL := deploy.BuildHealthURL(host.IP, spec.Port, app.HealthCheckURL)
	res := deploy.Probe(ctx, deploy.HealthOpts{
		URL:           probeURL,
		Timeout:       3 * time.Second,
		MaxAttempts:   30,
		Interval:      2 * time.Second,
		ExpectKeyword: "UP", // actuator/health 默认 body 含 {"status":"UP"}
	})
	if !res.OK {
		detail := fmt.Sprintf("url=%s attempts=%d code=%d", probeURL, res.Attempts, res.LastCode)
		errStr := res.LastError
		if errStr == "" {
			errStr = "probe failed"
		}
		return append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageHealth, false, detail, errStr, hStart))
	}
	steps = append(steps, mkStepTimed(host.ID, host.Name, host.IP, StageHealth, true,
		fmt.Sprintf("url=%s attempts=%d", probeURL, res.Attempts), "", hStart))
	return steps
}

// effectivePort：deployment 自定义 port 优先，否则用 app.Port
func effectivePort(dep *model.Deployment, app *model.Application) int {
	if dep.Port > 0 {
		return dep.Port
	}
	return app.Port
}

func mkStep(hostID uint, hostName, hostIP, stage string, ok bool, detail, errStr string) StepResult {
	now := time.Now().Format(time.RFC3339)
	return StepResult{
		HostID: hostID, HostName: hostName, HostIP: hostIP, Stage: stage,
		OK: ok, Detail: detail, Error: errStr,
		StartedAt: now, EndedAt: now,
	}
}

func mkStepTimed(hostID uint, hostName, hostIP, stage string, ok bool, detail, errStr string, start time.Time) StepResult {
	return StepResult{
		HostID: hostID, HostName: hostName, HostIP: hostIP, Stage: stage,
		OK: ok, Detail: detail, Error: errStr,
		StartedAt: start.Format(time.RFC3339),
		EndedAt:   time.Now().Format(time.RFC3339),
	}
}

func lastStepOK(steps []StepResult) bool {
	if len(steps) == 0 {
		return false
	}
	return steps[len(steps)-1].OK
}

func (s *PipelineService) finishRun(runID uint, snap *RunSnapshot, status string) {
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
