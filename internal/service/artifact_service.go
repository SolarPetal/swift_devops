package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
)

// ArtifactInput 注册制品入参。
// Sprint 2.3 真正实现 multipart 上传前，先用"注册已存在本地文件"的方式打通部署链路。
type ArtifactInput struct {
	AppID      uint   `json:"app_id" binding:"required"`
	VersionTag string `json:"version_tag" binding:"required"`
	FilePath   string `json:"file_path" binding:"required"` // 绝对路径
	FileName   string `json:"file_name,omitempty"`          // 留空则取 FilePath basename
}

// ArtifactView 响应视图
type ArtifactView struct {
	ID          uint   `json:"id"`
	AppID       uint   `json:"app_id"`
	VersionTag  string `json:"version_tag"`
	FileName    string `json:"file_name"`
	FilePath    string `json:"file_path"`
	FileMD5     string `json:"file_md5"`
	FileSize    int64  `json:"file_size"`
	BuildStatus string `json:"build_status"`
	CreatedAt   string `json:"created_at"`
}

func toArtifactView(a *model.Artifact) ArtifactView {
	return ArtifactView{
		ID: a.ID, AppID: a.AppID, VersionTag: a.VersionTag,
		FileName: a.FileName, FilePath: a.FilePath,
		FileMD5: a.FileMD5, FileSize: a.FileSize,
		BuildStatus: a.BuildStatus,
		CreatedAt:   a.CreatedAt.Format(time.RFC3339),
	}
}

// ArtifactService 制品注册（mock 实现）
type ArtifactService struct {
	db *gorm.DB
}

func NewArtifactService(db *gorm.DB) *ArtifactService {
	return &ArtifactService{db: db}
}

// Register 注册一个已存在的本地文件为制品。
// 校验：app 存在 / 文件是绝对路径且存在且是普通文件 / 计算 MD5+size。
func (s *ArtifactService) Register(in ArtifactInput) (ArtifactView, error) {
	// 1. app 存在
	var app model.Application
	if err := s.db.First(&app, in.AppID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ArtifactView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", in.AppID), 404)
		}
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	// 2. 路径与文件存在
	if !filepath.IsAbs(in.FilePath) {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "file_path 必须是绝对路径", 400)
	}
	st, err := os.Stat(in.FilePath)
	if err != nil {
		return ArtifactView{}, apperr.New("BAD_REQUEST", fmt.Sprintf("file_path 不可读：%v", err), 400)
	}
	if !st.Mode().IsRegular() {
		return ArtifactView{}, apperr.New("BAD_REQUEST", "file_path 必须是普通文件", 400)
	}
	// 3. MD5
	md5sum, err := deploy.LocalFileMD5(in.FilePath)
	if err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "md5", 500)
	}
	// 4. 写库
	name := in.FileName
	if name == "" {
		name = filepath.Base(in.FilePath)
	}
	a := &model.Artifact{
		AppID: in.AppID, VersionTag: in.VersionTag,
		FileName: name, FilePath: in.FilePath,
		FileMD5: md5sum, FileSize: st.Size(),
		BuildStatus: "success",
	}
	if err := s.db.Create(a).Error; err != nil {
		return ArtifactView{}, apperr.Wrap(err, "INTERNAL", "create artifact", 500)
	}
	return toArtifactView(a), nil
}

// ListByApp 列出应用制品（最新优先）
func (s *ArtifactService) ListByApp(appID uint) ([]ArtifactView, error) {
	var as []model.Artifact
	q := s.db.Order("id DESC")
	if appID > 0 {
		q = q.Where("app_id = ?", appID)
	}
	if err := q.Find(&as).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list artifacts", 500)
	}
	vs := make([]ArtifactView, len(as))
	for i := range as {
		vs[i] = toArtifactView(&as[i])
	}
	return vs, nil
}

// Get 取一个
func (s *ArtifactService) Get(id uint) (ArtifactView, error) {
	a, err := s.findByID(id)
	if err != nil {
		return ArtifactView{}, err
	}
	return toArtifactView(a), nil
}

// GetModel 取原始模型，供 pipeline 使用 FilePath / FileMD5。
func (s *ArtifactService) GetModel(id uint) (*model.Artifact, error) {
	return s.findByID(id)
}

// Delete 删除制品记录（不动本地文件）。
// 若仍被 Deployment.current_artifact_id 引用则拒绝，避免回滚链断裂。
func (s *ArtifactService) Delete(id uint) error {
	var n int64
	if err := s.db.Model(&model.Deployment{}).
		Where("current_artifact_id = ? OR previous_artifact_id = ?", id, id).
		Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT",
			fmt.Sprintf("制品仍被 %d 个部署引用，请先发布其他版本", n), 409)
	}
	res := s.db.Delete(&model.Artifact{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete artifact", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (s *ArtifactService) findByID(id uint) (*model.Artifact, error) {
	var a model.Artifact
	if err := s.db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get artifact", 500)
	}
	return &a, nil
}
