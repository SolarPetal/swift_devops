package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// DeploymentInput 绑定主机到应用的参数
type DeploymentInput struct {
	HostID      uint   `json:"host_id" binding:"required"`
	ServiceCode string `json:"service_code,omitempty"`
	GroupTag    string `json:"group_tag,omitempty"` // legacy，功能已下线，写入时清空
	Port        int    `json:"port,omitempty"`      // 0 = 沿用应用 / service 默认端口
}

// DeploymentView 绑定关系视图（带主机基本信息，前端列表可直接渲染）
type DeploymentView struct {
	ID                     uint   `json:"id"`
	AppID                  uint   `json:"app_id"`
	HostID                 uint   `json:"host_id"`
	ServiceCode            string `json:"service_code"`
	HostName               string `json:"host_name"`
	HostIP                 string `json:"host_ip"`
	HostStatus             string `json:"host_status"`
	GroupTag               string `json:"group_tag"`
	CurrentArtifactID      uint   `json:"current_artifact_id"`
	PreviousArtifactID     uint   `json:"previous_artifact_id"`
	CurrentArtifactItemID  uint   `json:"current_artifact_item_id"`
	PreviousArtifactItemID uint   `json:"previous_artifact_item_id"`
	CurrentBundleID        uint   `json:"current_bundle_id"`
	CurrentBundleVersion   string `json:"current_bundle_version"`
	Port                   int    `json:"port"`
	Status                 string `json:"status"` // runtime 状态：pending/running/stopped/failed
	LastRunID              uint   `json:"last_run_id"`
	LastDeployStatus       string `json:"last_deploy_status"`
	LastDeployStage        string `json:"last_deploy_stage"`
	LastDeployError        string `json:"last_deploy_error"`
	LastDeployAt           string `json:"last_deploy_at"`
	RuntimeContainerName   string `json:"runtime_container_name"`
	RuntimeCheckedAt       string `json:"runtime_checked_at"`
	RuntimeStatusDetail    string `json:"runtime_status_detail"`
	RuntimeCheckError      string `json:"runtime_check_error"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type DeploymentRuntimeLogsInput struct {
	Lines      int
	Timestamps bool
}

type DeploymentRuntimeLogsView struct {
	DeploymentID  uint   `json:"deployment_id"`
	AppID         uint   `json:"app_id"`
	HostID        uint   `json:"host_id"`
	HostName      string `json:"host_name"`
	HostIP        string `json:"host_ip"`
	ServiceCode   string `json:"service_code"`
	DeployMode    string `json:"deploy_mode"`
	ContainerName string `json:"container_name"`
	Lines         int    `json:"lines"`
	Status        string `json:"status"`
	Logs          string `json:"logs"`
	CapturedAt    string `json:"captured_at"`
}

type DeploymentRuntimeActionView struct {
	DeploymentID  uint   `json:"deployment_id"`
	AppID         uint   `json:"app_id"`
	HostID        uint   `json:"host_id"`
	HostName      string `json:"host_name"`
	HostIP        string `json:"host_ip"`
	ServiceCode   string `json:"service_code"`
	DeployMode    string `json:"deploy_mode"`
	ContainerName string `json:"container_name"`
	Action        string `json:"action"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	OperatedAt    string `json:"operated_at"`
}

// DeploymentService 应用×主机绑定关系
type DeploymentService struct {
	db      *gorm.DB
	hostSvc *HostService
	sshOpts sshpkg.DialOptions
}

func NewDeploymentService(db *gorm.DB, opts ...DeploymentServiceOption) *DeploymentService {
	s := &DeploymentService{db: db}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type DeploymentServiceOption func(*DeploymentService)

func WithDeploymentRuntimeLogs(hostSvc *HostService, timeout time.Duration) DeploymentServiceOption {
	return func(s *DeploymentService) {
		s.hostSvc = hostSvc
		s.sshOpts = sshpkg.DialOptions{Timeout: timeout}
	}
}

// Bind 把主机绑定到应用。
// 同一 (app_id, host_id) 由 UNIQUE 索引兜底；service 也提供更友好的错误。
func (s *DeploymentService) Bind(appID uint, in DeploymentInput) (DeploymentView, error) {
	in.GroupTag = ""
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
	serviceCode, err := s.resolveBindServiceCode(&app, in.ServiceCode)
	if err != nil {
		return DeploymentView{}, err
	}
	d := &model.Deployment{
		AppID:       appID,
		HostID:      in.HostID,
		ServiceCode: serviceCode,
		GroupTag:    "",
		Port:        in.Port,
		Status:      "pending",
	}
	if err := s.db.Create(d).Error; err != nil {
		if isUniqueConstraint(err) {
			return DeploymentView{}, apperr.New("CONFLICT",
				fmt.Sprintf("主机 %d 已绑定到应用 %d 的 service %s", in.HostID, appID, displayServiceCode(serviceCode)), 409)
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
	if err := s.decorateCurrentArtifactItems(vs); err != nil {
		return nil, err
	}
	if err := s.decorateLastDeployStatus(vs); err != nil {
		return nil, err
	}
	return vs, nil
}

// SyncRuntimeByApp 主动探测远端运行态并回写 deployments.status。
//
// 目前重点覆盖 Docker：部署成功只代表“曾经启动成功”，用户手工 docker stop/rm 后，
// 需要通过远端 docker ps -a 重新确认容器是否仍存在且处于 running。
func (s *DeploymentService) SyncRuntimeByApp(ctx context.Context, appID uint) ([]DeploymentView, error) {
	vs, err := s.ListByApp(appID)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return vs, nil
	}
	if s.hostSvc == nil {
		return nil, apperr.New("INTERNAL", "runtime check service 未初始化", 500)
	}

	var app model.Application
	if err := s.db.First(&app, appID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", appID), 404)
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get app", 500)
	}
	serviceMap, err := s.appServiceMap(appID)
	if err != nil {
		return nil, err
	}

	type runtimeCheckItem struct {
		index         int
		containerName string
	}
	checksByHost := map[uint][]runtimeCheckItem{}
	checkedAt := time.Now().Format(time.RFC3339)
	for i := range vs {
		svcCode := displayServiceCode(vs[i].ServiceCode)
		svc := serviceMap[svcCode]
		mode := effectiveDeploymentMode(&app, svc, svcCode)
		vs[i].RuntimeCheckedAt = checkedAt
		if deploy.NormalizeDeployMode(mode) != deploy.DeployModeDocker {
			vs[i].RuntimeStatusDetail = fmt.Sprintf("%s 模式暂不做远端运行态检测", mode)
			continue
		}
		name, err := effectiveDeploymentContainerName(&app, svc, svcCode)
		vs[i].RuntimeContainerName = name
		if err != nil {
			vs[i].RuntimeCheckError = err.Error()
			continue
		}
		checksByHost[vs[i].HostID] = append(checksByHost[vs[i].HostID], runtimeCheckItem{
			index:         i,
			containerName: name,
		})
	}

	for hostID, checks := range checksByHost {
		containers, err := s.listHostDockerContainersAll(ctx, hostID)
		if err != nil {
			msg := err.Error()
			for _, check := range checks {
				vs[check.index].RuntimeCheckError = msg
			}
			continue
		}
		containerMap := map[string]HostDockerContainerView{}
		for i := range containers {
			containerMap[containers[i].Name] = containers[i]
		}
		for _, check := range checks {
			c, ok := containerMap[check.containerName]
			status, detail := deploymentStatusFromDockerContainer(check.containerName, c, ok)
			vs[check.index].Status = status
			vs[check.index].RuntimeStatusDetail = detail
			if err := s.updateDeploymentRuntimeStatus(vs[check.index].ID, status); err != nil {
				vs[check.index].RuntimeCheckError = err.Error()
			}
		}
	}
	return vs, nil
}

// Unbind 解除绑定
func (s *DeploymentService) Unbind(id uint) error {
	var d model.Deployment
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperr.ErrNotFound
		}
		return apperr.Wrap(err, "INTERNAL", "find deployment", 500)
	}
	if strings.TrimSpace(d.Status) == "running" {
		return apperr.New("CONFLICT", "当前容器仍在运行，请先停止容器后再解绑主机", 409)
	}
	res := s.db.Delete(&model.Deployment{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete deployment", 500)
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

func (s *DeploymentService) appServiceMap(appID uint) (map[string]*model.AppService, error) {
	var services []model.AppService
	if err := s.db.Where("app_id = ?", appID).Find(&services).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list app services", 500)
	}
	out := make(map[string]*model.AppService, len(services))
	for i := range services {
		code := displayServiceCode(services[i].ServiceCode)
		out[code] = &services[i]
	}
	return out, nil
}

func (s *DeploymentService) listHostDockerContainersAll(ctx context.Context, hostID uint) ([]HostDockerContainerView, error) {
	client, host, err := s.hostSvc.dialHost(ctx, hostID)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	out, err := client.Exec(ctx, "docker ps -a --format '{{json .}}'")
	if err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list docker containers", 500)
	}
	if out.ExitCode != 0 {
		msg := strings.TrimSpace(out.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(out.Stdout)
		}
		return nil, apperr.New("BAD_REQUEST",
			fmt.Sprintf("主机 %s Docker 不可用或当前用户无权限：%s", host.IP, msg), 400)
	}
	return parseDockerPSJSONLines(out.Stdout), nil
}

func (s *DeploymentService) updateDeploymentRuntimeStatus(id uint, status string) error {
	if status == "" {
		return nil
	}
	if err := s.db.Model(&model.Deployment{}).Where("id = ?", id).
		Update("status", status).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "update deployment runtime status", 500)
	}
	return nil
}

func deploymentStatusFromDockerContainer(
	containerName string,
	c HostDockerContainerView,
	exists bool,
) (string, string) {
	if !exists {
		return "stopped", fmt.Sprintf("Docker 容器不存在：%s", containerName)
	}
	state := strings.ToLower(strings.TrimSpace(c.State))
	detail := strings.TrimSpace(c.Status)
	if detail == "" {
		detail = state
	}
	switch state {
	case "running":
		return "running", detail
	case "dead", "restarting":
		return "failed", detail
	default:
		return "stopped", detail
	}
}

func deploymentStatusFromDockerState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return "running"
	case "dead", "restarting":
		return "failed"
	default:
		return "stopped"
	}
}

func (s *DeploymentService) resolveBindServiceCode(app *model.Application, raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if err := EnsureDefaultAppService(s.db, app); err != nil {
		return "", err
	}
	var services []model.AppService
	if err := s.db.Where("app_id = ? AND enabled = ?", app.ID, true).
		Order("startup_order ASC, id ASC").
		Find(&services).Error; err != nil {
		if strings.Contains(err.Error(), "no such table") {
			if code == "" {
				return "default", nil
			}
			return code, nil
		}
		return "", apperr.Wrap(err, "INTERNAL", "list services", 500)
	}
	if len(services) == 0 {
		return "", apperr.New("BAD_REQUEST",
			"应用没有启用的 service，请先在「服务配置」创建或导入至少一个 service 后再绑定主机", 400)
	}
	if code == "" {
		if len(services) == 1 {
			return services[0].ServiceCode, nil
		}
		return "", apperr.New("BAD_REQUEST",
			"绑定主机时必须选择 service；请在「服务配置」确认服务清单后再绑定", 400)
	}
	for _, svc := range services {
		if svc.ServiceCode == code {
			return code, nil
		}
	}
	return "", apperr.New("BAD_REQUEST", fmt.Sprintf("service %s 不存在或未启用", code), 400)
}

func (s *DeploymentService) RuntimeLogs(
	ctx context.Context,
	id uint,
	in DeploymentRuntimeLogsInput,
) (DeploymentRuntimeLogsView, error) {
	if s.hostSvc == nil {
		return DeploymentRuntimeLogsView{}, apperr.New("INTERNAL", "runtime logs service 未初始化", 500)
	}
	lines := normalizeLogLines(in.Lines)
	d, app, svc, err := s.loadDeploymentRuntime(id)
	if err != nil {
		return DeploymentRuntimeLogsView{}, err
	}
	svcCode := effectiveDeploymentServiceCode(d, svc)
	mode := effectiveDeploymentMode(app, svc, svcCode)
	if deploy.NormalizeDeployMode(mode) != deploy.DeployModeDocker {
		return DeploymentRuntimeLogsView{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("当前 deployment 使用 %s 模式，不是 Docker 容器部署", deploy.NormalizeDeployMode(mode)), 400)
	}
	containerName, err := effectiveDeploymentContainerName(app, svc, svcCode)
	if err != nil {
		return DeploymentRuntimeLogsView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}

	target, auth, host, err := s.hostSvc.LoadAuth(d.HostID)
	if err != nil {
		return DeploymentRuntimeLogsView{}, err
	}
	client, err := sshpkg.Dial(target, auth, s.sshOpts)
	if err != nil {
		return DeploymentRuntimeLogsView{}, apperr.Wrap(err, "INTERNAL", "dial host for runtime logs", 500)
	}
	defer client.Close()
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		s.hostSvc.RecordHostKey(host.ID, client.LearnedHostKey())
	}

	logs, status, err := readRemoteDockerLogs(ctx, client, containerName, lines, in.Timestamps)
	if err != nil {
		return DeploymentRuntimeLogsView{}, apperr.Wrap(err, "INTERNAL", "read docker logs", 500)
	}
	return DeploymentRuntimeLogsView{
		DeploymentID:  d.ID,
		AppID:         d.AppID,
		HostID:        d.HostID,
		HostName:      host.Name,
		HostIP:        host.IP,
		ServiceCode:   svcCode,
		DeployMode:    deploy.DeployModeDocker,
		ContainerName: containerName,
		Lines:         lines,
		Status:        status,
		Logs:          logs,
		CapturedAt:    time.Now().Format(time.RFC3339),
	}, nil
}

func (s *DeploymentService) StopRuntime(ctx context.Context, id uint) (DeploymentRuntimeActionView, error) {
	return s.controlDockerRuntime(ctx, id, "stop")
}

func (s *DeploymentService) RestartRuntime(ctx context.Context, id uint) (DeploymentRuntimeActionView, error) {
	return s.controlDockerRuntime(ctx, id, "restart")
}

func (s *DeploymentService) controlDockerRuntime(
	ctx context.Context,
	id uint,
	action string,
) (DeploymentRuntimeActionView, error) {
	d, app, svc, err := s.loadDeploymentRuntime(id)
	if err != nil {
		return DeploymentRuntimeActionView{}, err
	}
	svcCode := effectiveDeploymentServiceCode(d, svc)
	mode := effectiveDeploymentMode(app, svc, svcCode)
	if deploy.NormalizeDeployMode(mode) != deploy.DeployModeDocker {
		return DeploymentRuntimeActionView{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("当前 deployment 使用 %s 模式，不是 Docker 容器部署", deploy.NormalizeDeployMode(mode)), 400)
	}
	containerName, err := effectiveDeploymentContainerName(app, svc, svcCode)
	if err != nil {
		return DeploymentRuntimeActionView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	if s.hostSvc == nil {
		return DeploymentRuntimeActionView{}, apperr.New("INTERNAL", "runtime action service 未初始化", 500)
	}

	target, auth, host, err := s.hostSvc.LoadAuth(d.HostID)
	if err != nil {
		return DeploymentRuntimeActionView{}, err
	}
	client, err := sshpkg.Dial(target, auth, s.sshOpts)
	if err != nil {
		return DeploymentRuntimeActionView{}, apperr.Wrap(err, "INTERNAL", "dial host for runtime action", 500)
	}
	defer client.Close()
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		s.hostSvc.RecordHostKey(host.ID, client.LearnedHostKey())
	}

	q := deploy.ShellQuote(containerName)
	var cmd string
	var actionLabel string
	switch action {
	case "stop":
		cmd = fmt.Sprintf("docker stop %s 2>&1", q)
		actionLabel = "停止"
	case "restart":
		cmd = fmt.Sprintf("docker restart %s 2>&1", q)
		actionLabel = "重启"
	default:
		return DeploymentRuntimeActionView{}, apperr.New("BAD_REQUEST", fmt.Sprintf("不支持的运行操作：%s", action), 400)
	}
	out, err := client.Exec(ctx, cmd)
	if err != nil {
		return DeploymentRuntimeActionView{}, apperr.Wrap(err, "INTERNAL", fmt.Sprintf("%s docker container", action), 500)
	}
	msg := strings.TrimSpace(out.Stdout)
	if msg == "" {
		msg = strings.TrimSpace(out.Stderr)
	}
	if out.ExitCode != 0 {
		if msg == "" {
			msg = fmt.Sprintf("docker %s exit=%d", action, out.ExitCode)
		}
		return DeploymentRuntimeActionView{}, apperr.New("CONFLICT",
			fmt.Sprintf("%s容器失败：%s", actionLabel, msg), 409)
	}

	dockerState, err := readRemoteDockerStatus(ctx, client, containerName)
	if err != nil {
		return DeploymentRuntimeActionView{}, apperr.Wrap(err, "INTERNAL", "inspect docker status", 500)
	}
	runtimeStatus := deploymentStatusFromDockerState(dockerState)
	if err := s.updateDeploymentRuntimeStatus(d.ID, runtimeStatus); err != nil {
		return DeploymentRuntimeActionView{}, err
	}
	if msg == "" {
		msg = fmt.Sprintf("容器已%s，Docker 状态：%s", actionLabel, dockerState)
	}

	return DeploymentRuntimeActionView{
		DeploymentID:  d.ID,
		AppID:         d.AppID,
		HostID:        d.HostID,
		HostName:      host.Name,
		HostIP:        host.IP,
		ServiceCode:   svcCode,
		DeployMode:    deploy.DeployModeDocker,
		ContainerName: containerName,
		Action:        action,
		Status:        runtimeStatus,
		Message:       msg,
		OperatedAt:    time.Now().Format(time.RFC3339),
	}, nil
}

func (s *DeploymentService) loadDeploymentRuntime(
	id uint,
) (*model.Deployment, *model.Application, *model.AppService, error) {
	var d model.Deployment
	if err := s.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, apperr.ErrNotFound
		}
		return nil, nil, nil, apperr.Wrap(err, "INTERNAL", "get deployment", 500)
	}
	var app model.Application
	if err := s.db.First(&app, d.AppID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, apperr.New("NOT_FOUND", fmt.Sprintf("应用 %d 不存在", d.AppID), 404)
		}
		return nil, nil, nil, apperr.Wrap(err, "INTERNAL", "get app", 500)
	}
	svcCode := strings.TrimSpace(d.ServiceCode)
	if svcCode == "" {
		svcCode = "default"
	}
	var svc model.AppService
	if err := s.db.Where("app_id = ? AND service_code = ?", app.ID, svcCode).First(&svc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &d, &app, nil, nil
		}
		return nil, nil, nil, apperr.Wrap(err, "INTERNAL", "get app service", 500)
	}
	return &d, &app, &svc, nil
}

func (s *DeploymentService) viewWithHost(d *model.Deployment, h *model.Host) DeploymentView {
	v := DeploymentView{
		ID:                     d.ID,
		AppID:                  d.AppID,
		HostID:                 d.HostID,
		ServiceCode:            d.ServiceCode,
		GroupTag:               d.GroupTag,
		CurrentArtifactID:      d.CurrentArtifactID,
		PreviousArtifactID:     d.PreviousArtifactID,
		CurrentArtifactItemID:  d.CurrentArtifactItemID,
		PreviousArtifactItemID: d.PreviousArtifactItemID,
		Port:                   d.Port,
		Status:                 d.Status,
		CreatedAt:              d.CreatedAt.Format(time.RFC3339),
		UpdatedAt:              d.UpdatedAt.Format(time.RFC3339),
	}
	if h != nil {
		v.HostName = h.Name
		v.HostIP = h.IP
		v.HostStatus = h.Status
	}
	return v
}

func (s *DeploymentService) decorateCurrentArtifactItems(vs []DeploymentView) error {
	itemIDs := make([]uint, 0, len(vs))
	seenItems := map[uint]struct{}{}
	for i := range vs {
		id := vs[i].CurrentArtifactItemID
		if id == 0 {
			continue
		}
		if _, ok := seenItems[id]; ok {
			continue
		}
		seenItems[id] = struct{}{}
		itemIDs = append(itemIDs, id)
	}
	if len(itemIDs) == 0 {
		return nil
	}

	var items []model.ArtifactItem
	if err := s.db.Select("id", "bundle_id").Where("id IN ?", itemIDs).Find(&items).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "list current artifact items", 500)
	}
	itemBundle := make(map[uint]uint, len(items))
	bundleIDs := make([]uint, 0, len(items))
	seenBundles := map[uint]struct{}{}
	for i := range items {
		itemBundle[items[i].ID] = items[i].BundleID
		if items[i].BundleID == 0 {
			continue
		}
		if _, ok := seenBundles[items[i].BundleID]; ok {
			continue
		}
		seenBundles[items[i].BundleID] = struct{}{}
		bundleIDs = append(bundleIDs, items[i].BundleID)
	}

	bundleVersion := map[uint]string{}
	if len(bundleIDs) > 0 {
		var bundles []model.ArtifactBundle
		if err := s.db.Select("id", "version_tag").Where("id IN ?", bundleIDs).Find(&bundles).Error; err != nil {
			return apperr.Wrap(err, "INTERNAL", "list current bundles", 500)
		}
		for i := range bundles {
			bundleVersion[bundles[i].ID] = bundles[i].VersionTag
		}
	}

	for i := range vs {
		bundleID := itemBundle[vs[i].CurrentArtifactItemID]
		if bundleID == 0 {
			continue
		}
		vs[i].CurrentBundleID = bundleID
		vs[i].CurrentBundleVersion = bundleVersion[bundleID]
	}
	return nil
}

func (s *DeploymentService) decorateLastDeployStatus(vs []DeploymentView) error {
	depIDs := make([]uint, 0, len(vs))
	for i := range vs {
		depIDs = append(depIDs, vs[i].ID)
	}
	if len(depIDs) == 0 {
		return nil
	}

	var rows []model.PipelineRunHost
	if err := s.db.Where("deployment_id IN ?", depIDs).
		Order("deployment_id ASC, run_id DESC, id DESC").
		Find(&rows).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "list latest deployment run hosts", 500)
	}
	latest := make(map[uint]model.PipelineRunHost, len(vs))
	for i := range rows {
		if rows[i].DeploymentID == 0 {
			continue
		}
		if _, ok := latest[rows[i].DeploymentID]; ok {
			continue
		}
		latest[rows[i].DeploymentID] = rows[i]
	}

	for i := range vs {
		rh, ok := latest[vs[i].ID]
		if !ok {
			continue
		}
		vs[i].LastRunID = rh.RunID
		vs[i].LastDeployStatus = rh.Status
		vs[i].LastDeployStage = rh.CurrentStage
		vs[i].LastDeployError = rh.Error
		vs[i].LastDeployAt = runHostTime(rh).Format(time.RFC3339)
	}
	return nil
}

func runHostTime(rh model.PipelineRunHost) time.Time {
	if rh.EndedAt != nil {
		return *rh.EndedAt
	}
	if rh.StartedAt != nil {
		return *rh.StartedAt
	}
	if !rh.UpdatedAt.IsZero() {
		return rh.UpdatedAt
	}
	return rh.CreatedAt
}

func normalizeLogLines(lines int) int {
	if lines <= 0 {
		return 300
	}
	if lines < 20 {
		return 20
	}
	if lines > 2000 {
		return 2000
	}
	return lines
}

func effectiveDeploymentServiceCode(d *model.Deployment, svc *model.AppService) string {
	if svc != nil && strings.TrimSpace(svc.ServiceCode) != "" {
		return strings.TrimSpace(svc.ServiceCode)
	}
	if c := strings.TrimSpace(d.ServiceCode); c != "" {
		return c
	}
	return "default"
}

func effectiveDeploymentMode(app *model.Application, svc *model.AppService, svcCode string) string {
	// 部署模式由应用统一管理；svc/svcCode 参数保留用于调用兼容和容器名等相邻逻辑。
	_ = svc
	_ = svcCode
	deployMode := strings.TrimSpace(app.DeployMode)
	if isDockerBuildModeForDeploymentLogs(app.BuildMode) {
		deployMode = deploy.DeployModeDocker
	}
	return deploy.NormalizeDeployMode(deployMode)
}

func effectiveDeploymentContainerName(app *model.Application, svc *model.AppService, svcCode string) (string, error) {
	_ = svc
	raw := strings.TrimSpace(app.DockerContainerName)
	if raw == "" {
		return deploy.AppSpec{ServiceName: deploymentUnitNameKey(app.AppCode, svcCode)}.ContainerName(), nil
	}
	rendered := deploy.RenderDockerRuntimeNameTemplate(raw, app.AppCode, svcCode)
	if err := deploy.ValidateDockerContainerName(rendered); err != nil {
		return "", fmt.Errorf(
			"docker_container_name %q 渲染为 %q 后非法：%w",
			raw, rendered, err,
		)
	}
	return rendered, nil
}

func readRemoteDockerLogs(
	ctx context.Context,
	client *sshpkg.Client,
	containerName string,
	lines int,
	timestamps bool,
) (string, string, error) {
	q := deploy.ShellQuote(containerName)
	tsArg := ""
	if timestamps {
		tsArg = " --timestamps"
	}
	logCmd := fmt.Sprintf("docker logs%s --tail %d %s 2>&1 || true", tsArg, lines, q)
	logsOut, err := client.Exec(ctx, logCmd)
	if err != nil {
		return "", "", err
	}
	logs := strings.TrimSpace(logsOut.Stdout)
	if logs == "" {
		logs = strings.TrimSpace(logsOut.Stderr)
	}

	status, err := readRemoteDockerStatus(ctx, client, containerName)
	if err != nil {
		return logs, "", err
	}
	return logs, status, nil
}

func readRemoteDockerStatus(ctx context.Context, client *sshpkg.Client, containerName string) (string, error) {
	q := deploy.ShellQuote(containerName)
	statusCmd := fmt.Sprintf("docker inspect -f '{{.State.Status}}' %s 2>/dev/null || echo unknown", q)
	statusOut, err := client.Exec(ctx, statusCmd)
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(statusOut.Stdout)
	if status == "" {
		status = "unknown"
	}
	return status, nil
}

func isDefaultDeploymentServiceCode(svcCode string) bool {
	c := strings.TrimSpace(svcCode)
	return c == "" || c == "default"
}

func displayServiceCode(svcCode string) string {
	if strings.TrimSpace(svcCode) == "" {
		return "default"
	}
	return strings.TrimSpace(svcCode)
}

func deploymentUnitNameKey(appCode, svcCode string) string {
	if isDefaultDeploymentServiceCode(svcCode) {
		return appCode
	}
	return appCode + "-" + strings.TrimSpace(svcCode)
}

func isDockerBuildModeForDeploymentLogs(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "local-docker", "remote-docker":
		return true
	default:
		return false
	}
}
