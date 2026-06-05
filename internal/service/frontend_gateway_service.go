package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
)

const (
	defaultFrontendGatewayContainer = "swift-devops-gateway"
	defaultFrontendGatewayNetwork   = "swift-devops-gateway"
	defaultFrontendGatewayImage     = "nginx:1.27-alpine"
	defaultFrontendGatewayBaseDir   = "/opt/swift-devops/gateway"

	generatedGatewayConfigPrefix = "swift-devops-"
)

// FrontendGatewayEnsureInput 创建/更新某台主机的前端网关实例。
type FrontendGatewayEnsureInput struct {
	ContainerName string `json:"container_name,omitempty"`
	NetworkName   string `json:"network_name,omitempty"`
	Image         string `json:"image,omitempty"`
	HTTPPort      int    `json:"http_port,omitempty"`
	HTTPSPort     int    `json:"https_port,omitempty"`
	BaseDir       string `json:"base_dir,omitempty"`
	ConfigDir     string `json:"config_dir,omitempty"`
	CertDir       string `json:"cert_dir,omitempty"`
	LogDir        string `json:"log_dir,omitempty"`
	Start         bool   `json:"start,omitempty"`
	ForceRecreate bool   `json:"force_recreate,omitempty"`
}

// FrontendGatewayRouteInput 创建/更新域名路由。
type FrontendGatewayRouteInput struct {
	AppID         uint   `json:"app_id,omitempty"`
	ServiceCode   string `json:"service_code,omitempty"`
	Domain        string `json:"domain,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	TargetPort    int    `json:"target_port,omitempty"`
	HTTPS         *bool  `json:"https,omitempty"`
	CertPath      string `json:"cert_path,omitempty"`
	KeyPath       string `json:"key_path,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

// FrontendGatewayView 网关实例响应。
type FrontendGatewayView struct {
	ID            uint   `json:"id"`
	HostID        uint   `json:"host_id"`
	HostName      string `json:"host_name"`
	HostIP        string `json:"host_ip"`
	ContainerName string `json:"container_name"`
	NetworkName   string `json:"network_name"`
	Image         string `json:"image"`
	HTTPPort      int    `json:"http_port"`
	HTTPSPort     int    `json:"https_port"`
	ConfigDir     string `json:"config_dir"`
	CertDir       string `json:"cert_dir"`
	LogDir        string `json:"log_dir"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// FrontendGatewayRouteView 域名路由响应。
type FrontendGatewayRouteView struct {
	ID            uint   `json:"id"`
	GatewayID     uint   `json:"gateway_id"`
	HostID        uint   `json:"host_id"`
	AppID         uint   `json:"app_id"`
	AppCode       string `json:"app_code"`
	AppName       string `json:"app_name"`
	ServiceCode   string `json:"service_code"`
	Domain        string `json:"domain"`
	ContainerName string `json:"container_name"`
	TargetPort    int    `json:"target_port"`
	HTTPS         bool   `json:"https"`
	CertPath      string `json:"cert_path"`
	KeyPath       string `json:"key_path"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// FrontendGatewayApplyView 网关配置下发结果。
type FrontendGatewayApplyView struct {
	Gateway   FrontendGatewayView        `json:"gateway"`
	Routes    []FrontendGatewayRouteView `json:"routes"`
	AppliedAt string                     `json:"applied_at"`
	Message   string                     `json:"message"`
}

// FrontendGatewayPreviewView 单条 route 对应的 Nginx 配置预览。
type FrontendGatewayPreviewView struct {
	RouteID  uint   `json:"route_id"`
	FileName string `json:"file_name"`
	Content  string `json:"content"`
}

// FrontendGatewayService 维护“网关容器 + 域名路由”。
type FrontendGatewayService struct {
	db      *gorm.DB
	hostSvc *HostService
	sshOpts sshpkg.DialOptions
}

func NewFrontendGatewayService(db *gorm.DB, hostSvc *HostService, sshTimeout time.Duration) *FrontendGatewayService {
	return &FrontendGatewayService{
		db:      db,
		hostSvc: hostSvc,
		sshOpts: sshpkg.DialOptions{Timeout: sshTimeout},
	}
}

func toFrontendGatewayView(g *model.FrontendGatewayInstance, h *model.Host) FrontendGatewayView {
	view := FrontendGatewayView{
		ID:            g.ID,
		HostID:        g.HostID,
		ContainerName: g.ContainerName,
		NetworkName:   g.NetworkName,
		Image:         g.Image,
		HTTPPort:      g.HTTPPort,
		HTTPSPort:     g.HTTPSPort,
		ConfigDir:     g.ConfigDir,
		CertDir:       g.CertDir,
		LogDir:        g.LogDir,
		Status:        g.Status,
		CreatedAt:     g.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     g.UpdatedAt.Format(time.RFC3339),
	}
	if h != nil {
		view.HostName = h.Name
		view.HostIP = h.IP
	}
	return view
}

func toFrontendGatewayRouteView(r *model.FrontendGatewayRoute, app *model.Application) FrontendGatewayRouteView {
	view := FrontendGatewayRouteView{
		ID:            r.ID,
		GatewayID:     r.GatewayID,
		HostID:        r.HostID,
		AppID:         r.AppID,
		ServiceCode:   r.ServiceCode,
		Domain:        r.Domain,
		ContainerName: r.ContainerName,
		TargetPort:    r.TargetPort,
		HTTPS:         r.HTTPS,
		CertPath:      r.CertPath,
		KeyPath:       r.KeyPath,
		Enabled:       r.Enabled,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     r.UpdatedAt.Format(time.RFC3339),
	}
	if app != nil {
		view.AppCode = app.AppCode
		view.AppName = app.Name
	}
	return view
}

// Ensure 创建或更新某台主机的 gateway 记录；Start=true 时会立刻下发配置并启动容器。
func (s *FrontendGatewayService) Ensure(ctx context.Context, hostID uint, in FrontendGatewayEnsureInput) (FrontendGatewayView, error) {
	host, err := s.findHost(hostID)
	if err != nil {
		return FrontendGatewayView{}, err
	}
	normalized, err := normalizeFrontendGatewayInput(in)
	if err != nil {
		return FrontendGatewayView{}, err
	}

	var row model.FrontendGatewayInstance
	err = s.db.Where("host_id = ?", hostID).First(&row).Error
	if err == nil {
		row.ContainerName = normalized.ContainerName
		row.NetworkName = normalized.NetworkName
		row.Image = normalized.Image
		row.HTTPPort = normalized.HTTPPort
		row.HTTPSPort = normalized.HTTPSPort
		row.ConfigDir = normalized.ConfigDir
		row.CertDir = normalized.CertDir
		row.LogDir = normalized.LogDir
		if row.Status == "" {
			row.Status = "pending"
		}
		if err := s.db.Save(&row).Error; err != nil {
			return FrontendGatewayView{}, apperr.Wrap(err, "INTERNAL", "update frontend gateway", 500)
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		row = model.FrontendGatewayInstance{
			HostID:        hostID,
			ContainerName: normalized.ContainerName,
			NetworkName:   normalized.NetworkName,
			Image:         normalized.Image,
			HTTPPort:      normalized.HTTPPort,
			HTTPSPort:     normalized.HTTPSPort,
			ConfigDir:     normalized.ConfigDir,
			CertDir:       normalized.CertDir,
			LogDir:        normalized.LogDir,
			Status:        "pending",
		}
		if err := s.db.Create(&row).Error; err != nil {
			if isUniqueConstraint(err) {
				return FrontendGatewayView{}, apperr.New("CONFLICT", "该主机已存在前端网关实例", 409)
			}
			return FrontendGatewayView{}, apperr.Wrap(err, "INTERNAL", "create frontend gateway", 500)
		}
	} else {
		return FrontendGatewayView{}, apperr.Wrap(err, "INTERNAL", "find frontend gateway", 500)
	}

	if in.Start {
		if _, err := s.apply(ctx, &row, in.ForceRecreate); err != nil {
			return FrontendGatewayView{}, err
		}
		if err := s.db.First(&row, row.ID).Error; err != nil {
			return FrontendGatewayView{}, apperr.Wrap(err, "INTERNAL", "reload frontend gateway", 500)
		}
	}
	return toFrontendGatewayView(&row, host), nil
}

func (s *FrontendGatewayService) GetByHost(hostID uint) (FrontendGatewayView, error) {
	host, err := s.findHost(hostID)
	if err != nil {
		return FrontendGatewayView{}, err
	}
	var row model.FrontendGatewayInstance
	if err := s.db.Where("host_id = ?", hostID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return FrontendGatewayView{}, apperr.ErrNotFound
		}
		return FrontendGatewayView{}, apperr.Wrap(err, "INTERNAL", "get frontend gateway", 500)
	}
	return toFrontendGatewayView(&row, host), nil
}

func (s *FrontendGatewayService) CreateRoute(ctx context.Context, hostID uint, in FrontendGatewayRouteInput, apply bool) (FrontendGatewayRouteView, error) {
	gateway, err := s.findOrCreateDefaultGateway(hostID)
	if err != nil {
		return FrontendGatewayRouteView{}, err
	}
	row, app, err := s.buildRoute(gateway, nil, in)
	if err != nil {
		return FrontendGatewayRouteView{}, err
	}
	if err := s.db.Create(row).Error; err != nil {
		if isUniqueConstraint(err) {
			return FrontendGatewayRouteView{}, apperr.New("CONFLICT",
				fmt.Sprintf("主机 %d 上域名 %s 已被其它前端路由占用", hostID, row.Domain), 409)
		}
		return FrontendGatewayRouteView{}, apperr.Wrap(err, "INTERNAL", "create frontend gateway route", 500)
	}
	if apply {
		if _, err := s.apply(ctx, gateway, false); err != nil {
			return FrontendGatewayRouteView{}, err
		}
		_ = s.db.First(row, row.ID).Error
	}
	return toFrontendGatewayRouteView(row, app), nil
}

func (s *FrontendGatewayService) ListRoutes(hostID uint) ([]FrontendGatewayRouteView, error) {
	if _, err := s.findHost(hostID); err != nil {
		return nil, err
	}
	var rows []model.FrontendGatewayRoute
	if err := s.db.Where("host_id = ?", hostID).Order("domain ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list frontend gateway routes", 500)
	}
	apps, err := s.routeApps(rows)
	if err != nil {
		return nil, err
	}
	out := make([]FrontendGatewayRouteView, len(rows))
	for i := range rows {
		out[i] = toFrontendGatewayRouteView(&rows[i], apps[rows[i].AppID])
	}
	return out, nil
}

func (s *FrontendGatewayService) GetRoute(id uint) (FrontendGatewayRouteView, error) {
	row, err := s.findRoute(id)
	if err != nil {
		return FrontendGatewayRouteView{}, err
	}
	var app model.Application
	_ = s.db.First(&app, row.AppID).Error
	return toFrontendGatewayRouteView(row, &app), nil
}

func (s *FrontendGatewayService) UpdateRoute(ctx context.Context, id uint, in FrontendGatewayRouteInput, apply bool) (FrontendGatewayRouteView, error) {
	current, err := s.findRoute(id)
	if err != nil {
		return FrontendGatewayRouteView{}, err
	}
	var gateway model.FrontendGatewayInstance
	if err := s.db.First(&gateway, current.GatewayID).Error; err != nil {
		return FrontendGatewayRouteView{}, apperr.Wrap(err, "INTERNAL", "find frontend gateway", 500)
	}
	row, app, err := s.buildRoute(&gateway, current, in)
	if err != nil {
		return FrontendGatewayRouteView{}, err
	}
	row.ID = current.ID
	row.CreatedAt = current.CreatedAt
	if err := s.db.Save(row).Error; err != nil {
		if isUniqueConstraint(err) {
			return FrontendGatewayRouteView{}, apperr.New("CONFLICT",
				fmt.Sprintf("主机 %d 上域名 %s 已被其它前端路由占用", row.HostID, row.Domain), 409)
		}
		return FrontendGatewayRouteView{}, apperr.Wrap(err, "INTERNAL", "update frontend gateway route", 500)
	}
	if apply {
		if _, err := s.apply(ctx, &gateway, false); err != nil {
			return FrontendGatewayRouteView{}, err
		}
		_ = s.db.First(row, row.ID).Error
	}
	return toFrontendGatewayRouteView(row, app), nil
}

func (s *FrontendGatewayService) DeleteRoute(ctx context.Context, id uint, apply bool) error {
	row, err := s.findRoute(id)
	if err != nil {
		return err
	}
	gatewayID := row.GatewayID
	res := s.db.Delete(&model.FrontendGatewayRoute{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete frontend gateway route", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	if apply {
		var gateway model.FrontendGatewayInstance
		if err := s.db.First(&gateway, gatewayID).Error; err != nil {
			return apperr.Wrap(err, "INTERNAL", "find frontend gateway", 500)
		}
		_, err = s.apply(ctx, &gateway, false)
		return err
	}
	return nil
}

func (s *FrontendGatewayService) PreviewRoute(id uint) (FrontendGatewayPreviewView, error) {
	row, err := s.findRoute(id)
	if err != nil {
		return FrontendGatewayPreviewView{}, err
	}
	content, err := renderFrontendGatewayRouteConfig(row)
	if err != nil {
		return FrontendGatewayPreviewView{}, err
	}
	return FrontendGatewayPreviewView{
		RouteID:  row.ID,
		FileName: frontendGatewayRouteFileName(row),
		Content:  content,
	}, nil
}

func (s *FrontendGatewayService) Apply(ctx context.Context, hostID uint, forceRecreate bool) (FrontendGatewayApplyView, error) {
	gateway, err := s.findOrCreateDefaultGateway(hostID)
	if err != nil {
		return FrontendGatewayApplyView{}, err
	}
	return s.apply(ctx, gateway, forceRecreate)
}

func (s *FrontendGatewayService) apply(ctx context.Context, gateway *model.FrontendGatewayInstance, forceRecreate bool) (FrontendGatewayApplyView, error) {
	host, err := s.findHost(gateway.HostID)
	if err != nil {
		return FrontendGatewayApplyView{}, err
	}
	routes, err := s.routesByGateway(gateway.ID)
	if err != nil {
		return FrontendGatewayApplyView{}, err
	}
	files, err := buildFrontendGatewayConfigFiles(routes)
	if err != nil {
		_ = s.markGatewayStatus(gateway.ID, "failed")
		return FrontendGatewayApplyView{}, err
	}

	client, err := s.dialHost(ctx, gateway.HostID)
	if err != nil {
		_ = s.markGatewayStatus(gateway.ID, "failed")
		return FrontendGatewayApplyView{}, err
	}
	defer client.Close()

	if err := s.writeGatewayConfigFiles(ctx, client, gateway, files); err != nil {
		_ = s.markGatewayStatus(gateway.ID, "failed")
		return FrontendGatewayApplyView{}, err
	}
	if err := s.ensureGatewayContainer(ctx, client, gateway, forceRecreate); err != nil {
		_ = s.markGatewayStatus(gateway.ID, "failed")
		return FrontendGatewayApplyView{}, err
	}
	if err := s.testAndReloadGateway(ctx, client, gateway); err != nil {
		_ = s.markGatewayStatus(gateway.ID, "failed")
		return FrontendGatewayApplyView{}, err
	}

	if err := s.markGatewayStatus(gateway.ID, "running"); err != nil {
		return FrontendGatewayApplyView{}, err
	}
	if err := s.markRouteStatuses(gateway.HostID); err != nil {
		return FrontendGatewayApplyView{}, err
	}
	if err := s.db.First(gateway, gateway.ID).Error; err != nil {
		return FrontendGatewayApplyView{}, apperr.Wrap(err, "INTERNAL", "reload frontend gateway", 500)
	}

	views, err := s.ListRoutes(gateway.HostID)
	if err != nil {
		return FrontendGatewayApplyView{}, err
	}
	return FrontendGatewayApplyView{
		Gateway:   toFrontendGatewayView(gateway, host),
		Routes:    views,
		AppliedAt: time.Now().Format(time.RFC3339),
		Message:   fmt.Sprintf("已同步 %d 条前端域名路由并 reload gateway", len(enabledFrontendRoutes(routes))),
	}, nil
}

func (s *FrontendGatewayService) findHost(hostID uint) (*model.Host, error) {
	var host model.Host
	if err := s.db.First(&host, hostID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("主机 %d 不存在", hostID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find host", 500)
	}
	return &host, nil
}

func (s *FrontendGatewayService) findOrCreateDefaultGateway(hostID uint) (*model.FrontendGatewayInstance, error) {
	if _, err := s.findHost(hostID); err != nil {
		return nil, err
	}
	var row model.FrontendGatewayInstance
	if err := s.db.Where("host_id = ?", hostID).First(&row).Error; err == nil {
		return &row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.Wrap(err, "INTERNAL", "find frontend gateway", 500)
	}
	def, err := normalizeFrontendGatewayInput(FrontendGatewayEnsureInput{})
	if err != nil {
		return nil, err
	}
	row = model.FrontendGatewayInstance{
		HostID:        hostID,
		ContainerName: def.ContainerName,
		NetworkName:   def.NetworkName,
		Image:         def.Image,
		HTTPPort:      def.HTTPPort,
		HTTPSPort:     def.HTTPSPort,
		ConfigDir:     def.ConfigDir,
		CertDir:       def.CertDir,
		LogDir:        def.LogDir,
		Status:        "pending",
	}
	if err := s.db.Create(&row).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, apperr.New("CONFLICT", "该主机已存在前端网关实例", 409)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "create frontend gateway", 500)
	}
	return &row, nil
}

func (s *FrontendGatewayService) findRoute(id uint) (*model.FrontendGatewayRoute, error) {
	var row model.FrontendGatewayRoute
	if err := s.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "find frontend gateway route", 500)
	}
	return &row, nil
}

func (s *FrontendGatewayService) routesByGateway(gatewayID uint) ([]model.FrontendGatewayRoute, error) {
	var rows []model.FrontendGatewayRoute
	if err := s.db.Where("gateway_id = ?", gatewayID).Order("domain ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list frontend gateway routes", 500)
	}
	return rows, nil
}

func (s *FrontendGatewayService) routeApps(rows []model.FrontendGatewayRoute) (map[uint]*model.Application, error) {
	out := map[uint]*model.Application{}
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]uint, 0, len(rows))
	seen := map[uint]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.AppID]; ok {
			continue
		}
		seen[row.AppID] = struct{}{}
		ids = append(ids, row.AppID)
	}
	var apps []model.Application
	if err := s.db.Where("id IN ?", ids).Find(&apps).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list route apps", 500)
	}
	for i := range apps {
		out[apps[i].ID] = &apps[i]
	}
	return out, nil
}

func (s *FrontendGatewayService) buildRoute(gateway *model.FrontendGatewayInstance, current *model.FrontendGatewayRoute, in FrontendGatewayRouteInput) (*model.FrontendGatewayRoute, *model.Application, error) {
	var app model.Application
	if in.AppID == 0 && current != nil {
		in.AppID = current.AppID
	}
	if in.AppID == 0 {
		return nil, nil, apperr.New("BAD_REQUEST", "app_id 必填", 400)
	}
	if err := s.db.First(&app, in.AppID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", in.AppID), 404)
		}
		return nil, nil, apperr.Wrap(err, "INTERNAL", "find app", 500)
	}

	serviceCode := strings.TrimSpace(in.ServiceCode)
	if serviceCode == "" && current != nil {
		serviceCode = current.ServiceCode
	}
	if serviceCode == "" {
		serviceCode = "web"
	}
	if !serviceCodeRE.MatchString(serviceCode) {
		return nil, nil, apperr.New("BAD_REQUEST",
			"service_code 必须以小写字母开头，2-50 位，仅含小写字母/数字/连字符", 400)
	}

	domain, err := normalizeFrontendDomain(in.Domain)
	if err != nil {
		return nil, nil, err
	}
	if domain == "" && current != nil {
		domain = current.Domain
	}
	if domain == "" {
		return nil, nil, apperr.New("BAD_REQUEST", "domain 必填", 400)
	}

	containerName := strings.TrimSpace(in.ContainerName)
	if containerName == "" && current != nil {
		containerName = current.ContainerName
	}
	if containerName == "" {
		containerName = fmt.Sprintf("sd-fe-%s-%s", app.AppCode, serviceCode)
	}
	if err := deploy.ValidateDockerContainerName(containerName); err != nil {
		return nil, nil, apperr.New("BAD_REQUEST", "container_name "+err.Error(), 400)
	}

	targetPort := in.TargetPort
	if targetPort == 0 && current != nil {
		targetPort = current.TargetPort
	}
	if targetPort == 0 {
		targetPort = 80
	}
	if err := validateTCPPort("target_port", targetPort); err != nil {
		return nil, nil, err
	}

	enabled := true
	if current != nil {
		enabled = current.Enabled
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	certPath := strings.TrimSpace(in.CertPath)
	keyPath := strings.TrimSpace(in.KeyPath)
	if current != nil {
		if certPath == "" {
			certPath = current.CertPath
		}
		if keyPath == "" {
			keyPath = current.KeyPath
		}
	}
	https := effectiveRouteHTTPS(in.HTTPS, current)
	if !https {
		certPath = ""
		keyPath = ""
	}
	if err := validateHTTPSConfig(https, certPath, keyPath); err != nil {
		return nil, nil, err
	}

	status := "pending"
	if !enabled {
		status = "disabled"
	} else if current != nil && current.Status != "" {
		status = current.Status
	}

	row := &model.FrontendGatewayRoute{
		GatewayID:     gateway.ID,
		HostID:        gateway.HostID,
		AppID:         app.ID,
		ServiceCode:   serviceCode,
		Domain:        domain,
		ContainerName: containerName,
		TargetPort:    targetPort,
		HTTPS:         https,
		CertPath:      certPath,
		KeyPath:       keyPath,
		Enabled:       enabled,
		Status:        status,
	}
	return row, &app, nil
}

func (s *FrontendGatewayService) dialHost(ctx context.Context, hostID uint) (*sshpkg.Client, error) {
	if s.hostSvc == nil {
		return nil, apperr.New("INTERNAL", "frontend gateway host service 未初始化", 500)
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

func (s *FrontendGatewayService) writeGatewayConfigFiles(ctx context.Context, client *sshpkg.Client, gateway *model.FrontendGatewayInstance, files []frontendGatewayConfigFile) error {
	mkdirCmd := fmt.Sprintf("mkdir -p %s %s %s",
		deploy.ShellQuote(gateway.ConfigDir),
		deploy.ShellQuote(gateway.CertDir),
		deploy.ShellQuote(gateway.LogDir),
	)
	if err := runRemoteChecked(ctx, client, mkdirCmd, "创建 gateway 目录失败"); err != nil {
		return err
	}
	cleanCmd := fmt.Sprintf("find %s -maxdepth 1 -type f -name %s -delete",
		deploy.ShellQuote(gateway.ConfigDir),
		deploy.ShellQuote(generatedGatewayConfigPrefix+"*.conf"),
	)
	if err := runRemoteChecked(ctx, client, cleanCmd, "清理旧 gateway 配置失败"); err != nil {
		return err
	}

	dispatcher := deploy.NewDispatcher(client.SSHClient())
	for _, file := range files {
		if err := dispatcher.WriteFile(path.Join(gateway.ConfigDir, file.Name), file.Content, 0o644); err != nil {
			return apperr.Wrap(err, "INTERNAL", "write gateway config", 500)
		}
	}
	return nil
}

func (s *FrontendGatewayService) ensureGatewayContainer(ctx context.Context, client *sshpkg.Client, gateway *model.FrontendGatewayInstance, forceRecreate bool) error {
	recreateLine := ":"
	if forceRecreate {
		recreateLine = fmt.Sprintf("docker rm -f %s >/dev/null 2>&1 || true", deploy.ShellQuote(gateway.ContainerName))
	}
	cmd := fmt.Sprintf(`
set -e
docker network inspect %[1]s >/dev/null 2>&1 || docker network create %[1]s >/dev/null
%[9]s
if ! docker inspect %[2]s >/dev/null 2>&1; then
  docker run -d \
    --name %[2]s \
    --restart unless-stopped \
    --network %[1]s \
    -p %[3]s \
    -p %[4]s \
    -v %[5]s \
    -v %[6]s \
    -v %[7]s \
    %[8]s >/dev/null
else
  docker network connect %[1]s %[2]s >/dev/null 2>&1 || true
  docker start %[2]s >/dev/null 2>&1 || true
fi
docker inspect -f '{{.State.Running}}' %[2]s
`,
		deploy.ShellQuote(gateway.NetworkName),
		deploy.ShellQuote(gateway.ContainerName),
		deploy.ShellQuote(fmt.Sprintf("%d:80", gateway.HTTPPort)),
		deploy.ShellQuote(fmt.Sprintf("%d:443", gateway.HTTPSPort)),
		deploy.ShellQuote(gateway.ConfigDir+":/etc/nginx/conf.d:ro"),
		deploy.ShellQuote(gateway.CertDir+":/etc/nginx/certs:ro"),
		deploy.ShellQuote(gateway.LogDir+":/var/log/nginx"),
		deploy.ShellQuote(gateway.Image),
		recreateLine,
	)
	out, err := client.Exec(ctx, cmd)
	if err != nil {
		return apperr.Wrap(err, "INTERNAL", "ensure gateway container", 500)
	}
	if out.ExitCode != 0 {
		return remoteCommandError("启动 gateway 容器失败", out)
	}
	if !strings.Contains(out.Stdout, "true") {
		return apperr.New("BAD_REQUEST", "gateway 容器未进入 running 状态", 400)
	}
	return nil
}

func (s *FrontendGatewayService) testAndReloadGateway(ctx context.Context, client *sshpkg.Client, gateway *model.FrontendGatewayInstance) error {
	testCmd := fmt.Sprintf("docker exec %s nginx -t", deploy.ShellQuote(gateway.ContainerName))
	if err := runRemoteChecked(ctx, client, testCmd, "gateway nginx 配置校验失败"); err != nil {
		return err
	}
	reloadCmd := fmt.Sprintf("docker exec %s nginx -s reload", deploy.ShellQuote(gateway.ContainerName))
	return runRemoteChecked(ctx, client, reloadCmd, "gateway nginx reload 失败")
}

func (s *FrontendGatewayService) markGatewayStatus(id uint, status string) error {
	if status == "" {
		return nil
	}
	if err := s.db.Model(&model.FrontendGatewayInstance{}).Where("id = ?", id).Update("status", status).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "update gateway status", 500)
	}
	return nil
}

func (s *FrontendGatewayService) markRouteStatuses(hostID uint) error {
	if err := s.db.Model(&model.FrontendGatewayRoute{}).
		Where("host_id = ? AND enabled = ?", hostID, false).
		Update("status", "disabled").Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "mark disabled routes", 500)
	}
	if err := s.db.Model(&model.FrontendGatewayRoute{}).
		Where("host_id = ? AND enabled = ?", hostID, true).
		Update("status", "active").Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "mark active routes", 500)
	}
	return nil
}

func normalizeFrontendGatewayInput(in FrontendGatewayEnsureInput) (model.FrontendGatewayInstance, error) {
	baseDir := strings.TrimSpace(in.BaseDir)
	if baseDir == "" {
		baseDir = defaultFrontendGatewayBaseDir
	}
	containerName := strings.TrimSpace(in.ContainerName)
	if containerName == "" {
		containerName = defaultFrontendGatewayContainer
	}
	if err := deploy.ValidateDockerContainerName(containerName); err != nil {
		return model.FrontendGatewayInstance{}, apperr.New("BAD_REQUEST", "container_name "+err.Error(), 400)
	}
	networkName := strings.TrimSpace(in.NetworkName)
	if networkName == "" {
		networkName = defaultFrontendGatewayNetwork
	}
	if err := validateDockerNetworkName(networkName); err != nil {
		return model.FrontendGatewayInstance{}, err
	}
	image := strings.TrimSpace(in.Image)
	if image == "" {
		image = defaultFrontendGatewayImage
	}
	if err := validateDockerImageRef(image); err != nil {
		return model.FrontendGatewayInstance{}, err
	}
	httpPort := in.HTTPPort
	if httpPort == 0 {
		httpPort = 80
	}
	httpsPort := in.HTTPSPort
	if httpsPort == 0 {
		httpsPort = 443
	}
	if err := validateTCPPort("http_port", httpPort); err != nil {
		return model.FrontendGatewayInstance{}, err
	}
	if err := validateTCPPort("https_port", httpsPort); err != nil {
		return model.FrontendGatewayInstance{}, err
	}
	if httpPort == httpsPort {
		return model.FrontendGatewayInstance{}, apperr.New("BAD_REQUEST", "http_port 与 https_port 不能相同", 400)
	}

	configDir := strings.TrimSpace(in.ConfigDir)
	if configDir == "" {
		configDir = path.Join(baseDir, "nginx", "conf.d")
	}
	certDir := strings.TrimSpace(in.CertDir)
	if certDir == "" {
		certDir = path.Join(baseDir, "certs")
	}
	logDir := strings.TrimSpace(in.LogDir)
	if logDir == "" {
		logDir = path.Join(baseDir, "logs")
	}
	for name, value := range map[string]string{
		"base_dir":   baseDir,
		"config_dir": configDir,
		"cert_dir":   certDir,
		"log_dir":    logDir,
	} {
		if err := validateAbsoluteCleanPath(name, value); err != nil {
			return model.FrontendGatewayInstance{}, err
		}
	}
	return model.FrontendGatewayInstance{
		ContainerName: containerName,
		NetworkName:   networkName,
		Image:         image,
		HTTPPort:      httpPort,
		HTTPSPort:     httpsPort,
		ConfigDir:     configDir,
		CertDir:       certDir,
		LogDir:        logDir,
	}, nil
}

func validateDockerNetworkName(name string) error {
	if err := deploy.ValidateDockerContainerName(name); err != nil {
		return apperr.New("BAD_REQUEST", "network_name 必须以字母或数字开头，长度 1-128，仅允许字母、数字、点、下划线、连字符", 400)
	}
	return nil
}

func validateDockerImageRef(image string) error {
	if image == "" {
		return apperr.New("BAD_REQUEST", "image 不能为空", 400)
	}
	if strings.ContainsAny(image, " \t\r\n'\"`;|&$<>") {
		return apperr.New("BAD_REQUEST", "image 不能包含空白或 shell 特殊字符", 400)
	}
	return nil
}

func validateTCPPort(name string, port int) error {
	if port < 1 || port > 65535 {
		return apperr.New("BAD_REQUEST", fmt.Sprintf("%s 必须在 1-65535 之间", name), 400)
	}
	return nil
}

func validateAbsoluteCleanPath(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return apperr.New("BAD_REQUEST", name+" 不能为空", 400)
	}
	if !path.IsAbs(value) {
		return apperr.New("BAD_REQUEST", name+" 必须是 Linux 绝对路径", 400)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return apperr.New("BAD_REQUEST", name+" 不能包含控制字符", 400)
	}
	return nil
}

func validateHTTPSConfig(https bool, certPath, keyPath string) error {
	if !https {
		if certPath != "" || keyPath != "" {
			return apperr.New("BAD_REQUEST", "未启用 https 时不要填写 cert_path / key_path", 400)
		}
		return nil
	}
	if certPath == "" || keyPath == "" {
		return apperr.New("BAD_REQUEST", "启用 https 时 cert_path 与 key_path 必填", 400)
	}
	if err := validateNginxPath("cert_path", certPath); err != nil {
		return err
	}
	return validateNginxPath("key_path", keyPath)
}

func effectiveRouteHTTPS(raw *bool, current *model.FrontendGatewayRoute) bool {
	if raw != nil {
		return *raw
	}
	if current != nil {
		return current.HTTPS
	}
	return false
}

func validateNginxPath(name, value string) error {
	if err := validateAbsoluteCleanPath(name, value); err != nil {
		return err
	}
	if strings.ContainsAny(value, " {};") {
		return apperr.New("BAD_REQUEST", name+" 不能包含空格、分号或花括号", 400)
	}
	return nil
}

var frontendDomainLabelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeFrontendDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", nil
	}
	if strings.Contains(domain, "://") || strings.ContainsAny(domain, "/\\: \t\r\n") {
		return "", apperr.New("BAD_REQUEST", "domain 只填写域名，不要包含协议、端口或路径", 400)
	}
	if len(domain) > 253 {
		return "", apperr.New("BAD_REQUEST", "domain 长度不能超过 253", 400)
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", apperr.New("BAD_REQUEST", "domain 至少需要包含二级域名，如 www.example.com", 400)
	}
	for i, label := range labels {
		if i == 0 && label == "*" {
			continue
		}
		if !frontendDomainLabelRE.MatchString(label) {
			return "", apperr.New("BAD_REQUEST", "domain 格式非法", 400)
		}
	}
	return domain, nil
}

type frontendGatewayConfigFile struct {
	Name    string
	Content string
}

func buildFrontendGatewayConfigFiles(routes []model.FrontendGatewayRoute) ([]frontendGatewayConfigFile, error) {
	files := []frontendGatewayConfigFile{{
		Name:    generatedGatewayConfigPrefix + "00-default.conf",
		Content: renderFrontendGatewayDefaultConfig(),
	}}
	enabled := enabledFrontendRoutes(routes)
	sort.Slice(enabled, func(i, j int) bool {
		if enabled[i].Domain == enabled[j].Domain {
			return enabled[i].ID < enabled[j].ID
		}
		return enabled[i].Domain < enabled[j].Domain
	})
	for i := range enabled {
		content, err := renderFrontendGatewayRouteConfig(&enabled[i])
		if err != nil {
			return nil, err
		}
		files = append(files, frontendGatewayConfigFile{
			Name:    frontendGatewayRouteFileName(&enabled[i]),
			Content: content,
		})
	}
	return files, nil
}

func enabledFrontendRoutes(routes []model.FrontendGatewayRoute) []model.FrontendGatewayRoute {
	out := make([]model.FrontendGatewayRoute, 0, len(routes))
	for _, route := range routes {
		if route.Enabled {
			out = append(out, route)
		}
	}
	return out
}

func renderFrontendGatewayDefaultConfig() string {
	return `# Generated by swift-devops. Do not edit manually.
server {
    listen 80 default_server;
    server_name _;

    location / {
        return 404;
    }
}
`
}

func renderFrontendGatewayRouteConfig(route *model.FrontendGatewayRoute) (string, error) {
	if route == nil {
		return "", apperr.New("BAD_REQUEST", "route 不能为空", 400)
	}
	domain, err := normalizeFrontendDomain(route.Domain)
	if err != nil {
		return "", err
	}
	if domain == "" {
		return "", apperr.New("BAD_REQUEST", "domain 必填", 400)
	}
	if err := deploy.ValidateDockerContainerName(route.ContainerName); err != nil {
		return "", apperr.New("BAD_REQUEST", "container_name "+err.Error(), 400)
	}
	if err := validateTCPPort("target_port", route.TargetPort); err != nil {
		return "", err
	}
	if err := validateHTTPSConfig(route.HTTPS, route.CertPath, route.KeyPath); err != nil {
		return "", err
	}

	upstream := frontendGatewayUpstreamName(route)
	proxyBlock := renderFrontendGatewayProxyBlock(upstream)
	if !route.HTTPS {
		return fmt.Sprintf(`# Generated by swift-devops. Do not edit manually.
upstream %[1]s {
    server %[2]s:%[3]d;
    keepalive 16;
}

server {
    listen 80;
    server_name %[4]s;

%[5]s
}
`, upstream, route.ContainerName, route.TargetPort, domain, indentNginxBlock(proxyBlock, 4)), nil
	}

	return fmt.Sprintf(`# Generated by swift-devops. Do not edit manually.
upstream %[1]s {
    server %[2]s:%[3]d;
    keepalive 16;
}

server {
    listen 80;
    server_name %[4]s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name %[4]s;

    ssl_certificate %[6]s;
    ssl_certificate_key %[7]s;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;

%[5]s
}
`, upstream, route.ContainerName, route.TargetPort, domain, indentNginxBlock(proxyBlock, 4), route.CertPath, route.KeyPath), nil
}

func renderFrontendGatewayProxyBlock(upstream string) string {
	return fmt.Sprintf(`location / {
    proxy_pass http://%s;
    proxy_http_version 1.1;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}`, upstream)
}

func indentNginxBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		if strings.TrimSpace(lines[i]) != "" {
			lines[i] = prefix + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func frontendGatewayUpstreamName(route *model.FrontendGatewayRoute) string {
	if route.ID > 0 {
		return fmt.Sprintf("sd_fe_route_%d", route.ID)
	}
	return fmt.Sprintf("sd_fe_%s_%s", sanitizeNginxIdent(route.Domain), sanitizeNginxIdent(route.ServiceCode))
}

func frontendGatewayRouteFileName(route *model.FrontendGatewayRoute) string {
	id := "new"
	if route.ID > 0 {
		id = strconv.FormatUint(uint64(route.ID), 10)
	}
	return fmt.Sprintf("%sroute-%s-%s.conf", generatedGatewayConfigPrefix, id, sanitizeNginxIdent(route.Domain))
}

func sanitizeNginxIdent(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "route"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func runRemoteChecked(ctx context.Context, client *sshpkg.Client, cmd, label string) error {
	out, err := client.Exec(ctx, cmd)
	if err != nil {
		return apperr.Wrap(err, "INTERNAL", label, 500)
	}
	if out.ExitCode != 0 {
		return remoteCommandError(label, out)
	}
	return nil
}

func remoteCommandError(label string, out sshpkg.ExecResult) error {
	msg := strings.TrimSpace(out.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(out.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit=%d", out.ExitCode)
	}
	return apperr.New("BAD_REQUEST", label+"："+msg, 400)
}
