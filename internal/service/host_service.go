// Package service 实现主机/应用/制品/流水线等业务编排。
//
// 设计原则：handler 薄，service 厚；事务边界与状态机在这一层。
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	"swift-devops/internal/pkg/deploy"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// HostInput 创建/更新主机入参
type HostInput struct {
	Name       string `json:"name" binding:"required"`
	IP         string `json:"ip" binding:"required"`
	Port       int    `json:"port,omitempty"`
	AuthType   string `json:"auth_type" binding:"required,oneof=password key"`
	Username   string `json:"username" binding:"required"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	GroupTag   string `json:"group_tag,omitempty"` // legacy，功能已下线，写入时清空
	Tags       string `json:"tags,omitempty"`
	// JavaPath 该主机 java 可执行文件绝对路径。Sprint 3.7 起。
	// 留空 = 后端写库时默认 "/usr/bin/java"；非空必须以 / 开头（绝对路径）。
	JavaPath string `json:"java_path,omitempty"`
}

// HostView 主机响应，不含敏感字段
type HostView struct {
	ID                   uint   `json:"id"`
	Name                 string `json:"name"`
	IP                   string `json:"ip"`
	Port                 int    `json:"port"`
	AuthType             string `json:"auth_type"`
	Username             string `json:"username"`
	Status               string `json:"status"`
	GroupTag             string `json:"group_tag"` // legacy，历史兼容
	Tags                 string `json:"tags"`
	JavaPath             string `json:"java_path"`
	RunningInstanceCount int64  `json:"running_instance_count"`
	HasSecret            bool   `json:"has_secret"`
	HasHostKey           bool   `json:"has_host_key"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type HostDockerContainerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Status    string `json:"status"`
	Ports     string `json:"ports"`
	CreatedAt string `json:"created_at"`
}

type HostDockerLogsInput struct {
	Container  string
	Lines      int
	Timestamps bool
}

type HostDockerLogsView struct {
	HostID        uint   `json:"host_id"`
	HostName      string `json:"host_name"`
	HostIP        string `json:"host_ip"`
	ContainerName string `json:"container_name"`
	Lines         int    `json:"lines"`
	Status        string `json:"status"`
	Logs          string `json:"logs"`
	CapturedAt    string `json:"captured_at"`
}

type HostMetricsView struct {
	HostID      uint    `json:"host_id"`
	HostName    string  `json:"host_name"`
	HostIP      string  `json:"host_ip"`
	HostStatus  string  `json:"host_status"`
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`
	DiskUsage   float64 `json:"disk_usage"`
	Load1       float64 `json:"load1"`
	CheckedAt   string  `json:"checked_at"`
	Cached      bool    `json:"cached"`
	Error       string  `json:"error,omitempty"`
}

type cachedHostMetrics struct {
	view      HostMetricsView
	expiresAt time.Time
}

type sampledHostMetrics struct {
	CPUUsage    float64
	MemoryUsage float64
	DiskUsage   float64
	Load1       float64
}

const (
	hostMetricsCacheTTL      = 30 * time.Second
	hostMetricsSampleTimeout = 8 * time.Second
	hostMetricsConcurrency   = 4
)

const hostMetricsCommand = `echo __CPU1__; head -n 1 /proc/stat; sleep 0.2; echo __CPU2__; head -n 1 /proc/stat; echo __MEM__; cat /proc/meminfo; echo __DISK__; df -P /; echo __LOAD__; cat /proc/loadavg`

func toView(h *model.Host) HostView {
	return HostView{
		ID: h.ID, Name: h.Name, IP: h.IP, Port: h.Port,
		AuthType: h.AuthType, Username: h.Username,
		Status: h.Status, GroupTag: h.GroupTag, Tags: h.Tags,
		JavaPath:   h.JavaPath,
		HasSecret:  h.Secret != "",
		HasHostKey: h.HostKey != "",
		CreatedAt:  h.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  h.UpdatedAt.Format(time.RFC3339),
	}
}

// secretBlob 是 Host.Secret 解密后的内部结构
type secretBlob struct {
	Value      string `json:"v"`
	Passphrase string `json:"p,omitempty"`
}

// HostService Host 业务编排
type HostService struct {
	db           *gorm.DB
	crypt        *cryptopkg.AESGCM
	metricsMu    sync.Mutex
	metricsCache map[uint]cachedHostMetrics
}

func NewHostService(db *gorm.DB, crypt *cryptopkg.AESGCM) *HostService {
	return &HostService{db: db, crypt: crypt, metricsCache: map[uint]cachedHostMetrics{}}
}

// Create 录入新主机
func (s *HostService) Create(in HostInput) (HostView, error) {
	if err := validateHostInput(in); err != nil {
		return HostView{}, err
	}
	if in.Port == 0 {
		in.Port = 22
	}
	sec, err := s.encryptSecret(in)
	if err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "encrypt secret", 500)
	}
	h := &model.Host{
		Name: in.Name, IP: in.IP, Port: in.Port,
		AuthType: in.AuthType, Username: in.Username,
		Secret: sec, GroupTag: "", Tags: in.Tags,
		JavaPath: defaultJavaPath(in.JavaPath),
		Status:   "unknown",
	}
	if err := s.db.Create(h).Error; err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "create host", 500)
	}
	return toView(h), nil
}

// List 列出全部主机
func (s *HostService) List() ([]HostView, error) {
	var hs []model.Host
	if err := s.db.Order("id DESC").Find(&hs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list hosts", 500)
	}
	counts, err := s.runningInstanceCounts()
	if err != nil {
		return nil, err
	}
	vs := make([]HostView, len(hs))
	for i := range hs {
		vs[i] = toView(&hs[i])
		vs[i].RunningInstanceCount = counts[hs[i].ID]
	}
	return vs, nil
}

// Get 取一个
func (s *HostService) Get(id uint) (HostView, error) {
	h, err := s.findByID(id)
	if err != nil {
		return HostView{}, err
	}
	v := toView(h)
	n, err := s.runningInstanceCount(id)
	if err != nil {
		return HostView{}, err
	}
	v.RunningInstanceCount = n
	return v, nil
}

// Update 更新主机。Password / PrivateKey 字段为空时保留原值。
func (s *HostService) Update(id uint, in HostInput) (HostView, error) {
	// 注意：Update 不要求凭证非空（保留旧值）；AuthType 由 handler 的 binding tag 校验
	if err := validateJavaPath(in.JavaPath); err != nil {
		return HostView{}, err
	}
	h, err := s.findByID(id)
	if err != nil {
		return HostView{}, err
	}
	h.Name = in.Name
	h.IP = in.IP
	if in.Port != 0 {
		h.Port = in.Port
	}
	h.AuthType = in.AuthType
	h.Username = in.Username
	h.GroupTag = ""
	h.Tags = in.Tags
	h.JavaPath = defaultJavaPath(in.JavaPath)
	if in.Password != "" || in.PrivateKey != "" {
		sec, encErr := s.encryptSecret(in)
		if encErr != nil {
			return HostView{}, apperr.Wrap(encErr, "INTERNAL", "encrypt secret", 500)
		}
		h.Secret = sec
		h.HostKey = "" // 凭证改了，重置 TOFU
	}
	if err := s.db.Save(h).Error; err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "update host", 500)
	}
	v := toView(h)
	n, err := s.runningInstanceCount(id)
	if err != nil {
		return HostView{}, err
	}
	v.RunningInstanceCount = n
	return v, nil
}

// Delete 删除主机。
// 拒绝级联：若仍有 Deployment 引用，先让用户解绑。
func (s *HostService) Delete(id uint) error {
	var n int64
	if err := s.db.Model(&model.Deployment{}).Where("host_id = ?", id).Count(&n).Error; err != nil {
		return apperr.Wrap(err, "INTERNAL", "count deployments", 500)
	}
	if n > 0 {
		return apperr.New("CONFLICT",
			fmt.Sprintf("主机仍被 %d 个应用引用，请先解绑", n), 409)
	}
	res := s.db.Delete(&model.Host{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete host", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// TestConnect 测试 SSH 连通性。
//   - 解密 secret
//   - 调 ssh.TestConnectivity
//   - 成功且 host key 是新学的：落库
//   - 成功：status=online；失败：status=offline
func (s *HostService) TestConnect(id uint) (sshpkg.TestResult, error) {
	h, err := s.findByID(id)
	if err != nil {
		return sshpkg.TestResult{}, err
	}
	blob, err := s.decryptSecret(h)
	if err != nil {
		return sshpkg.TestResult{}, apperr.Wrap(err, "INTERNAL", "decrypt secret", 500)
	}
	target := sshpkg.HostTarget{
		IP: h.IP, Port: h.Port, User: h.Username, KnownHostKey: h.HostKey,
	}
	auth := sshpkg.AuthMethod{
		Type: h.AuthType, Passphrase: blob.Passphrase,
	}
	if h.AuthType == "password" {
		auth.Password = blob.Value
	} else {
		auth.KeyPEM = blob.Value
	}
	result := sshpkg.TestConnectivity(target, auth, sshpkg.DialOptions{Timeout: 10 * time.Second})

	updates := map[string]any{}
	if result.OK {
		updates["status"] = "online"
		if h.HostKey == "" && result.HostKey != "" {
			updates["host_key"] = result.HostKey
		}
	} else {
		updates["status"] = "offline"
	}
	s.db.Model(&model.Host{}).Where("id = ?", id).Updates(updates)
	return result, nil
}

func (s *HostService) DockerContainers(ctx context.Context, id uint) ([]HostDockerContainerView, error) {
	client, host, err := s.dialHost(ctx, id)
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

func (s *HostService) DockerLogs(ctx context.Context, id uint, in HostDockerLogsInput) (HostDockerLogsView, error) {
	container := strings.TrimSpace(in.Container)
	if err := deploy.ValidateDockerContainerName(container); err != nil {
		return HostDockerLogsView{}, apperr.New("BAD_REQUEST", err.Error(), 400)
	}
	lines := normalizeLogLines(in.Lines)
	client, host, err := s.dialHost(ctx, id)
	if err != nil {
		return HostDockerLogsView{}, err
	}
	defer client.Close()

	logs, status, err := readRemoteDockerLogs(ctx, client, container, lines, in.Timestamps)
	if err != nil {
		return HostDockerLogsView{}, apperr.Wrap(err, "INTERNAL", "read docker logs", 500)
	}
	return HostDockerLogsView{
		HostID:        host.ID,
		HostName:      host.Name,
		HostIP:        host.IP,
		ContainerName: container,
		Lines:         lines,
		Status:        status,
		Logs:          logs,
		CapturedAt:    time.Now().Format(time.RFC3339),
	}, nil
}

func (s *HostService) Metrics(ctx context.Context) ([]HostMetricsView, error) {
	var hosts []model.Host
	if err := s.db.Order("id DESC").Find(&hosts).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list hosts for metrics", 500)
	}
	if len(hosts) == 0 {
		return []HostMetricsView{}, nil
	}

	out := make([]HostMetricsView, len(hosts))
	sem := make(chan struct{}, hostMetricsConcurrency)
	var wg sync.WaitGroup
	for i := range hosts {
		host := hosts[i]
		if cached, ok := s.cachedMetrics(host.ID, time.Now()); ok {
			out[i] = cached
			continue
		}
		wg.Add(1)
		go func(idx int, h model.Host) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				view := baseHostMetricsView(&h)
				view.Error = compactHostMetricsError(ctx.Err())
				view.CheckedAt = time.Now().Format(time.RFC3339)
				out[idx] = view
				return
			}
			out[idx] = s.sampleHostMetrics(ctx, &h)
		}(i, host)
	}
	wg.Wait()
	return out, nil
}

// LoadAuth 取主机的 SSH 拨号参数（不返回明文给 handler 层）。
// pipeline / 部署等需要主动建立连接的子系统使用。
//   - 同样负责 TOFU：上层成功 dial 后可拿 Client.LearnedHostKey() 通过 RecordHostKey 落库
func (s *HostService) LoadAuth(id uint) (sshpkg.HostTarget, sshpkg.AuthMethod, *model.Host, error) {
	h, err := s.findByID(id)
	if err != nil {
		return sshpkg.HostTarget{}, sshpkg.AuthMethod{}, nil, err
	}
	blob, err := s.decryptSecret(h)
	if err != nil {
		return sshpkg.HostTarget{}, sshpkg.AuthMethod{}, nil, apperr.Wrap(err, "INTERNAL", "decrypt secret", 500)
	}
	target := sshpkg.HostTarget{
		IP: h.IP, Port: h.Port, User: h.Username, KnownHostKey: h.HostKey,
	}
	auth := sshpkg.AuthMethod{Type: h.AuthType, Passphrase: blob.Passphrase}
	if h.AuthType == "password" {
		auth.Password = blob.Value
	} else {
		auth.KeyPEM = blob.Value
	}
	return target, auth, h, nil
}

// RecordHostKey 把首次 TOFU 学到的 host key 落库。
// 仅当原 HostKey 为空时写入，避免覆盖既有信任。
func (s *HostService) RecordHostKey(id uint, key string) {
	if key == "" {
		return
	}
	s.db.Model(&model.Host{}).
		Where("id = ? AND (host_key IS NULL OR host_key = '')", id).
		Update("host_key", key)
}

// --- 内部 ---

func baseHostMetricsView(h *model.Host) HostMetricsView {
	return HostMetricsView{
		HostID:     h.ID,
		HostName:   h.Name,
		HostIP:     h.IP,
		HostStatus: h.Status,
	}
}

func (s *HostService) cachedMetrics(hostID uint, now time.Time) (HostMetricsView, bool) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	cached, ok := s.metricsCache[hostID]
	if !ok || now.After(cached.expiresAt) {
		return HostMetricsView{}, false
	}
	view := cached.view
	view.Cached = true
	return view, true
}

func (s *HostService) cacheMetrics(hostID uint, view HostMetricsView, now time.Time) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	view.Cached = false
	s.metricsCache[hostID] = cachedHostMetrics{
		view:      view,
		expiresAt: now.Add(hostMetricsCacheTTL),
	}
}

func (s *HostService) sampleHostMetrics(ctx context.Context, h *model.Host) HostMetricsView {
	view := baseHostMetricsView(h)
	now := time.Now()
	view.CheckedAt = now.Format(time.RFC3339)

	sampleCtx, cancel := context.WithTimeout(ctx, hostMetricsSampleTimeout)
	defer cancel()
	client, host, err := s.dialHost(sampleCtx, h.ID)
	if err != nil {
		view.Error = compactHostMetricsError(err)
		view.HostStatus = "offline"
		s.updateHostStatus(h.ID, "offline")
		s.cacheMetrics(h.ID, view, now)
		return view
	}
	defer client.Close()
	view.HostName = host.Name
	view.HostIP = host.IP

	result, err := client.Exec(sampleCtx, hostMetricsCommand)
	if err != nil {
		view.Error = compactHostMetricsError(err)
		view.HostStatus = "online"
		s.updateHostStatus(h.ID, "online")
		s.cacheMetrics(h.ID, view, now)
		return view
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(result.Stdout)
		}
		view.Error = compactHostMetricsError(errors.New(msg))
		view.HostStatus = "online"
		s.updateHostStatus(h.ID, "online")
		s.cacheMetrics(h.ID, view, now)
		return view
	}
	metrics, err := parseHostMetricsOutput(result.Stdout)
	if err != nil {
		view.Error = compactHostMetricsError(err)
		view.HostStatus = "online"
		s.updateHostStatus(h.ID, "online")
		s.cacheMetrics(h.ID, view, now)
		return view
	}
	view.CPUUsage = clampPercent(metrics.CPUUsage)
	view.MemoryUsage = clampPercent(metrics.MemoryUsage)
	view.DiskUsage = clampPercent(metrics.DiskUsage)
	view.Load1 = roundFloat(metrics.Load1, 2)
	view.HostStatus = "online"
	s.updateHostStatus(h.ID, "online")
	s.cacheMetrics(h.ID, view, now)
	return view
}

func (s *HostService) dialHost(ctx context.Context, id uint) (*sshpkg.Client, *model.Host, error) {
	target, auth, host, err := s.LoadAuth(id)
	if err != nil {
		return nil, nil, err
	}
	client, err := sshpkg.Dial(target, auth, sshpkg.DialOptions{Timeout: 10 * time.Second})
	if err != nil {
		return nil, nil, apperr.Wrap(err, "INTERNAL", "dial host", 500)
	}
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		s.RecordHostKey(host.ID, client.LearnedHostKey())
	}
	return client, host, nil
}

func (s *HostService) updateHostStatus(id uint, status string) {
	if status == "" {
		return
	}
	_ = s.db.Model(&model.Host{}).Where("id = ?", id).Update("status", status).Error
}

func parseHostMetricsOutput(stdout string) (sampledHostMetrics, error) {
	var (
		mode             string
		cpu1Line         string
		cpu2Line         string
		memTotalKB       float64
		memAvailableKB   float64
		memFreeKB        float64
		diskUsagePercent float64
		load1            float64
		diskParsed       bool
		loadParsed       bool
	)
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "__CPU1__":
			mode = "cpu1"
			continue
		case "__CPU2__":
			mode = "cpu2"
			continue
		case "__MEM__":
			mode = "mem"
			continue
		case "__DISK__":
			mode = "disk"
			continue
		case "__LOAD__":
			mode = "load"
			continue
		}

		switch mode {
		case "cpu1":
			if strings.HasPrefix(line, "cpu ") {
				cpu1Line = line
			}
		case "cpu2":
			if strings.HasPrefix(line, "cpu ") {
				cpu2Line = line
			}
		case "mem":
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			value, err := strconv.ParseFloat(fields[1], 64)
			if err != nil {
				continue
			}
			switch strings.TrimSuffix(fields[0], ":") {
			case "MemTotal":
				memTotalKB = value
			case "MemAvailable":
				memAvailableKB = value
			case "MemFree":
				memFreeKB = value
			}
		case "disk":
			if strings.HasPrefix(line, "Filesystem") || diskParsed {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				value := strings.TrimSuffix(fields[4], "%")
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					diskUsagePercent = parsed
					diskParsed = true
				}
			}
		case "load":
			if loadParsed {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				if parsed, err := strconv.ParseFloat(fields[0], 64); err == nil {
					load1 = parsed
					loadParsed = true
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sampledHostMetrics{}, err
	}

	cpuUsage, err := cpuUsageFromProcStat(cpu1Line, cpu2Line)
	if err != nil {
		return sampledHostMetrics{}, err
	}
	if memAvailableKB == 0 {
		memAvailableKB = memFreeKB
	}
	if memTotalKB <= 0 {
		return sampledHostMetrics{}, errors.New("parse meminfo: missing MemTotal")
	}
	if !diskParsed {
		return sampledHostMetrics{}, errors.New("parse df: missing disk usage")
	}
	if !loadParsed {
		return sampledHostMetrics{}, errors.New("parse loadavg: missing load1")
	}
	return sampledHostMetrics{
		CPUUsage:    cpuUsage,
		MemoryUsage: ((memTotalKB - memAvailableKB) * 100) / memTotalKB,
		DiskUsage:   diskUsagePercent,
		Load1:       load1,
	}, nil
}

func cpuUsageFromProcStat(first, second string) (float64, error) {
	idle1, total1, err := procStatIdleTotal(first)
	if err != nil {
		return 0, err
	}
	idle2, total2, err := procStatIdleTotal(second)
	if err != nil {
		return 0, err
	}
	totalDelta := total2 - total1
	idleDelta := idle2 - idle1
	if totalDelta <= 0 {
		return 0, errors.New("parse /proc/stat: total delta <= 0")
	}
	return ((totalDelta - idleDelta) * 100) / totalDelta, nil
}

func procStatIdleTotal(line string) (float64, float64, error) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, errors.New("parse /proc/stat: invalid cpu line")
	}
	values := make([]float64, 8)
	for i := 0; i < len(values); i++ {
		fieldIdx := i + 1
		if fieldIdx >= len(fields) {
			values[i] = 0
			continue
		}
		parsed, err := strconv.ParseFloat(fields[fieldIdx], 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse /proc/stat field %d: %w", fieldIdx, err)
		}
		values[i] = parsed
	}
	idle := values[3] + values[4]
	total := 0.0
	for _, value := range values {
		total += value
	}
	return idle, total, nil
}

func clampPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return roundFloat(value, 1)
}

func roundFloat(value float64, precision int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	multiplier := math.Pow(10, float64(precision))
	return math.Round(value*multiplier) / multiplier
}

func compactHostMetricsError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "采样失败"
	}
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len([]rune(msg)) > 180 {
		runes := []rune(msg)
		msg = string(runes[:180]) + "…"
	}
	return msg
}

func parseDockerPSJSONLines(s string) []HostDockerContainerView {
	scanner := bufio.NewScanner(strings.NewReader(s))
	out := []HostDockerContainerView{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		out = append(out, HostDockerContainerView{
			ID:        raw["ID"],
			Name:      raw["Names"],
			Image:     raw["Image"],
			State:     raw["State"],
			Status:    raw["Status"],
			Ports:     raw["Ports"],
			CreatedAt: raw["CreatedAt"],
		})
	}
	return out
}

func (s *HostService) runningInstanceCounts() (map[uint]int64, error) {
	type row struct {
		HostID uint
		Count  int64
	}
	var rows []row
	if err := s.db.Model(&model.Deployment{}).
		Select("host_id, COUNT(*) AS count").
		Where("status = ?", "running").
		Group("host_id").
		Scan(&rows).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "count running instances", 500)
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.HostID] = r.Count
	}
	return out, nil
}

func (s *HostService) runningInstanceCount(hostID uint) (int64, error) {
	var n int64
	if err := s.db.Model(&model.Deployment{}).
		Where("host_id = ? AND status = ?", hostID, "running").
		Count(&n).Error; err != nil {
		return 0, apperr.Wrap(err, "INTERNAL", "count running instances", 500)
	}
	return n, nil
}

func (s *HostService) findByID(id uint) (*model.Host, error) {
	var h model.Host
	if err := s.db.First(&h, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get host", 500)
	}
	return &h, nil
}

func validateHostInput(in HostInput) error {
	if in.AuthType == "password" && in.Password == "" {
		return apperr.New("BAD_REQUEST", "auth_type=password 时 password 必填", 400)
	}
	if in.AuthType == "key" && in.PrivateKey == "" {
		return apperr.New("BAD_REQUEST", "auth_type=key 时 private_key 必填", 400)
	}
	if err := validateJavaPath(in.JavaPath); err != nil {
		return err
	}
	return nil
}

// validateJavaPath 共用校验：空允许，非空必须绝对路径
func validateJavaPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return apperr.New("BAD_REQUEST",
			"java_path 必须是绝对路径（以 / 开头），如 /usr/bin/java 或 /opt/java-17/bin/java", 400)
	}
	return nil
}

// defaultJavaPath 写库时把空值填成 /usr/bin/java（兜底），非空保持原样。
func defaultJavaPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/usr/bin/java"
	}
	return p
}

func (s *HostService) encryptSecret(in HostInput) (string, error) {
	blob := secretBlob{Passphrase: in.Passphrase}
	if in.AuthType == "password" {
		blob.Value = in.Password
	} else {
		blob.Value = in.PrivateKey
	}
	data, _ := json.Marshal(blob)
	ct, err := s.crypt.Encrypt(string(data))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return ct, nil
}

func (s *HostService) decryptSecret(h *model.Host) (secretBlob, error) {
	var blob secretBlob
	if h.Secret == "" {
		return blob, errors.New("empty secret")
	}
	plain, err := s.crypt.Decrypt(h.Secret)
	if err != nil {
		return blob, err
	}
	if err := json.Unmarshal([]byte(plain), &blob); err != nil {
		return blob, fmt.Errorf("unmarshal: %w", err)
	}
	return blob, nil
}
