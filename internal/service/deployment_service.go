package service

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
)

// DeploymentInput 绑定主机到应用的参数
type DeploymentInput struct {
	HostID   uint   `json:"host_id" binding:"required"`
	GroupTag string `json:"group_tag,omitempty"` // "" / "blue" / "green"
	Port     int    `json:"port,omitempty"`      // 0 = 沿用应用默认端口
}

// DeploymentView 绑定关系视图（带主机基本信息，前端列表可直接渲染）
type DeploymentView struct {
	ID                 uint   `json:"id"`
	AppID              uint   `json:"app_id"`
	HostID             uint   `json:"host_id"`
	HostName           string `json:"host_name"`
	HostIP             string `json:"host_ip"`
	HostStatus         string `json:"host_status"`
	GroupTag           string `json:"group_tag"`
	CurrentArtifactID  uint   `json:"current_artifact_id"`
	PreviousArtifactID uint   `json:"previous_artifact_id"`
	Port               int    `json:"port"`
	Status             string `json:"status"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// DeploymentService 应用×主机绑定关系
type DeploymentService struct {
	db *gorm.DB
}

func NewDeploymentService(db *gorm.DB) *DeploymentService {
	return &DeploymentService{db: db}
}

// Bind 把主机绑定到应用。
// 同一 (app_id, host_id) 由 UNIQUE 索引兜底；service 也提供更友好的错误。
func (s *DeploymentService) Bind(appID uint, in DeploymentInput) (DeploymentView, error) {
	if err := validateGroupTag(in.GroupTag); err != nil {
		return DeploymentView{}, err
	}
	// 校验 app 存在
	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DeploymentView{}, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return DeploymentView{}, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}
	// 校验 host 存在
	var host model.Host
	if err := s.db.First(&host, in.HostID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DeploymentView{}, apperr.New("NOT_FOUND", fmt.Sprintf("主机 %d 不存在", in.HostID), 404)
		}
		return DeploymentView{}, apperr.Wrap(err, "INTERNAL", "find host", 500)
	}
	d := &model.Deployment{
		AppID:    appID,
		HostID:   in.HostID,
		GroupTag: in.GroupTag,
		Port:     in.Port,
		Status:   "pending",
	}
	if err := s.db.Create(d).Error; err != nil {
		if isUniqueConstraint(err) {
			return DeploymentView{}, apperr.New("CONFLICT",
				fmt.Sprintf("主机 %d 已绑定到应用 %d", in.HostID, appID), 409)
		}
		return DeploymentView{}, apperr.Wrap(err, "INTERNAL", "create deployment", 500)
	}
	return s.viewWithHost(d, &host), nil
}

// ListByApp 应用的部署主机列表（含主机基本信息，单次查询拼装）
func (s *DeploymentService) ListByApp(appID uint) ([]DeploymentView, error) {
	var ds []model.Deployment
	if err := s.db.Where("app_id = ?", appID).Order("id ASC").Find(&ds).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list deployments", 500)
	}
	if len(ds) == 0 {
		return []DeploymentView{}, nil
	}
	hostIDs := make([]uint, 0, len(ds))
	for _, d := range ds {
		hostIDs = append(hostIDs, d.HostID)
	}
	var hosts []model.Host
	if err := s.db.Where("id IN ?", hostIDs).Find(&hosts).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list hosts", 500)
	}
	hostMap := make(map[uint]*model.Host, len(hosts))
	for i := range hosts {
		hostMap[hosts[i].ID] = &hosts[i]
	}
	vs := make([]DeploymentView, len(ds))
	for i, d := range ds {
		vs[i] = s.viewWithHost(&d, hostMap[d.HostID])
	}
	return vs, nil
}

// Unbind 解除绑定
func (s *DeploymentService) Unbind(id uint) error {
	res := s.db.Delete(&model.Deployment{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete deployment", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// UpdateGroup 更新 group_tag（蓝绿切换前的分组准备）
func (s *DeploymentService) UpdateGroup(id uint, group string) error {
	if err := validateGroupTag(group); err != nil {
		return err
	}
	res := s.db.Model(&model.Deployment{}).Where("id = ?", id).Update("group_tag", group)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "update deployment", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// CountByApp 该应用绑定的主机数（供 AppService.Delete 拒绝级联用）
func (s *DeploymentService) CountByApp(appID uint) (int64, error) {
	var n int64
	if err := s.db.Model(&model.Deployment{}).Where("app_id = ?", appID).Count(&n).Error; err != nil {
		return 0, apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	return n, nil
}

// CountByHost 该主机被多少应用绑定（供 HostService.Delete 拒绝级联用）
func (s *DeploymentService) CountByHost(hostID uint) (int64, error) {
	var n int64
	if err := s.db.Model(&model.Deployment{}).Where("host_id = ?", hostID).Count(&n).Error; err != nil {
		return 0, apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	return n, nil
}

func (s *DeploymentService) viewWithHost(d *model.Deployment, h *model.Host) DeploymentView {
	v := DeploymentView{
		ID:                 d.ID,
		AppID:              d.AppID,
		HostID:             d.HostID,
		GroupTag:           d.GroupTag,
		CurrentArtifactID:  d.CurrentArtifactID,
		PreviousArtifactID: d.PreviousArtifactID,
		Port:               d.Port,
		Status:             d.Status,
		CreatedAt:          d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          d.UpdatedAt.Format(time.RFC3339),
	}
	if h != nil {
		v.HostName = h.Name
		v.HostIP = h.IP
		v.HostStatus = h.Status
	}
	return v
}

func validateGroupTag(g string) error {
	if g == "" || g == "blue" || g == "green" {
		return nil
	}
	return apperr.New("BAD_REQUEST", `group_tag 只能为 ""/"blue"/"green"`, 400)
}
