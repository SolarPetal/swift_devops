package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/builder"
	apperr "swift-devops/internal/pkg/errors"
)

// BuildTriggerInput 触发构建的入参。
type BuildTriggerInput struct {
	GitRef          string `json:"git_ref,omitempty"`           // 空 = "main"
	MvnArgs         string `json:"mvn_args,omitempty"`          // 空 = builder 默认（clean package -DskipTests）
	CredID          uint   `json:"cred_id,omitempty"`           // 0 = 公网匿名 / 走本机 SSH 默认
	BuildModule     string `json:"build_module,omitempty"`      // 临时覆盖 app.BuildModule
	BuildJarPattern string `json:"build_jar_pattern,omitempty"` // 临时覆盖 app.BuildJarPattern
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
	ArtifactID  uint   `json:"artifact_id"` // 旧链路，X.4 删除
	BundleID    uint   `json:"bundle_id"`   // Sprint X.2：成功后回填
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
		LogPath: b.LogPath, ArtifactID: b.ArtifactID, BundleID: b.BundleID,
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
	db        *gorm.DB
	artSvc    *ArtifactService
	credSvc   *GitCredentialService
	envSvc    *BuilderEnvService
	workspace string // build_workspace 根目录
	// Sprint X.9：maven_cache_dir 不再从 config 注入；改到 BuilderEnv.MavenLocalRepo（UI 配置）
	// 触发构建时 envSvc.ResolveBuildInputs() 返回，允许留空走 settings.xml 默认。
	maxHistory    int       // Sprint 5.7：构建成功后保留的历史 Bundle 数（config.Storage.MaxHistory）；<=0 不清理
	pub           Publisher // Sprint 5.5：构建日志实时推 WS；nil = 只写文件不推
	dockerEnabled bool      // Sprint 5.6：config.builder.docker_enabled；true 走容器构建

	mu       sync.Mutex
	busyApps map[uint]struct{} // 同 app 互斥（构建 + 部署可分开管，但构建本身互斥）
}

func NewBuildService(db *gorm.DB, artSvc *ArtifactService, credSvc *GitCredentialService,
	envSvc *BuilderEnvService, workspace string, maxHistory int, dockerEnabled bool) *BuildService {
	return &BuildService{
		db: db, artSvc: artSvc, credSvc: credSvc, envSvc: envSvc,
		workspace: workspace, maxHistory: maxHistory, dockerEnabled: dockerEnabled,
		busyApps: map[uint]struct{}{},
	}
}

// SetPublisher 注入 WS 推送器（Sprint 5.5）。router 装配时调一次。
func (s *BuildService) SetPublisher(p Publisher) { s.pub = p }

// Trigger 异步触发一次构建。落库 building 状态后立即返回。
func (s *BuildService) Trigger(appID uint, actor string, in BuildTriggerInput) (BuildRunView, error) {
	if s.workspace == "" {
		return BuildRunView{}, apperr.New("INTERNAL", "storage.build_workspace 未配置", 500)
	}

	// 构建环境校验：docker 模式查 docker/image/git；本机模式查 java/maven/git 检测通过（Sprint 5.6）
	if s.dockerEnabled {
		if _, err := s.envSvc.ResolveDockerInputs(); err != nil {
			return BuildRunView{}, err
		}
	} else {
		if err := s.envSvc.RequireValid(); err != nil {
			return BuildRunView{}, err
		}
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
	ref := strings.TrimSpace(in.GitRef)
	if ref == "" {
		ref = defaultRef(app.GitRef)
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
	// 关键：log 放在 subDir 外（跟 subDir 同级），避免 builder.Build 的
	// "subDir 非空 → RemoveAll 重建" 把 log 文件一起删掉（unlinked fd 还能写，
	// 但 disk 上文件没了，GetLog 读不到 → 前端看到空日志）
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		s.releaseLock(appID)
		s.finishBuild(br.ID, BuildStatusFailed, "", 0, 0, "mkdir workspace: "+err.Error())
		return BuildRunView{}, apperr.Wrap(err, "INTERNAL", "mkdir workspace", 500)
	}
	logPath := filepath.Join(s.workspace, fmt.Sprintf("%s-%d.log", app.AppCode, br.ID))
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
		s.finishBuild(buildID, BuildStatusFailed, "", 0, 0, "open log file: "+err.Error())
		return
	}
	defer logFile.Close()

	// Sprint 5.5：构建日志双写——文件 + WS。元信息仍只写文件（瞬间完成，靠 snapshot 补全），
	// 只有耗时的 git clone + mvn 阶段（builder.Build）走双写，前端实时滚动。
	var logWriter io.Writer = logFile
	if s.pub != nil {
		logWriter = io.MultiWriter(logFile, &hubLogWriter{pub: s.pub, buildID: buildID})
	}

	fmt.Fprintf(logWriter, "=== swift-devops build run #%d ===\n", buildID)
	fmt.Fprintf(logWriter, "app: %s (%d)  git_url: %s  ref: %s  cred_id: %d  mvn_args: %q  build_mode: %s\n",
		app.AppCode, app.ID, app.GitURL, defaultRef(in.GitRef), in.CredID, in.MvnArgs, normalizeBuildMode(app.BuildMode))
	fmt.Fprintf(logWriter, "started at: %s\n\n", time.Now().Format(time.RFC3339))

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// Sprint 5.6：按 docker_enabled 决定容器构建还是本机构建，注入不同的 builder 输入
	var (
		mvnBin, gitBin, mavenCache, dockerImage string
		execEnv                                 []string
	)
	if s.dockerEnabled {
		di, derr := s.envSvc.ResolveDockerInputs()
		if derr != nil {
			fmt.Fprintf(logFile, "\n[BUILD FAILED] docker inputs: %v\n", derr)
			s.finishBuild(buildID, BuildStatusFailed, "", 0, 0, derr.Error())
			return
		}
		gitBin, dockerImage, mavenCache = di.GitBin, di.DockerImage, di.MavenCacheDir
		cacheDisplay := mavenCache
		if cacheDisplay == "" {
			cacheDisplay = "(空 → 容器内每次重下依赖)"
		}
		fmt.Fprintf(logWriter, "[docker] image=%s git=%s maven_cache=%s\n", dockerImage, gitBin, cacheDisplay)
	} else {
		ee, envErr := s.envSvc.BuildExecEnv()
		if envErr != nil {
			fmt.Fprintf(logFile, "[WARN] 读取构建环境失败：%v；fallback 到 os.Environ()\n", envErr)
		}
		execEnv = ee
		inputs, binsErr := s.envSvc.ResolveBuildInputs()
		if binsErr != nil {
			fmt.Fprintf(logFile, "\n[BUILD FAILED] load builder inputs: %v\n", binsErr)
			s.finishBuild(buildID, BuildStatusFailed, "", 0, 0, binsErr.Error())
			return
		}
		mvnBin, gitBin, mavenCache = inputs.MvnBin, inputs.GitBin, inputs.MavenLocalRepo
		cacheDisplay := mavenCache
		if cacheDisplay == "" {
			cacheDisplay = "(空 → 用 settings.xml 默认)"
		}
		fmt.Fprintf(logWriter, "[bins] mvn=%s git=%s maven_local_repo=%s\n", mvnBin, gitBin, cacheDisplay)
	}

	// Sprint X.2：决定走 multi-service 还是单 service 兼容
	specs, err := s.loadServiceBuildSpecs(app, in)
	if err != nil {
		fmt.Fprintf(logFile, "\n[BUILD FAILED] load service specs: %v\n", err)
		s.finishBuild(buildID, BuildStatusFailed, "", 0, 0, err.Error())
		return
	}
	for _, sp := range specs {
		fmt.Fprintf(logFile, "[plan] service=%s build_module=%q jar_pattern=%q\n",
			sp.ServiceCode, sp.BuildModule, sp.JarPattern)
	}

	plan := builder.Plan{
		AppCode: app.AppCode, GitURL: app.GitURL, GitRef: defaultRef(in.GitRef),
		Cred: cred, MvnArgs: in.MvnArgs,
		MvnBin:    mvnBin,
		GitBin:    gitBin,
		Workspace: s.workspace, MavenCacheDir: mavenCache,
		LogWriter:       logWriter,
		ExecEnv:         execEnv,
		BuildID:         buildID,
		Services:        specs,
		DockerImage:     dockerImage,
		BuildMode:       normalizeBuildMode(app.BuildMode),
		DockerRegistry:  strings.TrimSpace(app.DockerRegistry),
		DockerImageName: strings.TrimSpace(app.DockerImageName),
		DockerImageTag:  strings.TrimSpace(app.DockerImageTag),
		Dockerfile:      app.Dockerfile,
		DockerBuildArgs: strings.TrimSpace(app.DockerBuildArgs),
	}
	res, err := builder.Build(ctx, plan)
	if err != nil {
		fmt.Fprintf(logFile, "\n[BUILD FAILED] %v\n", err)
		s.finishBuild(buildID, BuildStatusFailed, res.CommitSHA, 0, 0, err.Error())
		return
	}

	// Sprint X.2：落 Bundle + N 条 Item
	versionTag := fmt.Sprintf("git-%s-%d", shortSHA(res.CommitSHA), buildID)
	fmt.Fprintf(logFile, "\n[ARTIFACT] registering bundle version_tag=%s with %d items ...\n",
		versionTag, len(res.JarPaths))

	bundleItems := make([]BundleItemInput, 0, len(specs))
	for _, sp := range specs {
		jar := res.JarPaths[sp.ServiceCode]
		if jar == "" {
			err := fmt.Errorf("service %s 未找到 jar 产物", sp.ServiceCode)
			fmt.Fprintf(logFile, "[ARTIFACT FAILED] %v\n", err)
			s.finishBuild(buildID, BuildStatusFailed, res.CommitSHA, 0, 0, err.Error())
			return
		}
		bundleItems = append(bundleItems, BundleItemInput{
			ServiceCode:       sp.ServiceCode,
			LocalPath:         jar,
			DockerImage:       res.DockerImages[sp.ServiceCode],
			DockerfileName:    res.DockerfileSnapshots[sp.ServiceCode].Name,
			DockerfileContent: res.DockerfileSnapshots[sp.ServiceCode].Content,
		})
	}

	bundleView, err := s.artSvc.IngestBundle(app.ID, versionTag, res.CommitSHA, "", logPath, bundleItems)
	if err != nil {
		fmt.Fprintf(logFile, "[ARTIFACT FAILED] %v\n", err)
		s.finishBuild(buildID, BuildStatusFailed, res.CommitSHA, 0, 0,
			"build success but bundle register failed: "+err.Error())
		return
	}

	// Sprint X.2 兼容：单 service "default" 模式额外落一条 Artifact，
	// 给老部署链（pipeline_service 走 artifact_id）兜底，X.3 切到 Bundle 后删除。
	var legacyArtifactID uint
	if len(specs) == 1 && specs[0].ServiceCode == "default" {
		if art, lerr := s.artSvc.IngestLocalJar(app.ID, versionTag+"-legacy", res.JarPath); lerr != nil {
			fmt.Fprintf(logFile, "[WARN] legacy artifact ingest failed (X.3 之前部署链会用不到): %v\n", lerr)
		} else {
			legacyArtifactID = art.ID
			fmt.Fprintf(logFile, "[LEGACY] artifact=#%d 兼容旧部署链\n", art.ID)
		}
	}

	fmt.Fprintf(logWriter, "[BUILD SUCCESS] bundle=#%d items=%d\n", bundleView.ID, len(bundleView.Items))

	// Sprint 5.7：构建成功后滚动清理历史 Bundle（保留最近 maxHistory 个）。
	// 清理失败不影响构建结果，仅记日志 + 写进 build log 给用户看。
	if s.maxHistory > 0 {
		if cr, cerr := s.artSvc.CleanupBundleHistory(app.ID, s.maxHistory); cerr != nil {
			slog.Warn("post-build cleanup history", "app", app.ID, "err", cerr)
		} else if len(cr.DeletedBundles) > 0 {
			fmt.Fprintf(logWriter, "[CLEANUP] 保留最近 %d 个 Bundle，清理 %d 个历史版本，释放 %d KB\n",
				cr.Keep, len(cr.DeletedBundles), cr.FreedBytes>>10)
		}
	}

	// 所有尾部日志写完后再推终态，避免前端收到 status 就 close 导致尾部日志丢失。
	s.finishBuild(buildID, BuildStatusSuccess, res.CommitSHA, legacyArtifactID, bundleView.ID, "")
}

// loadServiceBuildSpecs 决定构建模式：
//   - app 已有 AppService 行 → 多 service 模式（每行一个 spec）
//   - 没有 AppService → 单 service 兼容，service_code="default"，从 app 顶层字段取
//
// X.2 阶段：app_services 表为空（X.4 才会有 CRUD）；Trigger 入参的临时覆盖仍尊重。
func (s *BuildService) loadServiceBuildSpecs(app *model.Application, in BuildTriggerInput) ([]builder.ServiceBuildSpec, error) {
	var services []model.AppService
	if err := s.db.Where("app_id = ? AND enabled = ?", app.ID, true).
		Order("startup_order ASC, id ASC").Find(&services).Error; err != nil {
		return nil, fmt.Errorf("load app_services: %w", err)
	}
	if len(services) > 0 {
		specs := make([]builder.ServiceBuildSpec, 0, len(services))
		for _, svc := range services {
			dfName, dfContent := s.resolveDockerfileTemplate(app, &svc)
			specs = append(specs, builder.ServiceBuildSpec{
				ServiceCode:       svc.ServiceCode,
				BuildModule:       svc.BuildModule,
				JarPattern:        svc.BuildJarPattern,
				Port:              svc.Port,
				DockerRegistry:    pickDockerStr(svc.DockerRegistry, app.DockerRegistry),
				DockerImageName:   pickDockerStr(svc.DockerImageName, app.DockerImageName),
				DockerImageTag:    pickDockerStr(svc.DockerImageTag, app.DockerImageTag),
				DockerfileName:    dfName,
				DockerfileContent: dfContent,
				DockerBuildArgs:   pickDockerStr(svc.DockerBuildArgs, app.DockerBuildArgs),
			})
		}
		return specs, nil
	}

	// 单 service 兼容：service_code=default，从 app 顶层字段读
	module := pickStr(in.BuildModule, app.BuildModule)
	pattern := pickStr(in.BuildJarPattern, app.BuildJarPattern)
	dfName, dfContent := s.resolveDockerfileTemplate(app, nil)
	return []builder.ServiceBuildSpec{{
		ServiceCode:       "default",
		BuildModule:       module,
		JarPattern:        pattern,
		Port:              app.Port,
		DockerRegistry:    strings.TrimSpace(app.DockerRegistry),
		DockerImageName:   strings.TrimSpace(app.DockerImageName),
		DockerImageTag:    strings.TrimSpace(app.DockerImageTag),
		DockerfileName:    dfName,
		DockerfileContent: dfContent,
		DockerBuildArgs:   strings.TrimSpace(app.DockerBuildArgs),
	}}, nil
}

func (s *BuildService) resolveDockerfileTemplate(app *model.Application, svc *model.AppService) (string, string) {
	templateID := uint(0)
	legacyDockerfile := strings.TrimSpace(app.Dockerfile)
	if svc != nil {
		templateID = svc.DockerfileTemplateID
		if strings.TrimSpace(svc.Dockerfile) != "" {
			legacyDockerfile = svc.Dockerfile
		}
	}
	var tpl model.DockerfileTemplate
	if templateID > 0 {
		if err := s.db.Where("id = ? AND app_id = ?", templateID, app.ID).First(&tpl).Error; err == nil {
			return tpl.Name, tpl.Content
		}
	}
	if err := s.db.Where("app_id = ? AND is_default = ?", app.ID, true).Order("id ASC").First(&tpl).Error; err == nil {
		return tpl.Name, tpl.Content
	}
	if legacyDockerfile != "" {
		return "legacy-inline", legacyDockerfile
	}
	return "", ""
}

func pickDockerStr(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

func (s *BuildService) releaseLock(appID uint) {
	s.mu.Lock()
	delete(s.busyApps, appID)
	s.mu.Unlock()
}

func (s *BuildService) finishBuild(buildID uint, status, sha string, artifactID, bundleID uint, errMsg string) {
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
	if bundleID > 0 {
		updates["bundle_id"] = bundleID
	}
	if err := s.db.Model(&model.BuildRun{}).Where("id = ?", buildID).Updates(updates).Error; err != nil {
		slog.Error("finish build", "id", buildID, "err", err)
	}
	// Sprint 5.5：推一帧终态，前端 WS 据此停订阅 + 刷新（成功/失败/取消都覆盖）
	s.publishBuildStatus(buildID, status)
}

func defaultRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "main"
	}
	return ref
}

// pickStr 返回第一个非空（trim 后）字符串，否则返回 fallback
func pickStr(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
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

// ListRemoteBranches 获取远程仓库的分支列表（Sprint X.11）
func (s *BuildService) ListRemoteBranches(ctx context.Context, appID uint, credID uint) ([]string, error) {
	// 1. 查询应用
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("query app: %w", err)
	}

	if strings.TrimSpace(app.GitURL) == "" {
		return nil, fmt.Errorf("应用未配置 Git 仓库地址")
	}

	// 2. 查询凭证（如果指定）
	var cred *builder.Credential
	if credID > 0 {
		var gitCred model.GitCredential
		if err := s.db.First(&gitCred, credID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("凭证不存在")
			}
			return nil, fmt.Errorf("query cred: %w", err)
		}

		secret, err := unwrapSecretBlob(gitCred.Secret)
		if err != nil {
			return nil, fmt.Errorf("unwrap secret: %w", err)
		}

		cred = &builder.Credential{
			Type:     gitCred.Type,
			Username: gitCred.Username,
			Secret:   secret,
		}
	}

	// 3. 解析 git 二进制路径 + 执行环境
	//    git_path 默认留空（注释「空 = 走 PATH 找」），这里跟 ResolveBuildInputs 对齐：
	//    空则兜底 /usr/bin/git。获取分支只需要 git，故不强制整个构建环境 valid（不查 mvn/java）。
	env, err := s.envSvc.Load()
	if err != nil {
		return nil, fmt.Errorf("get builder env: %w", err)
	}
	gitBin := strings.TrimSpace(env.GitPath)
	if gitBin == "" {
		gitBin = "/usr/bin/git"
	}
	if !fileExecutable(gitBin) {
		return nil, apperr.New("BAD_REQUEST",
			fmt.Sprintf("git 不可执行：%s；请去「⚙ 构建环境」填 git 绝对路径", gitBin), 400)
	}
	// 执行环境：构建环境 valid 时注入 PATH/JAVA_HOME 等；否则为 nil，
	// 由 builder.ListRemoteBranches fallback 到 os.Environ()（含 HOME/PATH/SSH_AUTH_SOCK）。
	execEnv, _ := s.envSvc.BuildExecEnv()

	branches, err := builder.ListRemoteBranches(ctx, builder.ListBranchesOptions{
		GitURL:  app.GitURL,
		GitBin:  gitBin,
		Cred:    cred,
		ExecEnv: execEnv,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		if msg := branchListUserMessage(err); msg != "" {
			return nil, apperr.New("BAD_REQUEST", msg, http.StatusBadRequest)
		}
		return nil, fmt.Errorf("获取分支列表失败: %w", err)
	}

	return branches, nil
}

func branchListUserMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "authentication failed"),
		strings.Contains(msg, "incorrect username or password"),
		strings.Contains(msg, "could not read username"),
		strings.Contains(msg, "permission denied"):
		return "获取分支失败：Git 凭证认证失败，请检查用户名 / Token 是否正确，且 Token 有仓库读取权限"
	case strings.Contains(msg, "repository not found"),
		strings.Contains(msg, "not found"):
		return "获取分支失败：仓库不存在或当前凭证无权访问"
	}
	return ""
}
