package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/builder"
	apperr "swift-devops/internal/pkg/errors"
)

// BuildTriggerInput 触发构建的入参。
type BuildTriggerInput struct {
	GitRef  string `json:"git_ref,omitempty"`  // 空 = "main"
	MvnArgs string `json:"mvn_args,omitempty"` // 空 = builder 默认（clean package -DskipTests）
	CredID  uint   `json:"cred_id,omitempty"`  // 0 = 公网匿名 / 走本机 SSH 默认
}

// BuildRunView 构建任务响应视图。
type BuildRunView struct {
	ID          uint   `json:"id"`
	AppID       uint   `json:"app_id"`
	GitRef      string `json:"git_ref"`
	CommitSHA   string `json:"commit_sha"`
	MvnArgs     string `json:"mvn_args"`
	CredID      uint   `json:"cred_id"`
	Status      string `json:"status"`
	LogPath     string `json:"log_path"`
	ArtifactID  uint   `json:"artifact_id"`
	TriggeredBy string `json:"triggered_by"`
	Error       string `json:"error"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// Build 状态常量
const (
	BuildStatusBuilding  = "building"
	BuildStatusSuccess   = "success"
	BuildStatusFailed    = "failed"
	BuildStatusCancelled = "cancelled"
)

func toBuildRunView(b *model.BuildRun) BuildRunView {
	v := BuildRunView{
		ID: b.ID, AppID: b.AppID, GitRef: b.GitRef, CommitSHA: b.CommitSHA,
		MvnArgs: b.MvnArgs, CredID: b.CredID, Status: b.Status,
		LogPath: b.LogPath, ArtifactID: b.ArtifactID,
		TriggeredBy: b.TriggeredBy, Error: b.Error,
		CreatedAt: b.CreatedAt.Format(time.RFC3339),
	}
	if b.StartedAt != nil {
		v.StartedAt = b.StartedAt.Format(time.RFC3339)
	}
	if b.FinishedAt != nil {
		v.FinishedAt = b.FinishedAt.Format(time.RFC3339)
	}
	return v
}

// BuildService 构建编排：触发 / 异步执行 / 查询 / 读日志。
type BuildService struct {
	db            *gorm.DB
	artSvc        *ArtifactService
	credSvc       *GitCredentialService
	envSvc        *BuilderEnvService
	workspace     string // build_workspace 根目录
	mavenCacheDir string

	mu       sync.Mutex
	busyApps map[uint]struct{} // 同 app 互斥（构建 + 部署可分开管，但构建本身互斥）
}

func NewBuildService(db *gorm.DB, artSvc *ArtifactService, credSvc *GitCredentialService,
	envSvc *BuilderEnvService, workspace, mavenCacheDir string) *BuildService {
	return &BuildService{
		db: db, artSvc: artSvc, credSvc: credSvc, envSvc: envSvc,
		workspace:     workspace,
		mavenCacheDir: mavenCacheDir,
		busyApps:      map[uint]struct{}{},
	}
}

// Trigger 异步触发一次构建。落库 building 状态后立即返回。
func (s *BuildService) Trigger(appID uint, actor string, in BuildTriggerInput) (BuildRunView, error) {
	if s.workspace == "" {
		return BuildRunView{}, apperr.New("INTERNAL", "storage.build_workspace 未配置", 500)
	}

	// Sprint 5.4：构建环境必须先在「⚙ 构建环境」配好且检测通过
	if err := s.envSvc.RequireValid(); err != nil {
		return BuildRunView{}, err
	}

	// 1. 校验 app + git_url
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BuildRunView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return BuildRunView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	if app.GitURL == "" {
		return BuildRunView{}, apperr.New("BAD_REQUEST",
			"应用未配置 git_url，无法从远端构建。请在「应用管理」编辑 git_url", 400)
	}

	// 2. 校验凭证（如指定）
	var cred *builder.Credential
	if in.CredID > 0 {
		typ, user, secret, err := s.credSvc.LoadSecret(in.CredID)
		if err != nil {
			return BuildRunView{}, err
		}
		// LoadSecret 返回的是 JSON 包装后的明文 {"v": "..."}；这里再解一层
		plain, perr := unwrapSecretBlob(secret)
		if perr != nil {
			return BuildRunView{}, apperr.Wrap(perr, "INTERNAL", "unwrap secret", 500)
		}
		cred = &builder.Credential{Type: typ, Username: user, Secret: plain}
	}

	// 3. 同 app 互斥
	s.mu.Lock()
	if _, busy := s.busyApps[appID]; busy {
		s.mu.Unlock()
		return BuildRunView{}, apperr.New("CONFLICT",
			fmt.Sprintf("应用 %d 已有构建在跑，请等待结束", appID), 409)
	}
	s.busyApps[appID] = struct{}{}
	s.mu.Unlock()

	// 4. 落库 building
	ref := in.GitRef
	if ref == "" {
		ref = "main"
	}
	now := time.Now()
	br := &model.BuildRun{
		AppID: appID, GitRef: ref, MvnArgs: in.MvnArgs, CredID: in.CredID,
		Status: BuildStatusBuilding, TriggeredBy: actor, StartedAt: &now,
	}
	if err := s.db.Create(br).Error; err != nil {
		s.releaseLock(appID)
		return BuildRunView{}, apperr.Wrap(err, "INTERNAL", "create build run", 500)
	}

	// 5. 准备 log 文件路径
	logPath := filepath.Join(s.workspace, fmt.Sprintf("%s-%d", app.AppCode, br.ID), "build.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		s.releaseLock(appID)
		s.finishBuild(br.ID, BuildStatusFailed, "", 0, "mkdir log dir: "+err.Error())
		return BuildRunView{}, apperr.Wrap(err, "INTERNAL", "mkdir log dir", 500)
	}
	br.LogPath = logPath
	if err := s.db.Model(&model.BuildRun{}).Where("id = ?", br.ID).
		Update("log_path", logPath).Error; err != nil {
		slog.Warn("update build log_path", "id", br.ID, "err", err)
	}

	// 6. 异步执行
	go s.execute(br.ID, &app, in, cred, logPath)

	return toBuildRunView(br), nil
}

// List / Get / GetLog
func (s *BuildService) List(appID uint) ([]BuildRunView, error) {
	var rs []model.BuildRun
	q := s.db.Order("id DESC").Limit(200)
	if appID > 0 {
		q = q.Where("app_id = ?", appID)
	}
	if err := q.Find(&rs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list build runs", 500)
	}
	vs := make([]BuildRunView, len(rs))
	for i := range rs {
		vs[i] = toBuildRunView(&rs[i])
	}
	return vs, nil
}

func (s *BuildService) Get(id uint) (BuildRunView, error) {
	var r model.BuildRun
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BuildRunView{}, apperr.ErrNotFound
		}
		return BuildRunView{}, apperr.Wrap(err, "INTERNAL", "get build run", 500)
	}
	return toBuildRunView(&r), nil
}

// GetLog 读 build log 全文。
//   - 已结束的 run → 一次性读全文
//   - building 中 → 读已有部分（用户可轮询）
func (s *BuildService) GetLog(id uint) (string, error) {
	v, err := s.Get(id)
	if err != nil {
		return "", err
	}
	if v.LogPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(v.LogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", apperr.Wrap(err, "INTERNAL", "read build log", 500)
	}
	return string(data), nil
}

// --- 异步 ---

func (s *BuildService) execute(buildID uint, app *model.Application, in BuildTriggerInput, cred *builder.Credential, logPath string) {
	defer s.releaseLock(app.ID)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		s.finishBuild(buildID, BuildStatusFailed, "", 0, "open log file: "+err.Error())
		return
	}
	defer logFile.Close()

	fmt.Fprintf(logFile, "=== swift-devops build run #%d ===\n", buildID)
	fmt.Fprintf(logFile, "app: %s (%d)  git_url: %s  ref: %s  cred_id: %d  mvn_args: %q\n",
		app.AppCode, app.ID, app.GitURL, defaultRef(in.GitRef), in.CredID, in.MvnArgs)
	fmt.Fprintf(logFile, "started at: %s\n\n", time.Now().Format(time.RFC3339))

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// 注入构建机环境变量（JAVA_HOME / MAVEN_HOME / PATH 前置 java/mvn/git bin）
	execEnv, envErr := s.envSvc.BuildExecEnv()
	if envErr != nil {
		fmt.Fprintf(logFile, "[WARN] 读取构建环境失败：%v；fallback 到 os.Environ()\n", envErr)
	}

	plan := builder.Plan{
		AppCode: app.AppCode, GitURL: app.GitURL, GitRef: defaultRef(in.GitRef),
		Cred: cred, MvnArgs: in.MvnArgs,
		Workspace: s.workspace, MavenCacheDir: s.mavenCacheDir,
		LogWriter: io.MultiWriter(logFile),
		ExecEnv:   execEnv,
		BuildID:   buildID,
	}
	res, err := builder.Build(ctx, plan)
	if err != nil {
		fmt.Fprintf(logFile, "\n[BUILD FAILED] %v\n", err)
		s.finishBuild(buildID, BuildStatusFailed, res.CommitSHA, 0, err.Error())
		return
	}

	// 落 Artifact：jar 拷到 artifactDir，version_tag = "git-<shortSHA>"
	versionTag := fmt.Sprintf("git-%s-%d", shortSHA(res.CommitSHA), buildID)
	fmt.Fprintf(logFile, "\n[ARTIFACT] registering %s as version_tag=%s ...\n",
		res.JarPath, versionTag)

	artView, err := s.artSvc.IngestLocalJar(app.ID, versionTag, res.JarPath)
	if err != nil {
		fmt.Fprintf(logFile, "[ARTIFACT FAILED] %v\n", err)
		s.finishBuild(buildID, BuildStatusFailed, res.CommitSHA, 0,
			"build success but artifact register failed: "+err.Error())
		return
	}

	fmt.Fprintf(logFile, "[BUILD SUCCESS] artifact=#%d (%s)\n", artView.ID, artView.FileName)
	s.finishBuild(buildID, BuildStatusSuccess, res.CommitSHA, artView.ID, "")
}

func (s *BuildService) releaseLock(appID uint) {
	s.mu.Lock()
	delete(s.busyApps, appID)
	s.mu.Unlock()
}

func (s *BuildService) finishBuild(buildID uint, status, sha string, artifactID uint, errMsg string) {
	now := time.Now()
	updates := map[string]any{
		"status":      status,
		"finished_at": &now,
		"error":       errMsg,
	}
	if sha != "" {
		updates["commit_sha"] = sha
	}
	if artifactID > 0 {
		updates["artifact_id"] = artifactID
	}
	if err := s.db.Model(&model.BuildRun{}).Where("id = ?", buildID).Updates(updates).Error; err != nil {
		slog.Error("finish build", "id", buildID, "err", err)
	}
}

func defaultRef(ref string) string {
	if ref == "" {
		return "main"
	}
	return ref
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// unwrapSecretBlob 把 GitCredentialService.encryptSecret 包装的 JSON 拆开拿明文 v 字段
func unwrapSecretBlob(s string) (string, error) {
	type sb struct {
		V string `json:"v"`
	}
	var b sb
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		// 兼容：万一未来直接存明文也能跑
		return s, nil
	}
	return b.V, nil
}
