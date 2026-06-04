// Package service 中的 AppService（可部署服务层）CRUD —— Sprint X.4。
//
// AppService 是 Sprint X.1 引入的多 service 模型的最小可操作单元。
// 应用创建后不再自动生成 service_code='default'；服务清单必须由用户
// 手动创建、扫描导入或批量导入产生。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/builder"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
)

// AppServiceInput AppService 创建/更新入参。
type AppServiceInput struct {
	ServiceCode     string `json:"service_code" binding:"required"`
	Name            string `json:"name,omitempty"`
	BuildModule     string `json:"build_module,omitempty"`
	BuildJarPattern string `json:"build_jar_pattern,omitempty"`
	Port            int    `json:"port" binding:"required,min=1,max=65535"`
	HealthCheckURL  string `json:"health_check_url,omitempty"`
	JvmArgs         string `json:"jvm_args,omitempty"`
	EnvVars         string `json:"env_vars,omitempty"`
	SystemdUser     string `json:"systemd_user,omitempty"`
	JavaPath        string `json:"java_path,omitempty"`
	StartupOrder    int    `json:"startup_order,omitempty"`
	Optional        *bool  `json:"optional,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
	// 分组切流配置已下线：字段保留为 API 兼容，写入时后端会清空。
	NginxHostID       uint   `json:"nginx_host_id,omitempty"`
	NginxUpstreamName string `json:"nginx_upstream_name,omitempty"`
	ActiveGroup       string `json:"active_group,omitempty"`
	// DeployMode 部署模式（Sprint X.10/X.11）：systemd / nohup / docker；空 → 回退 Application.DeployMode → systemd
	DeployMode string `json:"deploy_mode,omitempty"`

	// Sprint X.11：Docker 配置（空时回退 Application 对应字段）。
	DockerRegistry       string `json:"docker_registry,omitempty"`
	DockerImageName      string `json:"docker_image_name,omitempty"`
	DockerImageTag       string `json:"docker_image_tag,omitempty"`
	DockerfileTemplateID uint   `json:"dockerfile_template_id,omitempty"`
	Dockerfile           string `json:"dockerfile,omitempty"`
	DockerBuildArgs      string `json:"docker_build_args,omitempty"`
	DockerContainerName  string `json:"docker_container_name,omitempty"`
	DockerRunArgs        string `json:"docker_run_args,omitempty"`
}

type AppServiceDiscoverInput struct {
	GitRef string `json:"git_ref,omitempty"`
	CredID uint   `json:"cred_id,omitempty"`
}

type AppServiceSuggestion struct {
	ServiceCode     string `json:"service_code"`
	Name            string `json:"name"`
	BuildModule     string `json:"build_module"`
	BuildJarPattern string `json:"build_jar_pattern"`
	Port            int    `json:"port"`
	HealthCheckURL  string `json:"health_check_url"`
	StartupOrder    int    `json:"startup_order"`
	Optional        bool   `json:"optional"`
	Enabled         bool   `json:"enabled"`
	Recommended     bool   `json:"recommended"`
	Confidence      string `json:"confidence"`
	Reason          string `json:"reason"`
	ArtifactID      string `json:"artifact_id"`
	Packaging       string `json:"packaging"`
	Existing        bool   `json:"existing"`
}

type AppServiceBatchImportInput struct {
	Items          []AppServiceInput `json:"items" binding:"required"`
	Upsert         bool              `json:"upsert"`
	DisableDefault bool              `json:"disable_default"`
}

type AppServiceBatchImportView struct {
	Items           []AppServiceView `json:"items"`
	Created         int              `json:"created"`
	Updated         int              `json:"updated"`
	Skipped         int              `json:"skipped"`
	DisabledDefault bool             `json:"disabled_default"`
}

// AppServiceView AppService 响应。
type AppServiceView struct {
	ID                   uint   `json:"id"`
	AppID                uint   `json:"app_id"`
	ServiceCode          string `json:"service_code"`
	Name                 string `json:"name"`
	BuildModule          string `json:"build_module"`
	BuildJarPattern      string `json:"build_jar_pattern"`
	Port                 int    `json:"port"`
	HealthCheckURL       string `json:"health_check_url"`
	JvmArgs              string `json:"jvm_args"`
	EnvVars              string `json:"env_vars"`
	SystemdUser          string `json:"systemd_user"`
	JavaPath             string `json:"java_path"`
	StartupOrder         int    `json:"startup_order"`
	Optional             bool   `json:"optional"`
	Enabled              bool   `json:"enabled"`
	NginxHostID          uint   `json:"nginx_host_id"`
	NginxUpstreamName    string `json:"nginx_upstream_name"`
	ActiveGroup          string `json:"active_group"`
	DeployMode           string `json:"deploy_mode"`
	DockerRegistry       string `json:"docker_registry"`
	DockerImageName      string `json:"docker_image_name"`
	DockerImageTag       string `json:"docker_image_tag"`
	DockerfileTemplateID uint   `json:"dockerfile_template_id"`
	Dockerfile           string `json:"dockerfile"`
	DockerBuildArgs      string `json:"docker_build_args"`
	DockerContainerName  string `json:"docker_container_name"`
	DockerRunArgs        string `json:"docker_run_args"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

// AppServiceService AppService 的 CRUD service。
type AppServiceService struct {
	db        *gorm.DB
	credSvc   *GitCredentialService
	envSvc    *BuilderEnvService
	workspace string
}

type AppServiceServiceOption func(*AppServiceService)

func WithAppServiceDiscovery(credSvc *GitCredentialService, envSvc *BuilderEnvService, workspace string) AppServiceServiceOption {
	return func(s *AppServiceService) {
		s.credSvc = credSvc
		s.envSvc = envSvc
		s.workspace = workspace
	}
}

func NewAppServiceService(db *gorm.DB, opts ...AppServiceServiceOption) *AppServiceService {
	s := &AppServiceService{db: db}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// normalizeAppManagedRuntimeFields 清理 service 级运行时字段。
//
// 这些字段由 Application 统一管理，避免多 service 场景下一次运行配置调整要逐个 service 修改。
// AppService 只保留 service 身份、构建定位、端口、启动顺序和 Dockerfile 模板选择。
func normalizeAppManagedRuntimeFields(in *AppServiceInput) {
	in.HealthCheckURL = ""
	in.JvmArgs = ""
	in.EnvVars = ""
	in.DeployMode = ""
	in.NginxHostID = 0
	in.NginxUpstreamName = ""
	in.ActiveGroup = ""
	in.DockerRegistry = ""
	in.DockerImageName = ""
	in.DockerImageTag = ""
	in.Dockerfile = ""
	in.DockerBuildArgs = ""
	in.DockerContainerName = ""
	in.DockerRunArgs = ""
}

// serviceCodeRE 与 app_code 同样的命名规范。
var serviceCodeRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,49}$`)

func toAppServiceView(s *model.AppService) AppServiceView {
	return AppServiceView{
		ID: s.ID, AppID: s.AppID, ServiceCode: s.ServiceCode, Name: s.Name,
		BuildModule: s.BuildModule, BuildJarPattern: s.BuildJarPattern,
		Port: s.Port, HealthCheckURL: s.HealthCheckURL,
		JvmArgs: s.JvmArgs, EnvVars: s.EnvVars,
		SystemdUser: s.SystemdUser, JavaPath: s.JavaPath,
		StartupOrder: s.StartupOrder, Optional: s.Optional, Enabled: s.Enabled,
		NginxHostID: s.NginxHostID, NginxUpstreamName: s.NginxUpstreamName, ActiveGroup: s.ActiveGroup,
		DeployMode:           s.DeployMode,
		DockerRegistry:       s.DockerRegistry,
		DockerImageName:      s.DockerImageName,
		DockerImageTag:       s.DockerImageTag,
		DockerfileTemplateID: s.DockerfileTemplateID,
		Dockerfile:           s.Dockerfile,
		DockerBuildArgs:      s.DockerBuildArgs,
		DockerContainerName:  s.DockerContainerName,
		DockerRunArgs:        s.DockerRunArgs,
		CreatedAt:            s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:            s.UpdatedAt.Format(time.RFC3339),
	}
}

// Create 在指定 app 下新建一个 AppService。
func (s *AppServiceService) Create(appID uint, in AppServiceInput) (AppServiceView, error) {
	code := strings.TrimSpace(in.ServiceCode)
	if !serviceCodeRE.MatchString(code) {
		return AppServiceView{}, apperr.New("BAD_REQUEST",
			"service_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
	}
	normalizeAppManagedRuntimeFields(&in)
	if err := deploy.ValidateDeployMode(in.DeployMode); err != nil {
		return AppServiceView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	if err := validateDockerContainerNameTemplate(in.DockerContainerName); err != nil {
		return AppServiceView{}, err
	}
	// app 存在
	if err := s.db.First(&model.Application{}, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AppServiceView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return AppServiceView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	if err := s.validateDockerfileTemplate(appID, in.DockerfileTemplateID); err != nil {
		return AppServiceView{}, err
	}
	opt := boolDeref(in.Optional, false)
	en := boolDeref(in.Enabled, true)
	row := &model.AppService{
		AppID: appID, ServiceCode: code, Name: strings.TrimSpace(in.Name),
		BuildModule:     strings.TrimSpace(in.BuildModule),
		BuildJarPattern: strings.TrimSpace(in.BuildJarPattern),
		Port:            in.Port,
		HealthCheckURL:  strings.TrimSpace(in.HealthCheckURL),
		JvmArgs:         in.JvmArgs,
		EnvVars:         in.EnvVars,
		SystemdUser:     strings.TrimSpace(in.SystemdUser),
		JavaPath:        strings.TrimSpace(in.JavaPath),
		StartupOrder:    in.StartupOrder,
		Optional:        opt, Enabled: en,
		NginxHostID:          in.NginxHostID,
		NginxUpstreamName:    strings.TrimSpace(in.NginxUpstreamName),
		ActiveGroup:          strings.TrimSpace(in.ActiveGroup),
		DeployMode:           strings.TrimSpace(in.DeployMode),
		DockerRegistry:       strings.TrimSpace(in.DockerRegistry),
		DockerImageName:      strings.TrimSpace(in.DockerImageName),
		DockerImageTag:       strings.TrimSpace(in.DockerImageTag),
		DockerfileTemplateID: in.DockerfileTemplateID,
		Dockerfile:           in.Dockerfile,
		DockerBuildArgs:      strings.TrimSpace(in.DockerBuildArgs),
		DockerContainerName:  strings.TrimSpace(in.DockerContainerName),
		DockerRunArgs:        strings.TrimSpace(in.DockerRunArgs),
	}
	if row.StartupOrder == 0 {
		row.StartupOrder = 100
	}
	if err := s.db.Create(row).Error; err != nil {
		if isUniqueConstraintErr(err) {
			return AppServiceView{}, apperr.New("CONFLICT",
				fmt.Sprintf("service_code=%s 在应用 %d 下已存在", code, appID), 409)
		}
		return AppServiceView{}, apperr.Wrap(err, "INTERNAL", "create app_service", 500)
	}
	return toAppServiceView(row), nil
}

// List 列出某 app 的所有 service（按 startup_order ASC）。
// 注意：这里不再自动补 default service；空列表就是空列表。
func (s *AppServiceService) List(appID uint) ([]AppServiceView, error) {
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	var rs []model.AppService
	if err := s.db.Where("app_id = ?", appID).
		Order("startup_order ASC, id ASC").Find(&rs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list app_services", 500)
	}
	out := make([]AppServiceView, len(rs))
	for i := range rs {
		out[i] = toAppServiceView(&rs[i])
	}
	return out, nil
}

func (s *AppServiceService) Discover(ctx context.Context, appID uint, in AppServiceDiscoverInput) ([]AppServiceSuggestion, error) {
	if strings.TrimSpace(s.workspace) == "" {
		return nil, apperr.New("INTERNAL", "storage.build_workspace 未配置，无法扫描 Maven 项目", 500)
	}
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	if strings.TrimSpace(app.GitURL) == "" {
		return nil, apperr.New("BAD_REQUEST", "应用未配置 Git 仓库地址，无法扫描 Maven 模块", 400)
	}

	credID := in.CredID
	if credID == 0 {
		credID = parseGitCredID(app.GitCredID)
	}
	cred, err := s.loadBuilderCredential(credID)
	if err != nil {
		return nil, err
	}
	gitBin, execEnv, err := s.resolveGitRuntime()
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(in.GitRef)
	if ref == "" {
		ref = defaultRef(app.GitRef)
	}
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "mkdir build workspace", 500)
	}
	target := filepath.Join(s.workspace, fmt.Sprintf("discover-%s-%d", app.AppCode, time.Now().UnixNano()))
	defer os.RemoveAll(target)

	if _, err := builder.Clone(ctx, builder.CloneOptions{
		URL:       app.GitURL,
		GitBin:    gitBin,
		Ref:       ref,
		TargetDir: target,
		Cred:      cred,
		LogWriter: io.Discard,
		Timeout:   2 * time.Minute,
		ExecEnv:   execEnv,
	}); err != nil {
		return nil, apperr.Wrap(err, "BAD_REQUEST", "扫描前拉取 Git 仓库失败: "+err.Error(), 400)
	}

	raw, err := builder.DiscoverMavenServices(target, app.Port)
	if err != nil {
		return nil, apperr.Wrap(err, "BAD_REQUEST", "扫描 Maven 项目失败: "+err.Error(), 400)
	}
	existing, err := s.existingServices(appID)
	if err != nil {
		return nil, err
	}
	out := make([]AppServiceSuggestion, 0, len(raw))
	for _, r := range raw {
		ex, exists := existing[r.ServiceCode]
		name := r.Name
		buildModule := r.BuildModule
		buildJarPattern := r.BuildJarPattern
		port := r.Port
		health := r.HealthCheckURL
		startupOrder := r.StartupOrder
		optional := false
		enabled := true
		reason := r.Reason
		if exists {
			if strings.TrimSpace(ex.Name) != "" {
				name = ex.Name
			}
			buildModule = ex.BuildModule
			buildJarPattern = ex.BuildJarPattern
			port = ex.Port
			health = ex.HealthCheckURL
			startupOrder = ex.StartupOrder
			optional = ex.Optional
			enabled = ex.Enabled
			reason += "；已存在，已保留当前配置"
		}
		out = append(out, AppServiceSuggestion{
			ServiceCode:     r.ServiceCode,
			Name:            name,
			BuildModule:     buildModule,
			BuildJarPattern: buildJarPattern,
			Port:            port,
			HealthCheckURL:  health,
			StartupOrder:    startupOrder,
			Optional:        optional,
			Enabled:         enabled,
			Recommended:     r.Recommended,
			Confidence:      r.Confidence,
			Reason:          reason,
			ArtifactID:      r.ArtifactID,
			Packaging:       r.Packaging,
			Existing:        exists,
		})
	}
	return out, nil
}

func (s *AppServiceService) BatchImport(appID uint, in AppServiceBatchImportInput) (AppServiceBatchImportView, error) {
	if len(in.Items) == 0 {
		return AppServiceBatchImportView{}, apperr.New("BAD_REQUEST", "至少选择一个 service", 400)
	}
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AppServiceBatchImportView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return AppServiceBatchImportView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	var existing []model.AppService
	if err := s.db.Where("app_id = ?", appID).Find(&existing).Error; err != nil {
		return AppServiceBatchImportView{}, apperr.Wrap(err, "INTERNAL", "list app_services", 500)
	}
	byCode := make(map[string]model.AppService, len(existing))
	for _, row := range existing {
		byCode[row.ServiceCode] = row
	}

	view := AppServiceBatchImportView{}
	importedNonDefault := false
	for _, item := range in.Items {
		item.ServiceCode = strings.TrimSpace(item.ServiceCode)
		if item.ServiceCode == "" {
			view.Skipped++
			continue
		}
		if item.Port == 0 {
			item.Port = app.Port
		}
		// 运行时配置由 Application 统一管理；扫描导入只写 service 自身差异项。
		normalizeAppManagedRuntimeFields(&item)
		if !isDefaultServiceCodeForImport(item.ServiceCode) {
			importedNonDefault = true
		}
		if row, ok := byCode[item.ServiceCode]; ok {
			if !in.Upsert {
				view.Skipped++
				continue
			}
			out, err := s.Update(row.ID, item)
			if err != nil {
				return AppServiceBatchImportView{}, err
			}
			view.Items = append(view.Items, out)
			view.Updated++
			continue
		}
		out, err := s.Create(appID, item)
		if err != nil {
			return AppServiceBatchImportView{}, err
		}
		view.Items = append(view.Items, out)
		view.Created++
	}
	if in.DisableDefault && importedNonDefault {
		res := s.db.Model(&model.AppService{}).
			Where("app_id = ? AND service_code = ? AND enabled = ?", appID, "default", true).
			Update("enabled", false)
		if res.Error != nil {
			return AppServiceBatchImportView{}, apperr.Wrap(res.Error, "INTERNAL", "disable default service", 500)
		}
		view.DisabledDefault = res.RowsAffected > 0
	}
	return view, nil
}

// Get 单条。
func (s *AppServiceService) Get(id uint) (AppServiceView, error) {
	r, err := s.findByID(id)
	if err != nil {
		return AppServiceView{}, err
	}
	return toAppServiceView(r), nil
}

// Update 全字段更新（除了 service_code，service_code 不可改，避免破坏部署历史）。
func (s *AppServiceService) Update(id uint, in AppServiceInput) (AppServiceView, error) {
	r, err := s.findByID(id)
	if err != nil {
		return AppServiceView{}, err
	}
	normalizeAppManagedRuntimeFields(&in)
	if err := deploy.ValidateDeployMode(in.DeployMode); err != nil {
		return AppServiceView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	if err := validateDockerContainerNameTemplate(in.DockerContainerName); err != nil {
		return AppServiceView{}, err
	}
	if err := s.validateDockerfileTemplate(r.AppID, in.DockerfileTemplateID); err != nil {
		return AppServiceView{}, err
	}
	// service_code 不可改（参考 app_code 也不可改的语义）
	if strings.TrimSpace(in.ServiceCode) != "" && strings.TrimSpace(in.ServiceCode) != r.ServiceCode {
		return AppServiceView{}, apperr.New("BAD_REQUEST",
			"service_code 不可修改（会破坏 systemd unit / 部署历史）", 400)
	}
	r.Name = strings.TrimSpace(in.Name)
	r.BuildModule = strings.TrimSpace(in.BuildModule)
	r.BuildJarPattern = strings.TrimSpace(in.BuildJarPattern)
	r.Port = in.Port
	r.HealthCheckURL = strings.TrimSpace(in.HealthCheckURL)
	r.JvmArgs = in.JvmArgs
	r.EnvVars = in.EnvVars
	r.SystemdUser = strings.TrimSpace(in.SystemdUser)
	r.JavaPath = strings.TrimSpace(in.JavaPath)
	if in.StartupOrder > 0 {
		r.StartupOrder = in.StartupOrder
	}
	if in.Optional != nil {
		r.Optional = *in.Optional
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	r.NginxHostID = in.NginxHostID
	r.NginxUpstreamName = strings.TrimSpace(in.NginxUpstreamName)
	r.ActiveGroup = strings.TrimSpace(in.ActiveGroup)
	r.DeployMode = strings.TrimSpace(in.DeployMode)
	r.DockerRegistry = strings.TrimSpace(in.DockerRegistry)
	r.DockerImageName = strings.TrimSpace(in.DockerImageName)
	r.DockerImageTag = strings.TrimSpace(in.DockerImageTag)
	r.DockerfileTemplateID = in.DockerfileTemplateID
	r.Dockerfile = in.Dockerfile
	r.DockerBuildArgs = strings.TrimSpace(in.DockerBuildArgs)
	r.DockerContainerName = strings.TrimSpace(in.DockerContainerName)
	r.DockerRunArgs = strings.TrimSpace(in.DockerRunArgs)
	if err := s.db.Save(r).Error; err != nil {
		return AppServiceView{}, apperr.Wrap(err, "INTERNAL", "update app_service", 500)
	}
	return toAppServiceView(r), nil
}

func (s *AppServiceService) validateDockerfileTemplate(appID, templateID uint) error {
	if templateID == 0 {
		return nil
	}
	var n int64
	if err := s.db.Model(&model.DockerfileTemplate{}).
		Where("id = ? AND app_id = ?", templateID, appID).Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "validate dockerfile template", 500)
	}
	if n == 0 {
		return apperr.New("BAD_REQUEST", "dockerfile_template_id 不属于当前应用", 400)
	}
	return nil
}

// Delete 软停用而非物理删（避免破坏现有 deployment 引用）。
// 若需要物理删，需先确保 deployments 表里没有 service_code 引用。
func (s *AppServiceService) Delete(id uint) error {
	r, err := s.findByID(id)
	if err != nil {
		return err
	}
	var n int64
	if err := s.db.Model(&model.Deployment{}).
		Where("app_id = ? AND service_code = ?", r.AppID, r.ServiceCode).
		Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT",
			fmt.Sprintf("service 仍被 %d 个部署引用，请先解绑或停用", n), 409)
	}
	res := s.db.Delete(&model.AppService{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete app_service", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// EnsureDefault 保留为兼容入口。
//
// 过去这里会自动创建 service_code="default"；现在产品要求新增应用后
// 服务列表保持为空，因此该方法只检查 app_services 表可访问，不再写入。
func (s *AppServiceService) EnsureDefault(app *model.Application) error {
	return EnsureDefaultAppService(s.db, app)
}

// EnsureDefaultAppService 包级 helper：避免 AppService ↔ AppServiceService 循环依赖。
// 兼容旧调用点，但不再创建 default service。
func EnsureDefaultAppService(db *gorm.DB, app *model.Application) error {
	var n int64
	if err := db.Model(&model.AppService{}).Where("app_id = ?", app.ID).Count(&n).Error; err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return apperr.Wrap(err, "INTERNAL", "count services", 500)
	}
	return nil
}

func (s *AppServiceService) findByID(id uint) (*model.AppService, error) {
	var r model.AppService
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get app_service", 500)
	}
	return &r, nil
}

func boolDeref(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// isUniqueConstraintErr SQLite + GORM 的唯一约束冲突识别。
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: unique")
}

func (s *AppServiceService) loadBuilderCredential(credID uint) (*builder.Credential, error) {
	if credID == 0 {
		return nil, nil
	}
	if s.credSvc == nil {
		return nil, apperr.New("INTERNAL", "Git 凭证服务未初始化，无法扫描私有仓库", 500)
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

func (s *AppServiceService) resolveGitRuntime() (gitBin string, execEnv []string, err error) {
	gitBin = "/usr/bin/git"
	if s.envSvc != nil {
		env, loadErr := s.envSvc.Load()
		if loadErr == nil && strings.TrimSpace(env.GitPath) != "" {
			gitBin = strings.TrimSpace(env.GitPath)
		}
		execEnv, _ = s.envSvc.BuildExecEnv()
	}
	if !fileExecutable(gitBin) {
		return "", nil, apperr.New("BAD_REQUEST",
			fmt.Sprintf("git 不可执行：%s；请去「⚙ 构建环境」填 git 绝对路径", gitBin), 400)
	}
	return gitBin, execEnv, nil
}

func (s *AppServiceService) existingServices(appID uint) (map[string]model.AppService, error) {
	var rows []model.AppService
	if err := s.db.Where("app_id = ?", appID).Find(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list existing app_services", 500)
	}
	out := make(map[string]model.AppService, len(rows))
	for _, row := range rows {
		out[row.ServiceCode] = row
	}
	return out, nil
}

func parseGitCredID(raw string) uint {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0
	}
	return uint(n)
}

func isDefaultServiceCodeForImport(code string) bool {
	code = strings.TrimSpace(code)
	return code == "" || code == "default"
}
