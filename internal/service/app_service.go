package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
)

// AppInput 应用创建/更新入参
type AppInput struct {
	AppCode    string   `json:"app_code" binding:"required"`
	Name       string   `json:"name" binding:"required"`
	AppType    string   `json:"app_type,omitempty"`
	GitURL     string   `json:"git_url,omitempty"`
	GitCredID  string   `json:"git_cred_id,omitempty"`
	GitRef     string   `json:"git_ref,omitempty"`
	GitRefs    []string `json:"git_refs,omitempty"`
	DeployPath string   `json:"deploy_path" binding:"required"`
	// Port 是应用级默认字段；UI 不再维护，service 端口由 AppService 承接。
	// 创建不传时用 defaultAppPort 兜底，仅作为应用级兼容字段保留。
	Port           int    `json:"port" binding:"omitempty,min=1,max=65535"`
	HealthCheckURL string `json:"health_check_url,omitempty"`
	JvmArgs        string `json:"jvm_args,omitempty"`
	EnvVars        string `json:"env_vars,omitempty"`     // JSON 字符串，如 {"SPRING_PROFILES_ACTIVE":"prod"}
	SystemdUser    string `json:"systemd_user,omitempty"` // 留空 = 用启动 sshd 的账号（一般 root）
	// JavaPath 应用级 java 可执行路径覆盖（可选）。空 = 沿用主机 Host.JavaPath。
	// 适用：同主机跑多 JDK 版本（jdk8 / jdk17）。
	JavaPath string `json:"java_path,omitempty"`
	// 构建参数（Sprint 5.4.7）：multi-module 项目专用
	BuildModule     string `json:"build_module,omitempty"`      // mvn -pl 用，如 "car-dealer-admin"
	BuildJarPattern string `json:"build_jar_pattern,omitempty"` // glob 选 jar，如 "car-dealer-admin/target/*.jar"
	// 分组切流配置已下线：字段保留为 API 兼容，写入时后端会清空。
	NginxHostID       uint   `json:"nginx_host_id,omitempty"`
	NginxUpstreamName string `json:"nginx_upstream_name,omitempty"`
	ActiveGroup       string `json:"active_group,omitempty"`
	// DeployMode 部署模式（Sprint X.10/X.11）：systemd / nohup / docker。空 = systemd。
	DeployMode string `json:"deploy_mode,omitempty"`

	// Sprint X.11：Docker 镜像构建 / 部署配置。
	BuildMode           string `json:"build_mode,omitempty"` // local-jar / local-docker / remote-docker
	DockerRegistry      string `json:"docker_registry,omitempty"`
	DockerImageName     string `json:"docker_image_name,omitempty"`
	DockerImageTag      string `json:"docker_image_tag,omitempty"`
	Dockerfile          string `json:"dockerfile,omitempty"`
	DockerBuildArgs     string `json:"docker_build_args,omitempty"`
	DockerContainerName string `json:"docker_container_name,omitempty"`
	DockerRunArgs       string `json:"docker_run_args,omitempty"`
}

// AppView 应用响应
type AppView struct {
	ID                  uint     `json:"id"`
	AppCode             string   `json:"app_code"`
	Name                string   `json:"name"`
	AppType             string   `json:"app_type"`
	GitURL              string   `json:"git_url"`
	GitCredID           string   `json:"git_cred_id"`
	GitRef              string   `json:"git_ref"`
	GitRefs             []string `json:"git_refs"`
	DeployPath          string   `json:"deploy_path"`
	Port                int      `json:"port"`
	HealthCheckURL      string   `json:"health_check_url"`
	JvmArgs             string   `json:"jvm_args"`
	EnvVars             string   `json:"env_vars"`
	SystemdUser         string   `json:"systemd_user"`
	JavaPath            string   `json:"java_path"`
	BuildModule         string   `json:"build_module"`
	BuildJarPattern     string   `json:"build_jar_pattern"`
	NginxHostID         uint     `json:"nginx_host_id"`
	NginxUpstreamName   string   `json:"nginx_upstream_name"`
	ActiveGroup         string   `json:"active_group"`
	DeployMode          string   `json:"deploy_mode"`
	BuildMode           string   `json:"build_mode"`
	DockerRegistry      string   `json:"docker_registry"`
	DockerImageName     string   `json:"docker_image_name"`
	DockerImageTag      string   `json:"docker_image_tag"`
	Dockerfile          string   `json:"dockerfile"`
	DockerBuildArgs     string   `json:"docker_build_args"`
	DockerContainerName string   `json:"docker_container_name"`
	DockerRunArgs       string   `json:"docker_run_args"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

func toAppView(a *model.Application) AppView {
	refs := normalizeGitRefs(a.GitRef, parseGitRefs(a.GitRefs))
	return AppView{
		ID: a.ID, AppCode: a.AppCode, Name: a.Name,
		AppType: a.AppType, GitURL: a.GitURL, GitCredID: a.GitCredID,
		GitRef:     refs[0],
		GitRefs:    refs,
		DeployPath: a.DeployPath, Port: a.Port,
		HealthCheckURL: a.HealthCheckURL, JvmArgs: a.JvmArgs, EnvVars: a.EnvVars,
		SystemdUser:         a.SystemdUser,
		JavaPath:            a.JavaPath,
		BuildModule:         a.BuildModule,
		BuildJarPattern:     a.BuildJarPattern,
		NginxHostID:         a.NginxHostID,
		NginxUpstreamName:   a.NginxUpstreamName,
		ActiveGroup:         a.ActiveGroup,
		DeployMode:          a.DeployMode,
		BuildMode:           normalizeBuildMode(a.BuildMode),
		DockerRegistry:      a.DockerRegistry,
		DockerImageName:     a.DockerImageName,
		DockerImageTag:      a.DockerImageTag,
		Dockerfile:          a.Dockerfile,
		DockerBuildArgs:     a.DockerBuildArgs,
		DockerContainerName: a.DockerContainerName,
		DockerRunArgs:       a.DockerRunArgs,
		CreatedAt:           a.CreatedAt.Format(time.RFC3339),
		UpdatedAt:           a.UpdatedAt.Format(time.RFC3339),
	}
}

// appCodeRE 限制 app_code 命名风格：小写字母开头，2-50 位，仅含小写字母/数字/连字符。
// 这是后续 systemd 单元名（devops-<app_code>.service）的安全约束。
var appCodeRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,49}$`)

// systemdUserRE 限制 systemd User= 值：POSIX 用户名风格（小写字母/下划线开头，
// 含小写字母/数字/下划线/连字符，长度 1-32）。够覆盖 java、nobody、deployer 这些常见用户。
// 不接 UID 数字，强制走可读名，省得 unit 里出现魔法数字。
var systemdUserRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func validateDockerContainerNameTemplate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	rendered := deploy.RenderDockerRuntimeNameTemplate(raw, "app", "service")
	if err := deploy.ValidateDockerContainerName(rendered); err != nil {
		return apperr.New("BAD_REQUEST",
			"docker_container_name 支持字母/数字/点/下划线/连字符，可用 {{APP_CODE}} / {{SERVICE_CODE}} / {app} / {service} 模板；渲染后 "+err.Error(), 400)
	}
	return nil
}

// AppService 应用业务编排
type AppService struct {
	db *gorm.DB
}

func NewAppService(db *gorm.DB) *AppService {
	return &AppService{db: db}
}

// Create 新建应用
func (s *AppService) Create(in AppInput) (AppView, error) {
	if err := validateAppInput(in); err != nil {
		return AppView{}, err
	}
	refs := normalizeGitRefs(in.GitRef, in.GitRefs)
	a := &model.Application{
		AppCode: in.AppCode, Name: in.Name, AppType: defaultAppType(in.AppType),
		GitURL: in.GitURL, GitCredID: in.GitCredID, GitRef: refs[0], GitRefs: marshalGitRefs(refs),
		DeployPath: in.DeployPath, Port: defaultAppPort(in.Port),
		HealthCheckURL: defaultHealthURL(in.HealthCheckURL),
		JvmArgs:        in.JvmArgs, EnvVars: in.EnvVars,
		SystemdUser:         strings.TrimSpace(in.SystemdUser),
		JavaPath:            strings.TrimSpace(in.JavaPath),
		BuildModule:         strings.TrimSpace(in.BuildModule),
		BuildJarPattern:     strings.TrimSpace(in.BuildJarPattern),
		NginxHostID:         0,
		NginxUpstreamName:   "",
		ActiveGroup:         "",
		DeployMode:          deploy.NormalizeDeployMode(in.DeployMode),
		BuildMode:           normalizeBuildMode(in.BuildMode),
		DockerRegistry:      strings.TrimSpace(in.DockerRegistry),
		DockerImageName:     strings.TrimSpace(in.DockerImageName),
		DockerImageTag:      strings.TrimSpace(in.DockerImageTag),
		Dockerfile:          in.Dockerfile,
		DockerBuildArgs:     strings.TrimSpace(in.DockerBuildArgs),
		DockerContainerName: strings.TrimSpace(in.DockerContainerName),
		DockerRunArgs:       strings.TrimSpace(in.DockerRunArgs),
	}
	if err := s.db.Create(a).Error; err != nil {
		if isUniqueConstraint(err) {
			return AppView{}, apperr.New("CONFLICT",
				fmt.Sprintf("app_code %q 已存在", in.AppCode), 409)
		}
		return AppView{}, apperr.Wrap(err, "INTERNAL", "create app", 500)
	}
	if _, err := EnsureDefaultDockerfileTemplate(s.db, a.ID); err != nil {
		slog.Warn("ensure default dockerfile template after app create", "app_id", a.ID, "err", err)
	}
	return toAppView(a), nil
}

// List 列出全部应用
func (s *AppService) List() ([]AppView, error) {
	var apps []model.Application
	if err := s.db.Order("id DESC").Find(&apps).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list apps", 500)
	}
	vs := make([]AppView, len(apps))
	for i := range apps {
		vs[i] = toAppView(&apps[i])
	}
	return vs, nil
}

// Get 取一个应用
func (s *AppService) Get(id uint) (AppView, error) {
	a, err := s.findByID(id)
	if err != nil {
		return AppView{}, err
	}
	return toAppView(a), nil
}

// Update 更新应用
func (s *AppService) Update(id uint, in AppInput) (AppView, error) {
	if err := validateAppInput(in); err != nil {
		return AppView{}, err
	}
	a, err := s.findByID(id)
	if err != nil {
		return AppView{}, err
	}
	a.AppCode = in.AppCode
	a.Name = in.Name
	a.AppType = defaultAppType(in.AppType)
	a.GitURL = in.GitURL
	a.GitCredID = in.GitCredID
	refs := normalizeGitRefs(in.GitRef, in.GitRefs)
	a.GitRef = refs[0]
	a.GitRefs = marshalGitRefs(refs)
	a.DeployPath = in.DeployPath
	if in.Port > 0 {
		a.Port = in.Port
	}
	a.HealthCheckURL = defaultHealthURL(in.HealthCheckURL)
	a.JvmArgs = in.JvmArgs
	a.EnvVars = in.EnvVars
	a.SystemdUser = strings.TrimSpace(in.SystemdUser)
	a.JavaPath = strings.TrimSpace(in.JavaPath)
	a.BuildModule = strings.TrimSpace(in.BuildModule)
	a.BuildJarPattern = strings.TrimSpace(in.BuildJarPattern)
	a.NginxHostID = 0
	a.NginxUpstreamName = ""
	a.ActiveGroup = ""
	a.DeployMode = deploy.NormalizeDeployMode(in.DeployMode)
	a.BuildMode = normalizeBuildMode(in.BuildMode)
	a.DockerRegistry = strings.TrimSpace(in.DockerRegistry)
	a.DockerImageName = strings.TrimSpace(in.DockerImageName)
	a.DockerImageTag = strings.TrimSpace(in.DockerImageTag)
	a.Dockerfile = in.Dockerfile
	a.DockerBuildArgs = strings.TrimSpace(in.DockerBuildArgs)
	a.DockerContainerName = strings.TrimSpace(in.DockerContainerName)
	a.DockerRunArgs = strings.TrimSpace(in.DockerRunArgs)
	if err := s.db.Save(a).Error; err != nil {
		if isUniqueConstraint(err) {
			return AppView{}, apperr.New("CONFLICT",
				fmt.Sprintf("app_code %q 已被占用", in.AppCode), 409)
		}
		return AppView{}, apperr.Wrap(err, "INTERNAL", "update app", 500)
	}
	return toAppView(a), nil
}

// Delete 删除应用。
// 拒绝级联：若仍有 Deployment 绑定主机，先让用户解绑。
func (s *AppService) Delete(id uint) error {
	var n int64
	if err := s.db.Model(&model.Deployment{}).Where("app_id = ?", id).Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT",
			fmt.Sprintf("应用仍有 %d 个主机绑定，请先解绑", n), 409)
	}
	res := s.db.Delete(&model.Application{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete app", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// --- 内部 ---

func (s *AppService) findByID(id uint) (*model.Application, error) {
	var a model.Application
	if err := s.db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get app", 500)
	}
	return &a, nil
}

func validateAppInput(in AppInput) error {
	if !appCodeRE.MatchString(in.AppCode) {
		return apperr.New("BAD_REQUEST",
			"app_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
	}
	if in.AppType != "" && in.AppType != "jar" && in.AppType != "spring-cloud" && in.AppType != "frontend" {
		return apperr.New("BAD_REQUEST", "app_type 必须为 jar / spring-cloud / frontend", 400)
	}
	if in.EnvVars != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(in.EnvVars), &m); err != nil {
			return apperr.New("BAD_REQUEST", "env_vars 必须是 JSON 对象（key=value 字符串）", 400)
		}
	}
	if u := strings.TrimSpace(in.SystemdUser); u != "" && !systemdUserRE.MatchString(u) {
		return apperr.New("BAD_REQUEST",
			"systemd_user 必须是 POSIX 用户名（小写字母/下划线开头，长度 1-32，仅含小写字母/数字/下划线/连字符）", 400)
	}
	if err := validateDockerContainerNameTemplate(in.DockerContainerName); err != nil {
		return err
	}
	if err := validateJavaPath(in.JavaPath); err != nil {
		return err
	}
	if err := deploy.ValidateDeployMode(in.DeployMode); err != nil {
		return apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	if err := validateBuildMode(in.BuildMode); err != nil {
		return err
	}
	return nil
}

func validateBuildMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "local-jar", "local-docker", "remote-docker":
		return nil
	default:
		return apperr.New("BAD_REQUEST", "build_mode 仅支持 local-jar / local-docker / remote-docker", 400)
	}
}

func normalizeBuildMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return "local-jar"
	}
	return m
}

func defaultAppType(t string) string {
	if t == "" {
		return "jar"
	}
	// app_type 只作为历史兼容字段保留；新模型统一走 AppService。
	if t == "spring-cloud" || t == "frontend" {
		return t
	}
	return "jar"
}

func defaultHealthURL(u string) string {
	if u == "" {
		return "/actuator/health"
	}
	return u
}

func defaultAppPort(port int) int {
	if port <= 0 {
		return 8080
	}
	return port
}

func defaultGitRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "main"
	}
	return ref
}

func parseGitRefs(raw string) []string {
	var refs []string
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	return refs
}

func marshalGitRefs(refs []string) string {
	b, err := json.Marshal(refs)
	if err != nil {
		return `["main"]`
	}
	return string(b)
}

func normalizeGitRefs(primary string, refs []string) []string {
	out := make([]string, 0, len(refs)+1)
	seen := map[string]struct{}{}
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	add(primary)
	for _, ref := range refs {
		add(ref)
	}
	if len(out) == 0 {
		out = append(out, "main")
	}
	return out
}

// isUniqueConstraint 识别 UNIQUE 违反。
// SQLite："UNIQUE constraint failed"
// MySQL："Duplicate entry"
// PostgreSQL："duplicate key value violates unique constraint"
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "duplicate key value")
}
