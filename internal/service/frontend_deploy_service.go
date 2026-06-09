package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/builder"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
	"swift-devops/internal/service/pipeline/strategy"
)

const (
	defaultFrontendServiceCode    = "web"
	defaultFrontendPackageManager = "auto"
	defaultFrontendBuildCommand   = "npm run build"
	defaultFrontendDistDir        = "dist"
	defaultFrontendNodeImage      = "node:20-alpine"
	defaultFrontendNginxImage     = "nginx:1.27-alpine"
)

const (
	frontendPipelineStrategy         = "frontend"
	frontendRollbackPipelineStrategy = "frontend_rollback"

	FrontendStagePrepare      = "frontend_prepare"
	FrontendStageGitClone     = "git_clone"
	FrontendStagePackage      = "frontend_package"
	FrontendStageDockerBuild  = "docker_build"
	FrontendStageImageCheck   = "docker_image_check"
	FrontendStageDockerRun    = "docker_run"
	FrontendStageGatewayRoute = "gateway_route"
	FrontendStageGatewayApply = "gateway_apply"
)

// FrontendAppConfigInput 前端项目部署配置入参。
type FrontendAppConfigInput struct {
	ServiceCode     string `json:"service_code,omitempty"`
	PackageManager  string `json:"package_manager,omitempty"`
	InstallCommand  string `json:"install_command,omitempty"`
	BuildCommand    string `json:"build_command,omitempty"`
	DistDir         string `json:"dist_dir,omitempty"`
	NodeImage       string `json:"node_image,omitempty"`
	NginxImage      string `json:"nginx_image,omitempty"`
	SPAFallback     *bool  `json:"spa_fallback,omitempty"`
	ContainerName   string `json:"container_name,omitempty"`
	TargetPort      int    `json:"target_port,omitempty"`
	Dockerfile      string `json:"dockerfile,omitempty"`
	NginxConfig     string `json:"nginx_config,omitempty"`
	DockerBuildArgs string `json:"docker_build_args,omitempty"`
	DockerRunArgs   string `json:"docker_run_args,omitempty"`
}

// FrontendDeployInput 触发一次前端部署。
type FrontendDeployInput struct {
	HostID               uint   `json:"host_id" binding:"required"`
	ServiceCode          string `json:"service_code,omitempty"`
	GitRef               string `json:"git_ref,omitempty"`
	CredID               uint   `json:"cred_id,omitempty"`
	Domain               string `json:"domain,omitempty"`
	HTTPS                *bool  `json:"https,omitempty"`
	CertPath             string `json:"cert_path,omitempty"`
	KeyPath              string `json:"key_path,omitempty"`
	ApplyGateway         bool   `json:"apply_gateway,omitempty"`
	ForceRecreateGateway bool   `json:"force_recreate_gateway,omitempty"`
}

// FrontendRollbackInput 触发一次前端部署回滚。
type FrontendRollbackInput struct {
	HostID               uint   `json:"host_id" binding:"required"`
	ServiceCode          string `json:"service_code,omitempty"`
	Domain               string `json:"domain,omitempty"`
	ApplyGateway         bool   `json:"apply_gateway,omitempty"`
	ForceRecreateGateway bool   `json:"force_recreate_gateway,omitempty"`
}

// FrontendDeploymentStateFilter 查询前端部署版本账本的过滤条件。
type FrontendDeploymentStateFilter struct {
	HostID      uint
	ServiceCode string
	Domain      string
}

// FrontendAppConfigView 前端配置响应。
type FrontendAppConfigView struct {
	ID              uint   `json:"id"`
	AppID           uint   `json:"app_id"`
	ServiceCode     string `json:"service_code"`
	PackageManager  string `json:"package_manager"`
	InstallCommand  string `json:"install_command"`
	BuildCommand    string `json:"build_command"`
	DistDir         string `json:"dist_dir"`
	NodeImage       string `json:"node_image"`
	NginxImage      string `json:"nginx_image"`
	SPAFallback     bool   `json:"spa_fallback"`
	ContainerName   string `json:"container_name"`
	TargetPort      int    `json:"target_port"`
	Dockerfile      string `json:"dockerfile"`
	NginxConfig     string `json:"nginx_config"`
	DockerBuildArgs string `json:"docker_build_args"`
	DockerRunArgs   string `json:"docker_run_args"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// FrontendDeployView 前端部署结果。
type FrontendDeployView struct {
	AppID          uint                      `json:"app_id"`
	AppCode        string                    `json:"app_code"`
	ServiceCode    string                    `json:"service_code"`
	HostID         uint                      `json:"host_id"`
	PipelineRunID  uint                      `json:"pipeline_run_id"`
	PipelineStatus string                    `json:"pipeline_status"`
	Domain         string                    `json:"domain"`
	ContainerName  string                    `json:"container_name"`
	Image          string                    `json:"image"`
	CommitSHA      string                    `json:"commit_sha"`
	RemoteWorkDir  string                    `json:"remote_work_dir"`
	GatewayRoute   FrontendGatewayRouteView  `json:"gateway_route"`
	GatewayApply   *FrontendGatewayApplyView `json:"gateway_apply,omitempty"`
	DeployedAt     string                    `json:"deployed_at"`
}

// FrontendDeploymentStateView 展示某个域名前端服务的 current/previous 版本账本。
type FrontendDeploymentStateView struct {
	ID            uint   `json:"id"`
	AppID         uint   `json:"app_id"`
	AppCode       string `json:"app_code"`
	AppName       string `json:"app_name"`
	HostID        uint   `json:"host_id"`
	HostName      string `json:"host_name"`
	HostIP        string `json:"host_ip"`
	ServiceCode   string `json:"service_code"`
	Domain        string `json:"domain"`
	ContainerName string `json:"container_name"`
	TargetPort    int    `json:"target_port"`

	CurrentImage         string `json:"current_image"`
	CurrentCommitSHA     string `json:"current_commit_sha"`
	CurrentRemoteWorkDir string `json:"current_remote_work_dir"`
	CurrentPipelineRunID uint   `json:"current_pipeline_run_id"`
	CurrentDeployedAt    string `json:"current_deployed_at"`

	PreviousImage         string `json:"previous_image"`
	PreviousCommitSHA     string `json:"previous_commit_sha"`
	PreviousRemoteWorkDir string `json:"previous_remote_work_dir"`
	PreviousPipelineRunID uint   `json:"previous_pipeline_run_id"`
	PreviousDeployedAt    string `json:"previous_deployed_at"`

	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// FrontendTemplatePreviewView 前端 Dockerfile/nginx.conf 预览。
type FrontendTemplatePreviewView struct {
	Dockerfile  string `json:"dockerfile"`
	NginxConfig string `json:"nginx_config"`
}

// FrontendDeployService 执行前端项目源码到 Docker 容器的部署。
type FrontendDeployService struct {
	db          *gorm.DB
	hostSvc     *HostService
	credSvc     *GitCredentialService
	envSvc      *BuilderEnvService
	gatewaySvc  *FrontendGatewayService
	workspace   string
	sshOpts     sshpkg.DialOptions
	pub         Publisher
	pipelineSvc *PipelineService
}

func NewFrontendDeployService(db *gorm.DB, hostSvc *HostService, credSvc *GitCredentialService, envSvc *BuilderEnvService, gatewaySvc *FrontendGatewayService, workspace string, sshTimeout time.Duration) *FrontendDeployService {
	return &FrontendDeployService{db: db, hostSvc: hostSvc, credSvc: credSvc, envSvc: envSvc, gatewaySvc: gatewaySvc, workspace: workspace, sshOpts: sshpkg.DialOptions{Timeout: sshTimeout}}
}

func (s *FrontendDeployService) SetPublisher(p Publisher) { s.pub = p }

func (s *FrontendDeployService) SetPipelineCoordinator(p *PipelineService) {
	s.pipelineSvc = p
}

func toFrontendAppConfigView(c *model.FrontendAppConfig) FrontendAppConfigView {
	return FrontendAppConfigView{
		ID: c.ID, AppID: c.AppID, ServiceCode: c.ServiceCode,
		PackageManager: c.PackageManager, InstallCommand: c.InstallCommand, BuildCommand: c.BuildCommand,
		DistDir: c.DistDir, NodeImage: c.NodeImage, NginxImage: c.NginxImage, SPAFallback: c.SPAFallback,
		ContainerName: c.ContainerName, TargetPort: c.TargetPort, Dockerfile: c.Dockerfile, NginxConfig: c.NginxConfig,
		DockerBuildArgs: c.DockerBuildArgs, DockerRunArgs: c.DockerRunArgs,
		CreatedAt: c.CreatedAt.Format(time.RFC3339), UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
	}
}

func toFrontendDeploymentStateView(row *model.FrontendDeploymentState, app *model.Application, host *model.Host) FrontendDeploymentStateView {
	view := FrontendDeploymentStateView{
		ID:            row.ID,
		AppID:         row.AppID,
		HostID:        row.HostID,
		ServiceCode:   row.ServiceCode,
		Domain:        row.Domain,
		ContainerName: row.ContainerName,
		TargetPort:    row.TargetPort,

		CurrentImage:         row.CurrentImage,
		CurrentCommitSHA:     row.CurrentCommitSHA,
		CurrentRemoteWorkDir: row.CurrentRemoteWorkDir,
		CurrentPipelineRunID: row.CurrentPipelineRunID,
		CurrentDeployedAt:    frontendTimeString(row.CurrentDeployedAt),

		PreviousImage:         row.PreviousImage,
		PreviousCommitSHA:     row.PreviousCommitSHA,
		PreviousRemoteWorkDir: row.PreviousRemoteWorkDir,
		PreviousPipelineRunID: row.PreviousPipelineRunID,
		PreviousDeployedAt:    frontendTimeString(row.PreviousDeployedAt),

		Status:    row.Status,
		CreatedAt: frontendTimeString(row.CreatedAt),
		UpdatedAt: frontendTimeString(row.UpdatedAt),
	}
	if app != nil {
		view.AppCode = app.AppCode
		view.AppName = app.Name
	}
	if host != nil {
		view.HostName = host.Name
		view.HostIP = host.IP
	}
	return view
}

func frontendTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (s *FrontendDeployService) GetConfig(appID uint, serviceCode string) (FrontendAppConfigView, error) {
	app, err := s.findApp(appID)
	if err != nil {
		return FrontendAppConfigView{}, err
	}
	cfg, err := s.findOrDefaultConfig(app, serviceCode)
	if err != nil {
		return FrontendAppConfigView{}, err
	}
	return toFrontendAppConfigView(cfg), nil
}

func (s *FrontendDeployService) ListStates(appID uint, filter FrontendDeploymentStateFilter) ([]FrontendDeploymentStateView, error) {
	app, err := s.findApp(appID)
	if err != nil {
		return nil, err
	}
	q := s.db.Where("app_id = ?", app.ID)
	if filter.HostID > 0 {
		q = q.Where("host_id = ?", filter.HostID)
	}
	serviceCode := strings.TrimSpace(filter.ServiceCode)
	if serviceCode != "" {
		if !serviceCodeRE.MatchString(serviceCode) {
			return nil, apperr.New("BAD_REQUEST",
				"service_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
		}
		q = q.Where("service_code = ?", serviceCode)
	}
	domain := strings.TrimSpace(filter.Domain)
	if domain != "" {
		normalized, err := normalizeFrontendDomain(domain)
		if err != nil {
			return nil, err
		}
		q = q.Where("domain = ?", normalized)
	}

	var rows []model.FrontendDeploymentState
	if err := q.Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list frontend deployment states", 500)
	}
	hostByID, err := s.frontendStateHosts(rows)
	if err != nil {
		return nil, err
	}
	out := make([]FrontendDeploymentStateView, len(rows))
	for i := range rows {
		out[i] = toFrontendDeploymentStateView(&rows[i], app, hostByID[rows[i].HostID])
	}
	return out, nil
}

func (s *FrontendDeployService) SaveConfig(appID uint, in FrontendAppConfigInput) (FrontendAppConfigView, error) {
	app, err := s.findApp(appID)
	if err != nil {
		return FrontendAppConfigView{}, err
	}
	row, err := normalizeFrontendAppConfig(app, in)
	if err != nil {
		return FrontendAppConfigView{}, err
	}
	var existing model.FrontendAppConfig
	err = s.db.Where("app_id = ? AND service_code = ?", app.ID, row.ServiceCode).First(&existing).Error
	if err == nil {
		row.ID = existing.ID
		row.CreatedAt = existing.CreatedAt
		if err := s.db.Save(row).Error; err != nil {
			return FrontendAppConfigView{}, apperr.Wrap(err, "INTERNAL", "update frontend config", 500)
		}
		return toFrontendAppConfigView(row), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return FrontendAppConfigView{}, apperr.Wrap(err, "INTERNAL", "find frontend config", 500)
	}
	if err := s.db.Create(row).Error; err != nil {
		if isUniqueConstraint(err) {
			return FrontendAppConfigView{}, apperr.New("CONFLICT", "该应用的前端配置已存在", 409)
		}
		return FrontendAppConfigView{}, apperr.Wrap(err, "INTERNAL", "create frontend config", 500)
	}
	return toFrontendAppConfigView(row), nil
}

func (s *FrontendDeployService) Preview(appID uint, serviceCode string) (FrontendTemplatePreviewView, error) {
	app, err := s.findApp(appID)
	if err != nil {
		return FrontendTemplatePreviewView{}, err
	}
	cfg, err := s.findOrDefaultConfig(app, serviceCode)
	if err != nil {
		return FrontendTemplatePreviewView{}, err
	}
	return FrontendTemplatePreviewView{
		Dockerfile:  renderFrontendDockerfile(app, cfg),
		NginxConfig: renderFrontendRuntimeNginxConfig(cfg),
	}, nil
}

func (s *FrontendDeployService) Deploy(ctx context.Context, appID uint, in FrontendDeployInput, actors ...string) (FrontendDeployView, error) {
	if strings.TrimSpace(s.workspace) == "" {
		return FrontendDeployView{}, apperr.New("INTERNAL", "storage.build_workspace 未配置", 500)
	}
	actor := frontendDeployActor(actors...)
	app, err := s.findApp(appID)
	if err != nil {
		return FrontendDeployView{}, err
	}
	if strings.TrimSpace(app.GitURL) == "" {
		return FrontendDeployView{}, apperr.New("BAD_REQUEST", "应用未配置 git_url，无法部署前端项目", 400)
	}
	cfg, err := s.findOrDefaultConfig(app, in.ServiceCode)
	if err != nil {
		return FrontendDeployView{}, err
	}
	domain, err := normalizeFrontendDomain(in.Domain)
	if err != nil {
		return FrontendDeployView{}, err
	}
	host, err := s.findHostForPipeline(in.HostID)
	if err != nil {
		return FrontendDeployView{}, err
	}

	locked := false
	if s.pipelineSvc != nil {
		if err := s.pipelineSvc.TryLockAppForExternalRun(app.ID); err != nil {
			return FrontendDeployView{}, err
		}
		locked = true
	}

	rec, err := s.startFrontendPipelineRun(app, cfg, host, actor)
	if err != nil {
		if locked {
			s.pipelineSvc.ReleaseAppForExternalRun(app.ID)
		}
		return FrontendDeployView{}, err
	}
	rec.PublishStatus(RunStatusRunning)

	runCtx, cancel := context.WithCancel(context.Background())
	if s.pipelineSvc != nil {
		s.pipelineSvc.RegisterCancelForExternalRun(rec.RunID(), cancel)
	}
	go func() {
		defer cancel()
		if s.pipelineSvc != nil {
			defer s.pipelineSvc.UnregisterCancelForExternalRun(rec.RunID())
			defer s.pipelineSvc.ReleaseAppForExternalRun(app.ID)
		}
		s.executeDeploy(runCtx, rec, app, cfg, host, domain, in)
	}()

	return FrontendDeployView{
		AppID: app.ID, AppCode: app.AppCode, ServiceCode: cfg.ServiceCode, HostID: in.HostID,
		PipelineRunID: rec.RunID(), PipelineStatus: RunStatusRunning,
		Domain: domain, ContainerName: cfg.ContainerName,
		DeployedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func (s *FrontendDeployService) Rollback(ctx context.Context, appID uint, in FrontendRollbackInput, actors ...string) (FrontendDeployView, error) {
	actor := frontendDeployActor(actors...)
	app, err := s.findApp(appID)
	if err != nil {
		return FrontendDeployView{}, err
	}
	cfg, err := s.findOrDefaultConfig(app, in.ServiceCode)
	if err != nil {
		return FrontendDeployView{}, err
	}
	domain, err := normalizeFrontendDomain(in.Domain)
	if err != nil {
		return FrontendDeployView{}, err
	}
	host, err := s.findHostForPipeline(in.HostID)
	if err != nil {
		return FrontendDeployView{}, err
	}

	locked := false
	releaseLock := func() {
		if locked && s.pipelineSvc != nil {
			s.pipelineSvc.ReleaseAppForExternalRun(app.ID)
			locked = false
		}
	}
	if s.pipelineSvc != nil {
		if err := s.pipelineSvc.TryLockAppForExternalRun(app.ID); err != nil {
			return FrontendDeployView{}, err
		}
		locked = true
	}

	state, err := s.loadFrontendDeploymentState(app.ID, host.ID, cfg.ServiceCode, domain)
	if err != nil {
		releaseLock()
		return FrontendDeployView{}, err
	}
	if strings.TrimSpace(state.PreviousImage) == "" {
		releaseLock()
		return FrontendDeployView{}, apperr.New("BAD_REQUEST", "没有可回滚的前端镜像", 400)
	}
	if err := validateDockerImageRef(state.PreviousImage); err != nil {
		releaseLock()
		return FrontendDeployView{}, err
	}

	runCfg := *cfg
	if strings.TrimSpace(state.ContainerName) != "" {
		runCfg.ContainerName = state.ContainerName
	}
	if state.TargetPort > 0 {
		runCfg.TargetPort = state.TargetPort
	}

	rec, err := s.startFrontendPipelineRunWithStrategy(app, &runCfg, host, actor, frontendRollbackPipelineStrategy, StageDial)
	if err != nil {
		releaseLock()
		return FrontendDeployView{}, err
	}
	rec.PublishStatus(RunStatusRunning)

	runCtx, cancel := context.WithCancel(context.Background())
	if s.pipelineSvc != nil {
		s.pipelineSvc.RegisterCancelForExternalRun(rec.RunID(), cancel)
	}
	go func() {
		defer cancel()
		if s.pipelineSvc != nil {
			defer s.pipelineSvc.UnregisterCancelForExternalRun(rec.RunID())
			defer s.pipelineSvc.ReleaseAppForExternalRun(app.ID)
		}
		s.executeRollback(runCtx, rec, app, &runCfg, host, state, in)
	}()

	return FrontendDeployView{
		AppID: app.ID, AppCode: app.AppCode, ServiceCode: runCfg.ServiceCode, HostID: in.HostID,
		PipelineRunID: rec.RunID(), PipelineStatus: RunStatusRunning,
		Domain: domain, ContainerName: runCfg.ContainerName,
		Image: state.PreviousImage, CommitSHA: state.PreviousCommitSHA, RemoteWorkDir: state.PreviousRemoteWorkDir,
		DeployedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func (s *FrontendDeployService) executeDeploy(ctx context.Context, rec *frontendPipelineRecorder, app *model.Application, cfg *model.FrontendAppConfig, host *model.Host, domain string, in FrontendDeployInput) {
	if cancelled := rec.CheckCancelled(ctx, FrontendStagePrepare, "前端部署已取消"); cancelled {
		return
	}
	credID := in.CredID
	if credID == 0 {
		credID = parseGitCredID(app.GitCredID)
	}
	stepStarted := time.Now()
	cred, err := s.loadGitCredential(credID)
	if err != nil {
		rec.FailOrCancel(ctx, FrontendStagePrepare, stepStarted, "加载 Git 凭证失败", err)
		return
	}
	stepStarted = time.Now()
	gitBin, execEnv, err := s.resolveGitRuntime()
	if err != nil {
		rec.FailOrCancel(ctx, FrontendStagePrepare, stepStarted, "解析本机 git 运行时失败", err)
		return
	}

	stepStarted = time.Now()
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		wrapped := apperr.Wrap(err, "INTERNAL", "mkdir build workspace", 500)
		rec.FailOrCancel(ctx, FrontendStagePrepare, stepStarted, "创建本机构建工作区失败", wrapped)
		return
	}
	rec.Step(FrontendStagePrepare, stepStarted, true, fmt.Sprintf("workspace=%s", s.workspace), nil)

	if cancelled := rec.CheckCancelled(ctx, FrontendStageGitClone, "Git clone 前取消"); cancelled {
		return
	}
	localDir := filepath.Join(s.workspace, fmt.Sprintf("frontend-%s-%s-%d", app.AppCode, cfg.ServiceCode, time.Now().UnixNano()))
	defer os.RemoveAll(localDir)
	ref := strings.TrimSpace(in.GitRef)
	if ref == "" {
		ref = defaultRef(app.GitRef)
	}
	stepStarted = time.Now()
	sha, err := builder.Clone(ctx, builder.CloneOptions{URL: app.GitURL, GitBin: gitBin, Ref: ref, TargetDir: localDir, Cred: cred, LogWriter: io.Discard, Timeout: 5 * time.Minute, ExecEnv: execEnv})
	if err != nil {
		wrapped := apperr.Wrap(err, "BAD_REQUEST", "拉取 Git 仓库失败: "+err.Error(), 400)
		rec.FailOrCancel(ctx, FrontendStageGitClone, stepStarted, fmt.Sprintf("ref=%s", ref), wrapped)
		return
	}
	rec.Step(FrontendStageGitClone, stepStarted, true, fmt.Sprintf("ref=%s commit=%s", ref, shortSHA(sha)), nil)

	if cancelled := rec.CheckCancelled(ctx, FrontendStagePrepare, "写入构建文件前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := writeFrontendBuildFiles(localDir, app, cfg); err != nil {
		rec.FailOrCancel(ctx, FrontendStagePrepare, stepStarted, "写入 Dockerfile/nginx.conf/.dockerignore 失败", err)
		return
	}
	rec.Step(FrontendStagePrepare, stepStarted, true, "已写入 Dockerfile/nginx.conf/.dockerignore", nil)

	bundlePath := localDir + ".tar.gz"
	defer os.Remove(bundlePath)
	if cancelled := rec.CheckCancelled(ctx, FrontendStagePackage, "打包前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := createTarGz(localDir, bundlePath); err != nil {
		wrapped := apperr.Wrap(err, "INTERNAL", "package frontend workspace", 500)
		rec.FailOrCancel(ctx, FrontendStagePackage, stepStarted, "打包前端构建上下文失败", wrapped)
		return
	}
	rec.Step(FrontendStagePackage, stepStarted, true, fmt.Sprintf("archive=%s", bundlePath), nil)

	if cancelled := rec.CheckCancelled(ctx, StageDial, "SSH 拨号前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	client, err := s.dialHost(ctx, in.HostID)
	if err != nil {
		rec.FailOrCancel(ctx, StageDial, stepStarted, fmt.Sprintf("host=%s", host.IP), err)
		return
	}
	defer client.Close()
	rec.Step(StageDial, stepStarted, true, fmt.Sprintf("host=%s connected", host.IP), nil)

	remoteRoot := path.Join(strings.TrimRight(app.DeployPath, "/"), "frontend", cfg.ServiceCode)
	remoteRelease := path.Join(remoteRoot, fmt.Sprintf("release-%s-%d", shortSHA(sha), time.Now().Unix()))
	remoteArchive := remoteRelease + ".tar.gz"
	if cancelled := rec.CheckCancelled(ctx, StageUpload, "上传前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := s.uploadAndExtract(ctx, client, bundlePath, remoteArchive, remoteRelease); err != nil {
		rec.FailOrCancel(ctx, StageUpload, stepStarted, fmt.Sprintf("remote_dir=%s", remoteRelease), err)
		return
	}
	rec.Step(StageUpload, stepStarted, true, fmt.Sprintf("remote_dir=%s", remoteRelease), nil)

	imageName := fmt.Sprintf("swift-devops/%s-%s:%s", app.AppCode, cfg.ServiceCode, shortSHA(sha))
	if cancelled := rec.CheckCancelled(ctx, FrontendStageDockerBuild, "Docker build 前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := s.remoteBuild(ctx, client, remoteRelease, imageName, cfg); err != nil {
		rec.FailOrCancel(ctx, FrontendStageDockerBuild, stepStarted, fmt.Sprintf("image=%s", imageName), err)
		return
	}
	rec.Step(FrontendStageDockerBuild, stepStarted, true, fmt.Sprintf("image=%s", imageName), nil)

	if cancelled := rec.CheckCancelled(ctx, FrontendStageDockerRun, "Docker run 前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := s.remoteRun(ctx, client, imageName, cfg); err != nil {
		rec.FailOrCancel(ctx, FrontendStageDockerRun, stepStarted, fmt.Sprintf("container=%s image=%s", cfg.ContainerName, imageName), err)
		return
	}
	rec.Step(FrontendStageDockerRun, stepStarted, true, fmt.Sprintf("container=%s network=%s", cfg.ContainerName, defaultFrontendGatewayNetwork), nil)

	stepStarted = time.Now()
	if domain != "" {
		https := boolDeref(in.HTTPS, false)
		routeInput := FrontendGatewayRouteInput{AppID: app.ID, ServiceCode: cfg.ServiceCode, Domain: domain, ContainerName: cfg.ContainerName, TargetPort: cfg.TargetPort, HTTPS: &https, CertPath: in.CertPath, KeyPath: in.KeyPath, Enabled: boolPtr(true)}
		if cancelled := rec.CheckCancelled(ctx, FrontendStageGatewayRoute, "更新 route 前取消"); cancelled {
			return
		}
		route, err := s.upsertRoute(ctx, in.HostID, routeInput)
		if err != nil {
			rec.FailOrCancel(ctx, FrontendStageGatewayRoute, stepStarted, fmt.Sprintf("domain=%s", domain), err)
			return
		}
		rec.Step(FrontendStageGatewayRoute, stepStarted, true, fmt.Sprintf("route_id=%d domain=%s -> %s:%d", route.ID, domain, cfg.ContainerName, cfg.TargetPort), nil)
	} else {
		rec.Step(FrontendStageGatewayRoute, stepStarted, true, "未配置域名；跳过 Gateway route（测试环境可通过 docker_run_args 端口映射直连）", nil)
	}

	stepStarted = time.Now()
	if domain == "" {
		rec.Step(FrontendStageGatewayApply, stepStarted, true, "未配置域名；跳过 Gateway Apply", nil)
	} else if in.ApplyGateway {
		if cancelled := rec.CheckCancelled(ctx, FrontendStageGatewayApply, "Apply Gateway 前取消"); cancelled {
			return
		}
		applied, err := s.gatewaySvc.Apply(ctx, in.HostID, in.ForceRecreateGateway)
		if err != nil {
			rec.FailOrCancel(ctx, FrontendStageGatewayApply, stepStarted, fmt.Sprintf("host_id=%d force_recreate=%t", in.HostID, in.ForceRecreateGateway), err)
			return
		}
		rec.Step(FrontendStageGatewayApply, stepStarted, true, applied.Message, nil)
	} else {
		rec.Step(FrontendStageGatewayApply, stepStarted, true, "apply_gateway=false；已更新 route，需手动 Apply Gateway 生效", nil)
	}

	stepStarted = time.Now()
	if err := s.markFrontendDeploySuccess(app, cfg, host, domain, imageName, sha, remoteRelease, rec.RunID()); err != nil {
		rec.FailOrCancel(ctx, FrontendStageGatewayRoute, stepStarted, "记录前端部署状态失败", err)
		return
	}

	rec.Finish(RunStatusSuccess, "")
}

func (s *FrontendDeployService) executeRollback(ctx context.Context, rec *frontendPipelineRecorder, app *model.Application, cfg *model.FrontendAppConfig, host *model.Host, state *model.FrontendDeploymentState, in FrontendRollbackInput) {
	if cancelled := rec.CheckCancelled(ctx, StageDial, "前端回滚已取消"); cancelled {
		return
	}
	previousImage := strings.TrimSpace(state.PreviousImage)
	if previousImage == "" {
		rec.Fail(StageDial, time.Now(), "读取回滚镜像失败", apperr.New("BAD_REQUEST", "没有可回滚的前端镜像", 400))
		return
	}

	stepStarted := time.Now()
	client, err := s.dialHost(ctx, in.HostID)
	if err != nil {
		rec.FailOrCancel(ctx, StageDial, stepStarted, fmt.Sprintf("host=%s", host.IP), err)
		return
	}
	defer client.Close()
	rec.Step(StageDial, stepStarted, true, fmt.Sprintf("host=%s connected", host.IP), nil)

	if cancelled := rec.CheckCancelled(ctx, FrontendStageImageCheck, "检查回滚镜像前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := s.remoteInspectImage(ctx, client, previousImage); err != nil {
		rec.FailOrCancel(ctx, FrontendStageImageCheck, stepStarted, fmt.Sprintf("image=%s", previousImage), err)
		return
	}
	rec.Step(FrontendStageImageCheck, stepStarted, true, fmt.Sprintf("image=%s exists", previousImage), nil)

	if cancelled := rec.CheckCancelled(ctx, FrontendStageDockerRun, "启动回滚镜像前取消"); cancelled {
		return
	}
	stepStarted = time.Now()
	if err := s.remoteRun(ctx, client, previousImage, cfg); err != nil {
		rec.FailOrCancel(ctx, FrontendStageDockerRun, stepStarted, fmt.Sprintf("rollback container=%s image=%s", cfg.ContainerName, previousImage), err)
		return
	}
	rec.Step(FrontendStageDockerRun, stepStarted, true, fmt.Sprintf("rollback container=%s image=%s", cfg.ContainerName, previousImage), nil)

	stepStarted = time.Now()
	if strings.TrimSpace(state.Domain) != "" {
		routeInput := FrontendGatewayRouteInput{
			AppID:         app.ID,
			ServiceCode:   cfg.ServiceCode,
			Domain:        state.Domain,
			ContainerName: cfg.ContainerName,
			TargetPort:    cfg.TargetPort,
			Enabled:       boolPtr(true),
		}
		if cancelled := rec.CheckCancelled(ctx, FrontendStageGatewayRoute, "更新 route 前取消"); cancelled {
			return
		}
		route, err := s.upsertRoute(ctx, in.HostID, routeInput)
		if err != nil {
			rec.FailOrCancel(ctx, FrontendStageGatewayRoute, stepStarted, fmt.Sprintf("domain=%s", state.Domain), err)
			return
		}
		rec.Step(FrontendStageGatewayRoute, stepStarted, true, fmt.Sprintf("route_id=%d domain=%s -> %s:%d", route.ID, state.Domain, cfg.ContainerName, cfg.TargetPort), nil)
	} else {
		rec.Step(FrontendStageGatewayRoute, stepStarted, true, "无域名部署；跳过 Gateway route", nil)
	}

	stepStarted = time.Now()
	if strings.TrimSpace(state.Domain) == "" {
		rec.Step(FrontendStageGatewayApply, stepStarted, true, "无域名部署；跳过 Gateway Apply", nil)
	} else if in.ApplyGateway {
		if cancelled := rec.CheckCancelled(ctx, FrontendStageGatewayApply, "Apply Gateway 前取消"); cancelled {
			return
		}
		applied, err := s.gatewaySvc.Apply(ctx, in.HostID, in.ForceRecreateGateway)
		if err != nil {
			rec.FailOrCancel(ctx, FrontendStageGatewayApply, stepStarted, fmt.Sprintf("host_id=%d force_recreate=%t", in.HostID, in.ForceRecreateGateway), err)
			return
		}
		rec.Step(FrontendStageGatewayApply, stepStarted, true, applied.Message, nil)
	} else {
		rec.Step(FrontendStageGatewayApply, stepStarted, true, "apply_gateway=false；已更新 route，需手动 Apply Gateway 生效", nil)
	}

	stepStarted = time.Now()
	if err := s.swapFrontendDeploymentStateOnRollback(app.ID, in.HostID, cfg.ServiceCode, state.Domain, cfg, rec.RunID()); err != nil {
		rec.FailOrCancel(ctx, FrontendStageGatewayRoute, stepStarted, "记录前端回滚状态失败", err)
		return
	}

	rec.Finish(RunStatusSuccess, "")
}

func (s *FrontendDeployService) loadFrontendDeploymentState(appID, hostID uint, serviceCode, domain string) (*model.FrontendDeploymentState, error) {
	var row model.FrontendDeploymentState
	err := s.db.Where(
		"app_id = ? AND host_id = ? AND service_code = ? AND domain = ?",
		appID, hostID, strings.TrimSpace(serviceCode), strings.TrimSpace(domain),
	).First(&row).Error
	if err == nil {
		return &row, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.New("BAD_REQUEST", "没有可回滚的前端镜像", 400)
	}
	return nil, apperr.Wrap(err, "INTERNAL", "find frontend deployment state", 500)
}

func (s *FrontendDeployService) markFrontendDeploySuccess(app *model.Application, cfg *model.FrontendAppConfig, host *model.Host, domain, imageName, commitSHA, remoteWorkDir string, runID uint) error {
	if app == nil || cfg == nil || host == nil {
		return apperr.New("INTERNAL", "frontend deployment state input missing", 500)
	}
	serviceCode := strings.TrimSpace(cfg.ServiceCode)
	domain = strings.TrimSpace(domain)
	if serviceCode == "" || strings.TrimSpace(imageName) == "" {
		return apperr.New("INTERNAL", "frontend deployment state key missing", 500)
	}
	now := time.Now()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var row model.FrontendDeploymentState
		err := tx.Where(
			"app_id = ? AND host_id = ? AND service_code = ? AND domain = ?",
			app.ID, host.ID, serviceCode, domain,
		).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.FrontendDeploymentState{
				AppID:                app.ID,
				HostID:               host.ID,
				ServiceCode:          serviceCode,
				Domain:               domain,
				ContainerName:        cfg.ContainerName,
				TargetPort:           cfg.TargetPort,
				CurrentImage:         strings.TrimSpace(imageName),
				CurrentCommitSHA:     strings.TrimSpace(commitSHA),
				CurrentRemoteWorkDir: strings.TrimSpace(remoteWorkDir),
				CurrentPipelineRunID: runID,
				CurrentDeployedAt:    now,
				Status:               "active",
			}
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}

		row.PreviousImage = row.CurrentImage
		row.PreviousCommitSHA = row.CurrentCommitSHA
		row.PreviousRemoteWorkDir = row.CurrentRemoteWorkDir
		row.PreviousPipelineRunID = row.CurrentPipelineRunID
		row.PreviousDeployedAt = row.CurrentDeployedAt
		row.ContainerName = cfg.ContainerName
		row.TargetPort = cfg.TargetPort
		row.CurrentImage = strings.TrimSpace(imageName)
		row.CurrentCommitSHA = strings.TrimSpace(commitSHA)
		row.CurrentRemoteWorkDir = strings.TrimSpace(remoteWorkDir)
		row.CurrentPipelineRunID = runID
		row.CurrentDeployedAt = now
		row.Status = "active"
		return tx.Save(&row).Error
	})
	if err != nil {
		return apperr.Wrap(err, "INTERNAL", "record frontend deployment state", 500)
	}
	return nil
}

func (s *FrontendDeployService) swapFrontendDeploymentStateOnRollback(appID, hostID uint, serviceCode, domain string, cfg *model.FrontendAppConfig, runID uint) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var row model.FrontendDeploymentState
		err := tx.Where(
			"app_id = ? AND host_id = ? AND service_code = ? AND domain = ?",
			appID, hostID, strings.TrimSpace(serviceCode), strings.TrimSpace(domain),
		).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.New("BAD_REQUEST", "没有可回滚的前端镜像", 400)
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(row.PreviousImage) == "" {
			return apperr.New("BAD_REQUEST", "没有可回滚的前端镜像", 400)
		}

		oldCurrentImage := row.CurrentImage
		oldCurrentCommitSHA := row.CurrentCommitSHA
		oldCurrentRemoteWorkDir := row.CurrentRemoteWorkDir
		oldCurrentPipelineRunID := row.CurrentPipelineRunID
		oldCurrentDeployedAt := row.CurrentDeployedAt

		row.CurrentImage = row.PreviousImage
		row.CurrentCommitSHA = row.PreviousCommitSHA
		row.CurrentRemoteWorkDir = row.PreviousRemoteWorkDir
		row.CurrentPipelineRunID = runID
		row.CurrentDeployedAt = time.Now()
		row.PreviousImage = oldCurrentImage
		row.PreviousCommitSHA = oldCurrentCommitSHA
		row.PreviousRemoteWorkDir = oldCurrentRemoteWorkDir
		row.PreviousPipelineRunID = oldCurrentPipelineRunID
		row.PreviousDeployedAt = oldCurrentDeployedAt
		if cfg != nil {
			if strings.TrimSpace(cfg.ContainerName) != "" {
				row.ContainerName = cfg.ContainerName
			}
			if cfg.TargetPort > 0 {
				row.TargetPort = cfg.TargetPort
			}
		}
		row.Status = "active"
		return tx.Save(&row).Error
	})
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return err
		}
		return apperr.Wrap(err, "INTERNAL", "swap frontend deployment state", 500)
	}
	return nil
}

func (s *FrontendDeployService) frontendStateHosts(rows []model.FrontendDeploymentState) (map[uint]*model.Host, error) {
	out := map[uint]*model.Host{}
	if len(rows) == 0 {
		return out, nil
	}
	seen := map[uint]struct{}{}
	ids := make([]uint, 0, len(rows))
	for i := range rows {
		if rows[i].HostID == 0 {
			continue
		}
		if _, ok := seen[rows[i].HostID]; ok {
			continue
		}
		seen[rows[i].HostID] = struct{}{}
		ids = append(ids, rows[i].HostID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	var hosts []model.Host
	if err := s.db.Select("id", "name", "ip").Where("id IN ?", ids).Find(&hosts).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list frontend state hosts", 500)
	}
	for i := range hosts {
		out[hosts[i].ID] = &hosts[i]
	}
	return out, nil
}

func (s *FrontendDeployService) findApp(appID uint) (*model.Application, error) {
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	if strings.TrimSpace(app.AppType) != "frontend" {
		return nil, apperr.New("BAD_REQUEST", "仅 app_type=frontend 的应用可以使用前端部署链路", 400)
	}
	return &app, nil
}

func frontendDeployActor(actors ...string) string {
	for _, actor := range actors {
		if v := strings.TrimSpace(actor); v != "" {
			return v
		}
	}
	return "unknown"
}

func (s *FrontendDeployService) findHostForPipeline(hostID uint) (*model.Host, error) {
	if hostID == 0 {
		return nil, apperr.New("BAD_REQUEST", "host_id 必填", 400)
	}
	var host model.Host
	if err := s.db.Select("id", "name", "ip").First(&host, hostID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("主机 %d 不存在", hostID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find host", 500)
	}
	return &host, nil
}

func (s *FrontendDeployService) startFrontendPipelineRun(app *model.Application, cfg *model.FrontendAppConfig, host *model.Host, actor string) (*frontendPipelineRecorder, error) {
	return s.startFrontendPipelineRunWithStrategy(app, cfg, host, actor, frontendPipelineStrategy, FrontendStagePrepare)
}

func (s *FrontendDeployService) startFrontendPipelineRunWithStrategy(app *model.Application, cfg *model.FrontendAppConfig, host *model.Host, actor, pipelineStrategy, initialStage string) (*frontendPipelineRecorder, error) {
	if app == nil || cfg == nil || host == nil {
		return nil, apperr.New("INTERNAL", "frontend pipeline recorder input missing", 500)
	}
	pipelineStrategy = strings.TrimSpace(pipelineStrategy)
	if pipelineStrategy == "" {
		pipelineStrategy = frontendPipelineStrategy
	}
	initialStage = strings.TrimSpace(initialStage)
	if initialStage == "" {
		initialStage = FrontendStagePrepare
	}
	now := time.Now()
	snap := strategy.RunSnapshot{
		Strategy:  pipelineStrategy,
		StartedAt: now.Format(time.RFC3339),
	}
	snapJSON, _ := json.Marshal(snap)
	run := &model.PipelineRun{
		AppID:         app.ID,
		Strategy:      pipelineStrategy,
		Status:        RunStatusRunning,
		StateSnapshot: string(snapJSON),
		TriggeredBy:   frontendDeployActor(actor),
		StartedAt:     &now,
	}
	if err := s.db.Create(run).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "create frontend pipeline run", 500)
	}
	runHost := &model.PipelineRunHost{
		RunID:        run.ID,
		HostID:       host.ID,
		ServiceCode:  cfg.ServiceCode,
		Status:       strategy.HostStatusRunning,
		CurrentStage: initialStage,
		StartedAt:    &now,
	}
	if err := s.db.Create(runHost).Error; err != nil {
		now := time.Now()
		_ = s.db.Model(&model.PipelineRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":      RunStatusFailed,
			"finished_at": &now,
		}).Error
		return nil, apperr.Wrap(err, "INTERNAL", "create frontend pipeline host row", 500)
	}
	return &frontendPipelineRecorder{
		db:        s.db,
		pub:       s.pub,
		runID:     run.ID,
		runHostID: runHost.ID,
		host:      *host,
		startedAt: now,
		strategy:  pipelineStrategy,
		steps:     []strategy.StepResult{},
	}, nil
}

type frontendPipelineRecorder struct {
	db        *gorm.DB
	pub       Publisher
	runID     uint
	runHostID uint
	host      model.Host
	startedAt time.Time
	strategy  string
	steps     []strategy.StepResult
}

func (r *frontendPipelineRecorder) RunID() uint {
	if r == nil {
		return 0
	}
	return r.runID
}

func (r *frontendPipelineRecorder) Step(stage string, started time.Time, ok bool, detail string, stepErr error) {
	if r == nil || r.db == nil || r.runID == 0 {
		return
	}
	if started.IsZero() {
		started = time.Now()
	}
	ended := time.Now()
	st := strategy.StepResult{
		HostID:    r.host.ID,
		HostName:  r.host.Name,
		HostIP:    r.host.IP,
		Stage:     stage,
		OK:        ok,
		Detail:    strings.TrimSpace(detail),
		StartedAt: started.Format(time.RFC3339),
		EndedAt:   ended.Format(time.RFC3339),
	}
	if stepErr != nil {
		st.Error = stepErr.Error()
	}
	r.steps = append(r.steps, st)
	r.persistSnapshot("")
	r.publishStep(st)

	hostStatus := strategy.HostStatusRunning
	if !ok {
		hostStatus = strategy.HostStatusFailed
	}
	updates := map[string]any{
		"status":        hostStatus,
		"current_stage": stage,
	}
	if !ok {
		updates["ended_at"] = &ended
	}
	if err := r.db.Model(&model.PipelineRunHost{}).Where("id = ?", r.runHostID).Updates(updates).Error; err != nil {
		slog.Warn("frontend pipeline: update host step", "run_id", r.runID, "err", err)
	}
}

func (r *frontendPipelineRecorder) FailOrCancel(ctx context.Context, stage string, started time.Time, detail string, err error) {
	if frontendContextCancelled(ctx, err) {
		r.Cancel(stage, started, detail)
		return
	}
	r.Fail(stage, started, detail, err)
}

func (r *frontendPipelineRecorder) Fail(stage string, started time.Time, detail string, err error) {
	r.Step(stage, started, false, detail, err)
	r.Finish(RunStatusFailed, frontendPipelineErrorMessage(err))
}

func (r *frontendPipelineRecorder) Cancel(stage string, started time.Time, detail string) {
	if strings.TrimSpace(detail) == "" {
		detail = "用户取消前端部署"
	}
	r.Step(stage, started, false, detail, context.Canceled)
	r.Finish(RunStatusCancelled, "前端部署已取消")
}

func (r *frontendPipelineRecorder) CheckCancelled(ctx context.Context, stage string, detail string) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	r.Cancel(stage, time.Now(), detail)
	return true
}

func (r *frontendPipelineRecorder) Finish(status, errMsg string) {
	if r == nil || r.db == nil || r.runID == 0 {
		return
	}
	r.persistFinalSnapshot(errMsg)
	now := time.Now()
	if err := r.db.Model(&model.PipelineRun{}).Where("id = ?", r.runID).Updates(map[string]any{
		"status":      status,
		"finished_at": &now,
	}).Error; err != nil {
		slog.Warn("frontend pipeline: finish run", "run_id", r.runID, "err", err)
	}
	hostStatus := strategy.HostStatusSuccess
	if status == RunStatusFailed {
		hostStatus = strategy.HostStatusFailed
	} else if status == RunStatusCancelled {
		hostStatus = strategy.HostStatusSkipped
	}
	if err := r.db.Model(&model.PipelineRunHost{}).Where("id = ?", r.runHostID).Updates(map[string]any{
		"status":   hostStatus,
		"ended_at": &now,
	}).Error; err != nil {
		slog.Warn("frontend pipeline: finish host", "run_id", r.runID, "err", err)
	}
	r.PublishStatus(status)
}

func (r *frontendPipelineRecorder) persistSnapshot(errMsg string) {
	if r == nil || r.db == nil || r.runID == 0 {
		return
	}
	pipelineStrategy := strings.TrimSpace(r.strategy)
	if pipelineStrategy == "" {
		pipelineStrategy = frontendPipelineStrategy
	}
	snap := strategy.RunSnapshot{
		Strategy:   pipelineStrategy,
		StartedAt:  r.startedAt.Format(time.RFC3339),
		FinishedAt: "",
		Steps:      append([]strategy.StepResult(nil), r.steps...),
		Error:      strings.TrimSpace(errMsg),
	}
	if strings.TrimSpace(errMsg) != "" {
		snap.FinishedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("frontend pipeline: marshal snapshot", "run_id", r.runID, "err", err)
		return
	}
	if err := r.db.Model(&model.PipelineRun{}).Where("id = ?", r.runID).
		Update("state_snapshot", string(data)).Error; err != nil {
		slog.Warn("frontend pipeline: persist snapshot", "run_id", r.runID, "err", err)
	}
}

func (r *frontendPipelineRecorder) persistFinalSnapshot(errMsg string) {
	if r == nil || r.db == nil || r.runID == 0 {
		return
	}
	pipelineStrategy := strings.TrimSpace(r.strategy)
	if pipelineStrategy == "" {
		pipelineStrategy = frontendPipelineStrategy
	}
	snap := strategy.RunSnapshot{
		Strategy:   pipelineStrategy,
		StartedAt:  r.startedAt.Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
		Steps:      append([]strategy.StepResult(nil), r.steps...),
		Error:      strings.TrimSpace(errMsg),
	}
	data, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("frontend pipeline: marshal final snapshot", "run_id", r.runID, "err", err)
		return
	}
	if err := r.db.Model(&model.PipelineRun{}).Where("id = ?", r.runID).
		Update("state_snapshot", string(data)).Error; err != nil {
		slog.Warn("frontend pipeline: persist final snapshot", "run_id", r.runID, "err", err)
	}
}

func frontendPipelineErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if ae, ok := apperr.As(err); ok {
		return ae.Message
	}
	return err.Error()
}

func frontendContextCancelled(ctx context.Context, err error) bool {
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return true
	}
	return errors.Is(err, context.Canceled)
}

func (r *frontendPipelineRecorder) publishStep(st strategy.StepResult) {
	if r == nil || r.pub == nil {
		return
	}
	data, err := json.Marshal(PipelineEvent{
		Type: "step", RunID: r.runID, Step: &st,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("frontend pipeline: marshal step event", "run_id", r.runID, "err", err)
		return
	}
	r.pub.Publish(PipelineTopic(r.runID), data)
}

func (r *frontendPipelineRecorder) PublishStatus(status string) {
	if r == nil || r.pub == nil {
		return
	}
	data, err := json.Marshal(PipelineEvent{
		Type: "status", RunID: r.runID, Status: status,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("frontend pipeline: marshal status event", "run_id", r.runID, "err", err)
		return
	}
	r.pub.Publish(PipelineTopic(r.runID), data)
}

func (s *FrontendDeployService) findOrDefaultConfig(app *model.Application, serviceCode string) (*model.FrontendAppConfig, error) {
	code := strings.TrimSpace(serviceCode)
	if code == "" {
		code = defaultFrontendServiceCode
	}
	var row model.FrontendAppConfig
	if err := s.db.Where("app_id = ? AND service_code = ?", app.ID, code).First(&row).Error; err == nil {
		return &row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.Wrap(err, "INTERNAL", "find frontend config", 500)
	}
	return normalizeFrontendAppConfig(app, FrontendAppConfigInput{ServiceCode: code})
}

func (s *FrontendDeployService) loadGitCredential(credID uint) (*builder.Credential, error) {
	if credID == 0 {
		return nil, nil
	}
	if s.credSvc == nil {
		return nil, apperr.New("INTERNAL", "Git 凭证服务未初始化", 500)
	}
	typ, user, secret, err := s.credSvc.LoadSecret(credID)
	if err != nil {
		return nil, err
	}
	plain, err := unwrapSecretBlob(secret)
	if err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "unwrap git credential", 500)
	}
	return &builder.Credential{Type: typ, Username: user, Secret: plain}, nil
}

func (s *FrontendDeployService) resolveGitRuntime() (string, []string, error) {
	gitBin := "/usr/bin/git"
	if s.envSvc != nil {
		env, loadErr := s.envSvc.Load()
		if loadErr == nil && strings.TrimSpace(env.GitPath) != "" {
			gitBin = strings.TrimSpace(env.GitPath)
		}
		execEnv, _ := s.envSvc.BuildExecEnv()
		if !fileExecutable(gitBin) {
			return "", nil, apperr.New("BAD_REQUEST", fmt.Sprintf("git 不可执行：%s；请去「⚙ 构建环境」填 git 绝对路径", gitBin), 400)
		}
		return gitBin, execEnv, nil
	}
	if !fileExecutable(gitBin) {
		return "", nil, apperr.New("BAD_REQUEST", "git_path 未配且 /usr/bin/git 不存在；请在「构建环境」填 git 绝对路径", 400)
	}
	return gitBin, nil, nil
}

func (s *FrontendDeployService) dialHost(ctx context.Context, hostID uint) (*sshpkg.Client, error) {
	if s.hostSvc == nil {
		return nil, apperr.New("INTERNAL", "host service 未初始化", 500)
	}
	target, auth, host, err := s.hostSvc.LoadAuth(hostID)
	if err != nil {
		return nil, err
	}
	client, err := sshpkg.Dial(target, auth, s.sshOpts)
	if err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "dial host", 500)
	}
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		s.hostSvc.RecordHostKey(host.ID, client.LearnedHostKey())
	}
	return client, nil
}

func (s *FrontendDeployService) uploadAndExtract(ctx context.Context, client *sshpkg.Client, localArchive, remoteArchive, remoteRelease string) error {
	if err := runRemoteChecked(ctx, client, fmt.Sprintf("rm -rf %s && mkdir -p %s", deploy.ShellQuote(remoteRelease), deploy.ShellQuote(remoteRelease)), "创建前端远端工作目录失败"); err != nil {
		return err
	}
	dispatcher := deploy.NewDispatcher(client.SSHClient())
	if _, err := dispatcher.Upload(localArchive, remoteArchive); err != nil {
		return apperr.Wrap(err, "INTERNAL", "upload frontend archive", 500)
	}
	cmd := fmt.Sprintf("tar -xzf %s -C %s && rm -f %s", deploy.ShellQuote(remoteArchive), deploy.ShellQuote(remoteRelease), deploy.ShellQuote(remoteArchive))
	return runRemoteChecked(ctx, client, cmd, "解压前端构建上下文失败")
}

func (s *FrontendDeployService) remoteBuildAndRun(ctx context.Context, client *sshpkg.Client, remoteRelease, imageName string, cfg *model.FrontendAppConfig) error {
	if err := s.remoteBuild(ctx, client, remoteRelease, imageName, cfg); err != nil {
		return err
	}
	return s.remoteRun(ctx, client, imageName, cfg)
}

func (s *FrontendDeployService) remoteBuild(ctx context.Context, client *sshpkg.Client, remoteRelease, imageName string, cfg *model.FrontendAppConfig) error {
	buildArgs := frontendShellJoinFields(cfg.DockerBuildArgs)
	if buildArgs != "" {
		buildArgs += " "
	}
	buildCmd := fmt.Sprintf("cd %s && docker build %s-t %s -f Dockerfile .", deploy.ShellQuote(remoteRelease), buildArgs, deploy.ShellQuote(imageName))
	if err := runRemoteChecked(ctx, client, buildCmd, "前端镜像构建失败"); err != nil {
		return err
	}
	return nil
}

func (s *FrontendDeployService) remoteRun(ctx context.Context, client *sshpkg.Client, imageName string, cfg *model.FrontendAppConfig) error {
	runArgs := frontendShellJoinFields(cfg.DockerRunArgs)
	if runArgs == "" {
		runArgs = fmt.Sprintf("-d --restart=unless-stopped --network %s", deploy.ShellQuote(defaultFrontendGatewayNetwork))
	} else if !strings.Contains(runArgs, "--network") && !strings.Contains(runArgs, "--net") {
		runArgs = fmt.Sprintf("%s --network %s", runArgs, deploy.ShellQuote(defaultFrontendGatewayNetwork))
	}
	cmd := fmt.Sprintf("docker network inspect %s >/dev/null 2>&1 || docker network create %s >/dev/null\n"+
		"docker rm -f %s >/dev/null 2>&1 || true\n"+
		"docker run --name %s %s %s\n"+
		"docker inspect -f '{{.State.Running}}' %s",
		deploy.ShellQuote(defaultFrontendGatewayNetwork), deploy.ShellQuote(defaultFrontendGatewayNetwork),
		deploy.ShellQuote(cfg.ContainerName), deploy.ShellQuote(cfg.ContainerName), runArgs, deploy.ShellQuote(imageName), deploy.ShellQuote(cfg.ContainerName))
	out, err := client.Exec(ctx, cmd)
	if err != nil {
		return apperr.Wrap(err, "INTERNAL", "启动前端容器", 500)
	}
	if out.ExitCode != 0 {
		return remoteCommandError("启动前端容器失败", out)
	}
	if !strings.Contains(out.Stdout, "true") {
		return apperr.New("BAD_REQUEST", "前端容器未进入 running 状态", 400)
	}
	return nil
}

func (s *FrontendDeployService) remoteInspectImage(ctx context.Context, client *sshpkg.Client, imageName string) error {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return apperr.New("BAD_REQUEST", "回滚镜像为空", 400)
	}
	if err := validateDockerImageRef(imageName); err != nil {
		return err
	}
	out, err := client.Exec(ctx, frontendDockerImageInspectCommand(imageName))
	if err != nil {
		return apperr.Wrap(err, "INTERNAL", "检查前端回滚镜像", 500)
	}
	if out.ExitCode != 0 {
		return remoteCommandError("前端回滚镜像预检失败", out)
	}
	return nil
}

func frontendDockerImageInspectCommand(imageName string) string {
	q := deploy.ShellQuote(strings.TrimSpace(imageName))
	okMsg := deploy.ShellQuote("image exists: " + strings.TrimSpace(imageName))
	missingMsg := deploy.ShellQuote("image not found: " + strings.TrimSpace(imageName))
	return fmt.Sprintf(
		"if docker image inspect %s >/dev/null 2>&1; then echo %s; else echo %s; exit 1; fi",
		q, okMsg, missingMsg,
	)
}

func (s *FrontendDeployService) upsertRoute(ctx context.Context, hostID uint, in FrontendGatewayRouteInput) (FrontendGatewayRouteView, error) {
	if s.gatewaySvc == nil {
		return FrontendGatewayRouteView{}, apperr.New("INTERNAL", "frontend gateway service 未初始化", 500)
	}
	routes, err := s.gatewaySvc.ListRoutes(hostID)
	if err != nil {
		if ae, ok := apperr.As(err); !ok || ae.Code != "NOT_FOUND" {
			return FrontendGatewayRouteView{}, err
		}
	}
	for _, route := range routes {
		if route.Domain == in.Domain {
			return s.gatewaySvc.UpdateRoute(ctx, route.ID, in, false)
		}
	}
	return s.gatewaySvc.CreateRoute(ctx, hostID, in, false)
}

func normalizeFrontendAppConfig(app *model.Application, in FrontendAppConfigInput) (*model.FrontendAppConfig, error) {
	code := strings.TrimSpace(in.ServiceCode)
	if code == "" {
		code = defaultFrontendServiceCode
	}
	if !serviceCodeRE.MatchString(code) {
		return nil, apperr.New("BAD_REQUEST", "service_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
	}
	pm := strings.ToLower(strings.TrimSpace(in.PackageManager))
	if pm == "" {
		pm = defaultFrontendPackageManager
	}
	if err := validateFrontendPackageManager(pm); err != nil {
		return nil, err
	}
	buildCommand := strings.TrimSpace(in.BuildCommand)
	if buildCommand == "" {
		buildCommand = defaultBuildCommandForPackageManager(pm)
	}
	distDir := strings.TrimSpace(in.DistDir)
	if distDir == "" {
		distDir = defaultFrontendDistDir
	}
	if err := validateRelativeCleanPath("dist_dir", distDir); err != nil {
		return nil, err
	}
	nodeImage := strings.TrimSpace(in.NodeImage)
	if nodeImage == "" {
		nodeImage = defaultFrontendNodeImage
	}
	if err := validateDockerImageRef(nodeImage); err != nil {
		return nil, err
	}
	nginxImage := strings.TrimSpace(in.NginxImage)
	if nginxImage == "" {
		nginxImage = defaultFrontendNginxImage
	}
	if err := validateDockerImageRef(nginxImage); err != nil {
		return nil, err
	}
	targetPort := in.TargetPort
	if targetPort == 0 {
		targetPort = 80
	}
	if err := validateTCPPort("target_port", targetPort); err != nil {
		return nil, err
	}
	containerName := strings.TrimSpace(in.ContainerName)
	if containerName == "" {
		containerName = fmt.Sprintf("sd-fe-%s-%s", app.AppCode, code)
	}
	if err := deploy.ValidateDockerContainerName(containerName); err != nil {
		return nil, apperr.New("BAD_REQUEST", "container_name "+err.Error(), 400)
	}
	spaFallback := true
	if in.SPAFallback != nil {
		spaFallback = *in.SPAFallback
	}
	if err := validateShellSnippet("install_command", in.InstallCommand); err != nil {
		return nil, err
	}
	if err := validateShellSnippet("build_command", buildCommand); err != nil {
		return nil, err
	}
	if err := validateShellSnippet("docker_build_args", in.DockerBuildArgs); err != nil {
		return nil, err
	}
	if err := validateShellSnippet("docker_run_args", in.DockerRunArgs); err != nil {
		return nil, err
	}
	return &model.FrontendAppConfig{AppID: app.ID, ServiceCode: code, PackageManager: pm, InstallCommand: strings.TrimSpace(in.InstallCommand), BuildCommand: buildCommand, DistDir: distDir, NodeImage: nodeImage, NginxImage: nginxImage, SPAFallback: spaFallback, ContainerName: containerName, TargetPort: targetPort, Dockerfile: in.Dockerfile, NginxConfig: in.NginxConfig, DockerBuildArgs: strings.TrimSpace(in.DockerBuildArgs), DockerRunArgs: strings.TrimSpace(in.DockerRunArgs)}, nil
}

func validateFrontendPackageManager(pm string) error {
	switch pm {
	case "auto", "npm", "pnpm", "yarn":
		return nil
	default:
		return apperr.New("BAD_REQUEST", "package_manager 仅支持 auto / npm / pnpm / yarn", 400)
	}
}

func defaultBuildCommandForPackageManager(pm string) string {
	switch pm {
	case "pnpm":
		return "pnpm build"
	case "yarn":
		return "yarn build"
	default:
		return defaultFrontendBuildCommand
	}
}

func validateRelativeCleanPath(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return apperr.New("BAD_REQUEST", name+" 不能为空", 400)
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\\\x00\r\n") {
		return apperr.New("BAD_REQUEST", name+" 必须是安全的相对路径", 400)
	}
	return nil
}

var forbiddenShellSnippetRE = regexp.MustCompile(`[\x00\r\n]`)

func validateShellSnippet(name, value string) error {
	if forbiddenShellSnippetRE.MatchString(value) {
		return apperr.New("BAD_REQUEST", name+" 不能包含换行或控制字符", 400)
	}
	return nil
}

func writeFrontendBuildFiles(workDir string, app *model.Application, cfg *model.FrontendAppConfig) error {
	if err := os.WriteFile(filepath.Join(workDir, "Dockerfile"), []byte(renderFrontendDockerfile(app, cfg)), 0o644); err != nil {
		return apperr.Wrap(err, "INTERNAL", "write frontend Dockerfile", 500)
	}
	if err := os.WriteFile(filepath.Join(workDir, "nginx.conf"), []byte(renderFrontendRuntimeNginxConfig(cfg)), 0o644); err != nil {
		return apperr.Wrap(err, "INTERNAL", "write frontend nginx.conf", 500)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".dockerignore"), []byte(defaultFrontendDockerignore()), 0o644); err != nil {
		return apperr.Wrap(err, "INTERNAL", "write frontend .dockerignore", 500)
	}
	return nil
}

func renderFrontendDockerfile(app *model.Application, cfg *model.FrontendAppConfig) string {
	if strings.TrimSpace(cfg.Dockerfile) != "" {
		return renderFrontendTemplateTokens(cfg.Dockerfile, app, cfg)
	}
	install := strings.TrimSpace(cfg.InstallCommand)
	if install == "" {
		install = defaultInstallCommand(cfg.PackageManager)
	}
	return renderFrontendTemplateTokens(fmt.Sprintf(`# Generated by swift-devops. Do not edit manually.
FROM {{NODE_IMAGE}} AS builder
WORKDIR /app

COPY package.json ./
COPY package-lock.json* ./
COPY pnpm-lock.yaml* ./
COPY yarn.lock* ./
RUN %s

COPY . .
RUN %s

FROM {{NGINX_IMAGE}}
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/{{DIST_DIR}} /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
`, install, cfg.BuildCommand), app, cfg)
}

func defaultInstallCommand(pm string) string {
	switch strings.ToLower(strings.TrimSpace(pm)) {
	case "npm":
		return `if [ -f package-lock.json ]; then npm ci; else npm install; fi`
	case "pnpm":
		return `corepack enable && pnpm install --frozen-lockfile`
	case "yarn":
		return `corepack enable && yarn install --frozen-lockfile`
	default:
		return `if [ -f pnpm-lock.yaml ]; then corepack enable && pnpm install --frozen-lockfile; elif [ -f yarn.lock ]; then corepack enable && yarn install --frozen-lockfile; elif [ -f package-lock.json ]; then npm ci; else npm install; fi`
	}
}

func renderFrontendRuntimeNginxConfig(cfg *model.FrontendAppConfig) string {
	if strings.TrimSpace(cfg.NginxConfig) != "" {
		return cfg.NginxConfig
	}
	fallback := "try_files $uri $uri/ =404;"
	if cfg.SPAFallback {
		fallback = "try_files $uri $uri/ /index.html;"
	}
	return fmt.Sprintf(`server {
    listen 80;
    server_name _;

    root /usr/share/nginx/html;
    index index.html;

    location / {
        %s
    }

    location ~* \.(js|css|png|jpg|jpeg|gif|svg|ico|woff2?)$ {
        expires 30d;
        add_header Cache-Control "public, immutable";
        try_files $uri =404;
    }
}
`, fallback)
}

func renderFrontendTemplateTokens(raw string, app *model.Application, cfg *model.FrontendAppConfig) string {
	repl := map[string]string{"{{APP_CODE}}": app.AppCode, "{{SERVICE_CODE}}": cfg.ServiceCode, "{{NODE_IMAGE}}": cfg.NodeImage, "{{NGINX_IMAGE}}": cfg.NginxImage, "{{DIST_DIR}}": cfg.DistDir, "{{BUILD_COMMAND}}": cfg.BuildCommand}
	out := raw
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

func defaultFrontendDockerignore() string {
	return `node_modules
.git
.gitignore
Dockerfile
.dockerignore
*.log
dist
build
coverage
.env.local
.env.*.local
`
}

func createTarGz(srcDir, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if shouldSkipFrontendArchive(rel, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}

func shouldSkipFrontendArchive(rel string, info os.FileInfo) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case ".git", "node_modules", "coverage":
		return true
	}
	if info.IsDir() && (parts[0] == "dist" || parts[0] == "build") {
		return true
	}
	return false
}

func frontendShellJoinFields(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = deploy.ShellQuote(f)
	}
	return strings.Join(quoted, " ")
}

func boolPtr(v bool) *bool { return &v }
