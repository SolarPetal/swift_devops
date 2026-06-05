package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
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
)

const (
	defaultFrontendServiceCode    = "web"
	defaultFrontendPackageManager = "auto"
	defaultFrontendBuildCommand   = "npm run build"
	defaultFrontendDistDir        = "dist"
	defaultFrontendNodeImage      = "node:20-alpine"
	defaultFrontendNginxImage     = "nginx:1.27-alpine"
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
	Domain               string `json:"domain" binding:"required"`
	HTTPS                *bool  `json:"https,omitempty"`
	CertPath             string `json:"cert_path,omitempty"`
	KeyPath              string `json:"key_path,omitempty"`
	ApplyGateway         bool   `json:"apply_gateway,omitempty"`
	ForceRecreateGateway bool   `json:"force_recreate_gateway,omitempty"`
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
	AppID         uint                      `json:"app_id"`
	AppCode       string                    `json:"app_code"`
	ServiceCode   string                    `json:"service_code"`
	HostID        uint                      `json:"host_id"`
	Domain        string                    `json:"domain"`
	ContainerName string                    `json:"container_name"`
	Image         string                    `json:"image"`
	CommitSHA     string                    `json:"commit_sha"`
	RemoteWorkDir string                    `json:"remote_work_dir"`
	GatewayRoute  FrontendGatewayRouteView  `json:"gateway_route"`
	GatewayApply  *FrontendGatewayApplyView `json:"gateway_apply,omitempty"`
	DeployedAt    string                    `json:"deployed_at"`
}

// FrontendTemplatePreviewView 前端 Dockerfile/nginx.conf 预览。
type FrontendTemplatePreviewView struct {
	Dockerfile  string `json:"dockerfile"`
	NginxConfig string `json:"nginx_config"`
}

// FrontendDeployService 执行前端项目源码到 Docker 容器的部署。
type FrontendDeployService struct {
	db         *gorm.DB
	hostSvc    *HostService
	credSvc    *GitCredentialService
	envSvc     *BuilderEnvService
	gatewaySvc *FrontendGatewayService
	workspace  string
	sshOpts    sshpkg.DialOptions
}

func NewFrontendDeployService(db *gorm.DB, hostSvc *HostService, credSvc *GitCredentialService, envSvc *BuilderEnvService, gatewaySvc *FrontendGatewayService, workspace string, sshTimeout time.Duration) *FrontendDeployService {
	return &FrontendDeployService{db: db, hostSvc: hostSvc, credSvc: credSvc, envSvc: envSvc, gatewaySvc: gatewaySvc, workspace: workspace, sshOpts: sshpkg.DialOptions{Timeout: sshTimeout}}
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

func (s *FrontendDeployService) Deploy(ctx context.Context, appID uint, in FrontendDeployInput) (FrontendDeployView, error) {
	if strings.TrimSpace(s.workspace) == "" {
		return FrontendDeployView{}, apperr.New("INTERNAL", "storage.build_workspace 未配置", 500)
	}
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
	if domain == "" {
		return FrontendDeployView{}, apperr.New("BAD_REQUEST", "domain 必填", 400)
	}

	credID := in.CredID
	if credID == 0 {
		credID = parseGitCredID(app.GitCredID)
	}
	cred, err := s.loadGitCredential(credID)
	if err != nil {
		return FrontendDeployView{}, err
	}
	gitBin, execEnv, err := s.resolveGitRuntime()
	if err != nil {
		return FrontendDeployView{}, err
	}

	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return FrontendDeployView{}, apperr.Wrap(err, "INTERNAL", "mkdir build workspace", 500)
	}
	localDir := filepath.Join(s.workspace, fmt.Sprintf("frontend-%s-%s-%d", app.AppCode, cfg.ServiceCode, time.Now().UnixNano()))
	defer os.RemoveAll(localDir)
	ref := strings.TrimSpace(in.GitRef)
	if ref == "" {
		ref = defaultRef(app.GitRef)
	}
	sha, err := builder.Clone(ctx, builder.CloneOptions{URL: app.GitURL, GitBin: gitBin, Ref: ref, TargetDir: localDir, Cred: cred, LogWriter: io.Discard, Timeout: 5 * time.Minute, ExecEnv: execEnv})
	if err != nil {
		return FrontendDeployView{}, apperr.Wrap(err, "BAD_REQUEST", "拉取 Git 仓库失败: "+err.Error(), 400)
	}
	if err := writeFrontendBuildFiles(localDir, app, cfg); err != nil {
		return FrontendDeployView{}, err
	}

	bundlePath := localDir + ".tar.gz"
	defer os.Remove(bundlePath)
	if err := createTarGz(localDir, bundlePath); err != nil {
		return FrontendDeployView{}, apperr.Wrap(err, "INTERNAL", "package frontend workspace", 500)
	}

	client, err := s.dialHost(ctx, in.HostID)
	if err != nil {
		return FrontendDeployView{}, err
	}
	defer client.Close()

	remoteRoot := path.Join(strings.TrimRight(app.DeployPath, "/"), "frontend", cfg.ServiceCode)
	remoteRelease := path.Join(remoteRoot, fmt.Sprintf("release-%s-%d", shortSHA(sha), time.Now().Unix()))
	remoteArchive := remoteRelease + ".tar.gz"
	if err := s.uploadAndExtract(ctx, client, bundlePath, remoteArchive, remoteRelease); err != nil {
		return FrontendDeployView{}, err
	}
	imageName := fmt.Sprintf("swift-devops/%s-%s:%s", app.AppCode, cfg.ServiceCode, shortSHA(sha))
	if err := s.remoteBuildAndRun(ctx, client, remoteRelease, imageName, cfg); err != nil {
		return FrontendDeployView{}, err
	}

	https := boolDeref(in.HTTPS, false)
	routeInput := FrontendGatewayRouteInput{AppID: app.ID, ServiceCode: cfg.ServiceCode, Domain: domain, ContainerName: cfg.ContainerName, TargetPort: cfg.TargetPort, HTTPS: &https, CertPath: in.CertPath, KeyPath: in.KeyPath, Enabled: boolPtr(true)}
	route, err := s.upsertRoute(ctx, in.HostID, routeInput)
	if err != nil {
		return FrontendDeployView{}, err
	}
	var applyView *FrontendGatewayApplyView
	if in.ApplyGateway {
		applied, err := s.gatewaySvc.Apply(ctx, in.HostID, in.ForceRecreateGateway)
		if err != nil {
			return FrontendDeployView{}, err
		}
		applyView = &applied
	}

	return FrontendDeployView{AppID: app.ID, AppCode: app.AppCode, ServiceCode: cfg.ServiceCode, HostID: in.HostID, Domain: domain, ContainerName: cfg.ContainerName, Image: imageName, CommitSHA: sha, RemoteWorkDir: remoteRelease, GatewayRoute: route, GatewayApply: applyView, DeployedAt: time.Now().Format(time.RFC3339)}, nil
}

func (s *FrontendDeployService) findApp(appID uint) (*model.Application, error) {
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	return &app, nil
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
	buildArgs := frontendShellJoinFields(cfg.DockerBuildArgs)
	if buildArgs != "" {
		buildArgs += " "
	}
	buildCmd := fmt.Sprintf("cd %s && docker build %s-t %s -f Dockerfile .", deploy.ShellQuote(remoteRelease), buildArgs, deploy.ShellQuote(imageName))
	if err := runRemoteChecked(ctx, client, buildCmd, "前端镜像构建失败"); err != nil {
		return err
	}
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

func (s *FrontendDeployService) upsertRoute(ctx context.Context, hostID uint, in FrontendGatewayRouteInput) (FrontendGatewayRouteView, error) {
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
