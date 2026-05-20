package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
)

// AppInput 应用创建/更新入参
type AppInput struct {
	AppCode        string `json:"app_code" binding:"required"`
	Name           string `json:"name" binding:"required"`
	AppType        string `json:"app_type,omitempty"`
	GitURL         string `json:"git_url,omitempty"`
	GitCredID      string `json:"git_cred_id,omitempty"`
	DeployPath     string `json:"deploy_path" binding:"required"`
	Port           int    `json:"port" binding:"required,min=1,max=65535"`
	HealthCheckURL string `json:"health_check_url,omitempty"`
	JvmArgs        string `json:"jvm_args,omitempty"`
	EnvVars        string `json:"env_vars,omitempty"` // JSON 字符串，如 {"SPRING_PROFILES_ACTIVE":"prod"}
}

// AppView 应用响应
type AppView struct {
	ID             uint   `json:"id"`
	AppCode        string `json:"app_code"`
	Name           string `json:"name"`
	AppType        string `json:"app_type"`
	GitURL         string `json:"git_url"`
	GitCredID      string `json:"git_cred_id"`
	DeployPath     string `json:"deploy_path"`
	Port           int    `json:"port"`
	HealthCheckURL string `json:"health_check_url"`
	JvmArgs        string `json:"jvm_args"`
	EnvVars        string `json:"env_vars"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toAppView(a *model.Application) AppView {
	return AppView{
		ID: a.ID, AppCode: a.AppCode, Name: a.Name,
		AppType: a.AppType, GitURL: a.GitURL, GitCredID: a.GitCredID,
		DeployPath: a.DeployPath, Port: a.Port,
		HealthCheckURL: a.HealthCheckURL, JvmArgs: a.JvmArgs, EnvVars: a.EnvVars,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
		UpdatedAt: a.UpdatedAt.Format(time.RFC3339),
	}
}

// appCodeRE 限制 app_code 命名风格：小写字母开头，2-50 位，仅含小写字母/数字/连字符。
// 这是后续 systemd 单元名（devops-<app_code>.service）的安全约束。
var appCodeRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,49}$`)

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
	a := &model.Application{
		AppCode: in.AppCode, Name: in.Name, AppType: defaultAppType(in.AppType),
		GitURL: in.GitURL, GitCredID: in.GitCredID,
		DeployPath: in.DeployPath, Port: in.Port,
		HealthCheckURL: defaultHealthURL(in.HealthCheckURL),
		JvmArgs:        in.JvmArgs, EnvVars: in.EnvVars,
	}
	if err := s.db.Create(a).Error; err != nil {
		if isUniqueConstraint(err) {
			return AppView{}, apperr.New("CONFLICT",
				fmt.Sprintf("app_code %q 已存在", in.AppCode), 409)
		}
		return AppView{}, apperr.Wrap(err, "INTERNAL", "create app", 500)
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
	a.DeployPath = in.DeployPath
	a.Port = in.Port
	a.HealthCheckURL = defaultHealthURL(in.HealthCheckURL)
	a.JvmArgs = in.JvmArgs
	a.EnvVars = in.EnvVars
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
	if in.AppType != "" && in.AppType != "jar" && in.AppType != "spring-cloud" {
		return apperr.New("BAD_REQUEST", "app_type 必须为 jar 或 spring-cloud", 400)
	}
	if in.EnvVars != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(in.EnvVars), &m); err != nil {
			return apperr.New("BAD_REQUEST", "env_vars 必须是 JSON 对象（key=value 字符串）", 400)
		}
	}
	return nil
}

func defaultAppType(t string) string {
	if t == "" {
		return "jar"
	}
	return t
}

func defaultHealthURL(u string) string {
	if u == "" {
		return "/actuator/health"
	}
	return u
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
