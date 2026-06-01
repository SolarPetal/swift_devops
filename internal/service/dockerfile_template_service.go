package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/builder"
	apperr "swift-devops/internal/pkg/errors"
)

// DockerfileTemplateInput 创建/更新 Dockerfile 模板。
type DockerfileTemplateInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content" binding:"required"`
	IsDefault   bool   `json:"is_default,omitempty"`
}

// DockerfileTemplateView 响应视图。
type DockerfileTemplateView struct {
	ID          uint   `json:"id"`
	AppID       uint   `json:"app_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	IsDefault   bool   `json:"is_default"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type DockerfileTemplateService struct{ db *gorm.DB }

func NewDockerfileTemplateService(db *gorm.DB) *DockerfileTemplateService {
	return &DockerfileTemplateService{db: db}
}

func toDockerfileTemplateView(t *model.DockerfileTemplate) DockerfileTemplateView {
	return DockerfileTemplateView{
		ID: t.ID, AppID: t.AppID, Name: t.Name, Description: t.Description,
		Content: t.Content, IsDefault: t.IsDefault,
		CreatedAt: t.CreatedAt.Format(time.RFC3339), UpdatedAt: t.UpdatedAt.Format(time.RFC3339),
	}
}

func (s *DockerfileTemplateService) List(appID uint) ([]DockerfileTemplateView, error) {
	if err := s.ensureApp(appID); err != nil {
		return nil, err
	}
	if _, err := EnsureDefaultDockerfileTemplate(s.db, appID); err != nil {
		return nil, err
	}
	var rows []model.DockerfileTemplate
	if err := s.db.Where("app_id = ?", appID).Order("is_default DESC, id ASC").Find(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list dockerfile templates", 500)
	}
	out := make([]DockerfileTemplateView, len(rows))
	for i := range rows {
		out[i] = toDockerfileTemplateView(&rows[i])
	}
	return out, nil
}

func (s *DockerfileTemplateService) Get(id uint) (DockerfileTemplateView, error) {
	t, err := s.find(id)
	if err != nil {
		return DockerfileTemplateView{}, err
	}
	return toDockerfileTemplateView(t), nil
}

func (s *DockerfileTemplateService) Create(appID uint, in DockerfileTemplateInput) (DockerfileTemplateView, error) {
	if err := s.ensureApp(appID); err != nil {
		return DockerfileTemplateView{}, err
	}
	if err := validateDockerfileTemplateInput(in); err != nil {
		return DockerfileTemplateView{}, err
	}
	var n int64
	if err := s.db.Model(&model.DockerfileTemplate{}).Where("app_id = ?", appID).Count(&n).Error; err != nil {
		return DockerfileTemplateView{}, apperr.Wrap(err, "INTERNAL", "count dockerfile templates", 500)
	}
	row := &model.DockerfileTemplate{
		AppID: appID, Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description),
		Content: in.Content, IsDefault: in.IsDefault || n == 0,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if row.IsDefault {
			if err := tx.Model(&model.DockerfileTemplate{}).Where("app_id = ?", appID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(row).Error
	})
	if err != nil {
		return DockerfileTemplateView{}, apperr.Wrap(err, "INTERNAL", "create dockerfile template", 500)
	}
	return toDockerfileTemplateView(row), nil
}

func (s *DockerfileTemplateService) Update(id uint, in DockerfileTemplateInput) (DockerfileTemplateView, error) {
	if err := validateDockerfileTemplateInput(in); err != nil {
		return DockerfileTemplateView{}, err
	}
	row, err := s.find(id)
	if err != nil {
		return DockerfileTemplateView{}, err
	}
	row.Name = strings.TrimSpace(in.Name)
	row.Description = strings.TrimSpace(in.Description)
	row.Content = in.Content
	row.IsDefault = in.IsDefault
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if row.IsDefault {
			if err := tx.Model(&model.DockerfileTemplate{}).Where("app_id = ? AND id <> ?", row.AppID, row.ID).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(row).Error
	})
	if err != nil {
		return DockerfileTemplateView{}, apperr.Wrap(err, "INTERNAL", "update dockerfile template", 500)
	}
	return toDockerfileTemplateView(row), nil
}

func (s *DockerfileTemplateService) Delete(id uint) error {
	row, err := s.find(id)
	if err != nil {
		return err
	}
	var n int64
	if err := s.db.Model(&model.AppService{}).Where("dockerfile_template_id = ?", id).Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count dockerfile template refs", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT", fmt.Sprintf("模板仍被 %d 个 service 绑定，请先切换模板", n), 409)
	}
	res := s.db.Delete(&model.DockerfileTemplate{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete dockerfile template", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	if row.IsDefault {
		var next model.DockerfileTemplate
		if err := s.db.Where("app_id = ?", row.AppID).Order("id ASC").First(&next).Error; err == nil {
			_ = s.db.Model(&next).Update("is_default", true).Error
		}
	}
	return nil
}

func (s *DockerfileTemplateService) CreateDefault(appID uint) (DockerfileTemplateView, error) {
	if err := s.ensureApp(appID); err != nil {
		return DockerfileTemplateView{}, err
	}
	row, err := EnsureDefaultDockerfileTemplate(s.db, appID)
	if err != nil {
		return DockerfileTemplateView{}, err
	}
	return toDockerfileTemplateView(row), nil
}

func (s *DockerfileTemplateService) ensureApp(appID uint) error {
	if err := s.db.First(&model.Application{}, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	return nil
}

func (s *DockerfileTemplateService) find(id uint) (*model.DockerfileTemplate, error) {
	var row model.DockerfileTemplate
	if err := s.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get dockerfile template", 500)
	}
	return &row, nil
}

func validateDockerfileTemplateInput(in DockerfileTemplateInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return apperr.New("BAD_REQUEST", "模板名称必填", 400)
	}
	if strings.TrimSpace(in.Content) == "" {
		return apperr.New("BAD_REQUEST", "Dockerfile 内容必填", 400)
	}
	if !strings.Contains(strings.ToUpper(in.Content), "FROM ") {
		return apperr.New("BAD_REQUEST", "Dockerfile 内容至少需要包含 FROM 指令", 400)
	}
	return nil
}

// EnsureDefaultDockerfileTemplate 确保 app 至少有一个默认模板。
func EnsureDefaultDockerfileTemplate(db *gorm.DB, appID uint) (*model.DockerfileTemplate, error) {
	var row model.DockerfileTemplate
	if err := db.Where("app_id = ? AND is_default = ?", appID, true).Order("id ASC").First(&row).Error; err == nil {
		return &row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.Wrap(err, "INTERNAL", "find default dockerfile template", 500)
	}
	if err := db.Where("app_id = ?", appID).Order("id ASC").First(&row).Error; err == nil {
		if !row.IsDefault {
			_ = db.Model(&row).Update("is_default", true).Error
			row.IsDefault = true
		}
		return &row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.Wrap(err, "INTERNAL", "find first dockerfile template", 500)
	}
	row = model.DockerfileTemplate{AppID: appID, Name: "default-jdk21", Description: "默认 Java 21 JDK 镜像模板", Content: builder.DefaultDockerfileTemplate(), IsDefault: true}
	if err := db.Create(&row).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "create default dockerfile template", 500)
	}
	return &row, nil
}
