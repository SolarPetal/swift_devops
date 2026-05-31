// Package service 中的 AppService（微服务层）CRUD —— Sprint X.4。
//
// AppService 是 Sprint X.1 引入的多 service 模型的最小可操作单元。
// 单体 App 走"自动建 1 行 service_code='default'"兜底，X.3 的部署链
// 此时退化为旧行为（unit/部署路径不带 service 后缀）。
package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
)

// AppServiceInput AppService 创建/更新入参。
type AppServiceInput struct {
	ServiceCode       string `json:"service_code" binding:"required"`
	Name              string `json:"name,omitempty"`
	BuildModule       string `json:"build_module,omitempty"`
	BuildJarPattern   string `json:"build_jar_pattern,omitempty"`
	Port              int    `json:"port" binding:"required,min=1,max=65535"`
	HealthCheckURL    string `json:"health_check_url,omitempty"`
	JvmArgs           string `json:"jvm_args,omitempty"`
	EnvVars           string `json:"env_vars,omitempty"`
	SystemdUser       string `json:"systemd_user,omitempty"`
	JavaPath          string `json:"java_path,omitempty"`
	StartupOrder      int    `json:"startup_order,omitempty"`
	Optional          *bool  `json:"optional,omitempty"`
	Enabled           *bool  `json:"enabled,omitempty"`
	NginxHostID       uint   `json:"nginx_host_id,omitempty"`
	NginxUpstreamName string `json:"nginx_upstream_name,omitempty"`
	ActiveGroup       string `json:"active_group,omitempty"`
	// DeployMode 部署模式（Sprint X.10）：systemd / nohup；空 → 回退 Application.DeployMode → systemd
	DeployMode string `json:"deploy_mode,omitempty"`
}

// AppServiceView AppService 响应。
type AppServiceView struct {
	ID                uint   `json:"id"`
	AppID             uint   `json:"app_id"`
	ServiceCode       string `json:"service_code"`
	Name              string `json:"name"`
	BuildModule       string `json:"build_module"`
	BuildJarPattern   string `json:"build_jar_pattern"`
	Port              int    `json:"port"`
	HealthCheckURL    string `json:"health_check_url"`
	JvmArgs           string `json:"jvm_args"`
	EnvVars           string `json:"env_vars"`
	SystemdUser       string `json:"systemd_user"`
	JavaPath          string `json:"java_path"`
	StartupOrder      int    `json:"startup_order"`
	Optional          bool   `json:"optional"`
	Enabled           bool   `json:"enabled"`
	NginxHostID       uint   `json:"nginx_host_id"`
	NginxUpstreamName string `json:"nginx_upstream_name"`
	ActiveGroup       string `json:"active_group"`
	DeployMode        string `json:"deploy_mode"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// AppServiceService AppService 的 CRUD service。
type AppServiceService struct {
	db *gorm.DB
}

func NewAppServiceService(db *gorm.DB) *AppServiceService {
	return &AppServiceService{db: db}
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
		DeployMode: s.DeployMode,
		CreatedAt:  s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  s.UpdatedAt.Format(time.RFC3339),
	}
}

// Create 在指定 app 下新建一个 AppService。
func (s *AppServiceService) Create(appID uint, in AppServiceInput) (AppServiceView, error) {
	code := strings.TrimSpace(in.ServiceCode)
	if !serviceCodeRE.MatchString(code) {
		return AppServiceView{}, apperr.New("BAD_REQUEST",
			"service_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
	}
	if err := deploy.ValidateDeployMode(in.DeployMode); err != nil {
		return AppServiceView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	// app 存在
	if err := s.db.First(&model.Application{}, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AppServiceView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return AppServiceView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	opt := boolDeref(in.Optional, false)
	en := boolDeref(in.Enabled, true)
	row := &model.AppService{
		AppID: appID, ServiceCode: code, Name: strings.TrimSpace(in.Name),
		BuildModule:     strings.TrimSpace(in.BuildModule),
		BuildJarPattern: strings.TrimSpace(in.BuildJarPattern),
		Port:            in.Port, HealthCheckURL: defaultHealthURL(in.HealthCheckURL),
		JvmArgs: in.JvmArgs, EnvVars: in.EnvVars,
		SystemdUser:  strings.TrimSpace(in.SystemdUser),
		JavaPath:     strings.TrimSpace(in.JavaPath),
		StartupOrder: in.StartupOrder,
		Optional:     opt, Enabled: en,
		NginxHostID:       in.NginxHostID,
		NginxUpstreamName: strings.TrimSpace(in.NginxUpstreamName),
		ActiveGroup:       strings.TrimSpace(in.ActiveGroup),
		DeployMode:        strings.TrimSpace(in.DeployMode),
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
func (s *AppServiceService) List(appID uint) ([]AppServiceView, error) {
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
	if err := deploy.ValidateDeployMode(in.DeployMode); err != nil {
		return AppServiceView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
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
	r.HealthCheckURL = defaultHealthURL(in.HealthCheckURL)
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
	if err := s.db.Save(r).Error; err != nil {
		return AppServiceView{}, apperr.Wrap(err, "INTERNAL", "update app_service", 500)
	}
	return toAppServiceView(r), nil
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

// EnsureDefault 确保 app 至少有一行 AppService。
//
// 用法：Application Create / build / deploy 触发前调用；
// 已有 service 行 → 直接返回；否则用 app 顶层字段（port/health/jvm/...）
// 建一行 service_code="default"，让多 service 链路有 service 可对照。
//
// Sprint X.4：单体 App 自动迁移到"1 个 default service"模型的关键。
func (s *AppServiceService) EnsureDefault(app *model.Application) error {
	return EnsureDefaultAppService(s.db, app)
}

// EnsureDefaultAppService 包级 helper：避免 AppService ↔ AppServiceService 循环依赖。
func EnsureDefaultAppService(db *gorm.DB, app *model.Application) error {
	var n int64
	if err := db.Model(&model.AppService{}).Where("app_id = ?", app.ID).Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count services", 500)
	}
	if n > 0 {
		return nil
	}
	row := &model.AppService{
		AppID: app.ID, ServiceCode: "default", Name: "default",
		BuildModule: app.BuildModule, BuildJarPattern: app.BuildJarPattern,
		Port: app.Port, HealthCheckURL: defaultHealthURL(app.HealthCheckURL),
		JvmArgs: app.JvmArgs, EnvVars: app.EnvVars,
		SystemdUser: app.SystemdUser, JavaPath: app.JavaPath,
		StartupOrder: 100, Optional: false, Enabled: true,
		NginxHostID:       app.NginxHostID,
		NginxUpstreamName: app.NginxUpstreamName,
		ActiveGroup:       app.ActiveGroup,
		DeployMode:        strings.TrimSpace(app.DeployMode), // Sprint X.10：从 app 继承
	}
	if err := db.Create(row).Error; err != nil {
		if isUniqueConstraintErr(err) {
			return nil
		}
		return apperr.Wrap(err, "INTERNAL", "create default service", 500)
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
