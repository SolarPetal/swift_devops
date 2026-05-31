package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// Runtime 一台主机上一个服务的「部署 + 生命周期」原语抽象（Sprint X.10 + X.11）。
//
// 设计意图：把"写启动文件 + 重启 + 等 active + 诊断 + 停止"从 deployHost 里抽出来，
// 让 systemd / nohup / docker（以及未来 k8s）各自实现，deployHost 只管编排阶段。
//
// 命名约定：本接口的"Unit"沿用 systemd 术语，对 nohup 模式而言指 start.sh + stop.sh 这套启动脚本，
// 对 docker 模式而言指容器。
type Runtime interface {
	// Name 模式标识："systemd" / "nohup" / "docker"，用于日志和错误归因。
	Name() string

	// PrepareUnit 在远端写下启动需要的文件（systemd 写 unit，nohup 写 start.sh / stop.sh 与 pid 目录，docker 无需准备）。
	PrepareUnit(ctx context.Context, spec AppSpec) error

	// UnitArtifactPath 返回 PrepareUnit 写入的"主要标识文件"路径，用于 step.detail 展示给前端。
	UnitArtifactPath(spec AppSpec) string

	// RestartAndWait 重启服务并等待进入 active 状态。
	// 返回等待耗时（用于 step.detail 展示）和错误。
	RestartAndWait(ctx context.Context, spec AppSpec, timeout time.Duration) (waited time.Duration, err error)

	// StatusDump 失败诊断：返回供前端 Drawer 展示的人类可读文本（status 摘要 + 日志末尾 N 行）。
	StatusDump(ctx context.Context, spec AppSpec, lines int) string

	// HumanizeError 从 dump 识别常见错误码，返回友好中文 hint。无命中返回空串。
	HumanizeError(dump string) string

	// Stop 停止服务（蓝绿切流保留旧组、解绑、显式停服用）。
	Stop(ctx context.Context, spec AppSpec) error

	// StopOther best-effort 清理"另一种部署模式"的残留：
	//   - systemdRuntime 调用时 → 干掉同 spec 的 nohup pid / 进程 + docker 容器
	//   - nohupRuntime 调用时 → systemctl stop + disable 同名 unit + docker 容器
	//   - dockerRuntime 调用时 → systemctl stop + disable 同名 unit + nohup pid / 进程
	// 失败不阻塞主流程，仅返回 nil（调用方可忽略错误）。
	StopOther(ctx context.Context, spec AppSpec) error
}

// DeployMode 取值常量。
const (
	DeployModeSystemd = "systemd"
	DeployModeNohup   = "nohup"
	DeployModeDocker  = "docker" // Sprint X.11
)

// NormalizeDeployMode 把空串/未知值归一化到 "systemd"。
// 调用方拿空串时应当先用本函数兜底，保证向后兼容。
func NormalizeDeployMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", DeployModeSystemd:
		return DeployModeSystemd
	case DeployModeNohup:
		return DeployModeNohup
	case DeployModeDocker:
		return DeployModeDocker
	default:
		return DeployModeSystemd
	}
}

// ValidateDeployMode 用于 API 层校验入参，未知值返回错误。
// 空串视为合法（service 字段空 → 回退 app 字段 → 回退 systemd）。
func ValidateDeployMode(mode string) error {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", DeployModeSystemd, DeployModeNohup, DeployModeDocker:
		return nil
	}
	return fmt.Errorf("deploy_mode 仅支持 systemd / nohup / docker（实际：%q）", mode)
}

// PickRuntime 工厂：按 mode 返回对应 Runtime 实现。
// 空串/未知 → systemd（向后兼容）。
func PickRuntime(mode string, client *sshpkg.Client) Runtime {
	switch NormalizeDeployMode(mode) {
	case DeployModeNohup:
		return newNohupRuntime(client)
	case DeployModeDocker:
		return newDockerRuntime(client)
	default:
		return newSystemdRuntime(client)
	}
}
