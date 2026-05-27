package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// systemdRuntime 把现有的 RenderUnit + Dispatcher.WriteFile + Systemctl 整套包装成 Runtime。
// 行为与 Sprint X.10 之前完全一致：写 /etc/systemd/system/devops-<app>.service，
// daemon-reload + enable + restart + WaitActive。
type systemdRuntime struct {
	client     *sshpkg.Client
	dispatcher *Dispatcher
	sysctl     *Systemctl
}

func newSystemdRuntime(client *sshpkg.Client) *systemdRuntime {
	return &systemdRuntime{
		client:     client,
		dispatcher: NewDispatcher(client.SSHClient()),
		sysctl:     NewSystemctl(client),
	}
}

func (r *systemdRuntime) Name() string { return DeployModeSystemd }

func (r *systemdRuntime) UnitArtifactPath(spec AppSpec) string {
	return spec.UnitPath()
}

// PrepareUnit 渲染 unit 文本并写到远端 /etc/systemd/system/。
func (r *systemdRuntime) PrepareUnit(ctx context.Context, spec AppSpec) error {
	unitText, err := RenderUnit(spec)
	if err != nil {
		return fmt.Errorf("render unit: %w", err)
	}
	if err := r.dispatcher.WriteFile(spec.UnitPath(), unitText, 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	return nil
}

// RestartAndWait daemon-reload + enable + restart + WaitActive。
func (r *systemdRuntime) RestartAndWait(ctx context.Context, spec AppSpec, timeout time.Duration) (time.Duration, error) {
	if err := r.sysctl.DaemonReload(ctx); err != nil {
		return 0, fmt.Errorf("daemon-reload: %w", err)
	}
	if err := r.sysctl.EnableAndRestart(ctx, spec.UnitName()); err != nil {
		return 0, err
	}
	_, waited, err := r.sysctl.WaitActive(ctx, spec.UnitName(), timeout)
	return waited, err
}

func (r *systemdRuntime) StatusDump(ctx context.Context, spec AppSpec, lines int) string {
	return r.sysctl.StatusDump(ctx, spec.UnitName(), lines)
}

// HumanizeError 从 systemctl status / journalctl 输出里识别常见错误码，
// 返回友好的中文 hint（"⚠ Java 可执行文件不存在/权限不足..."），便于用户立刻定位。
// 没命中任何已知模式时返回空串，调用方应直接展示原始 dump。
//
// Sprint 3.7 先支持 5 个最常见的 systemd 错误模式，覆盖 80% 部署失败场景。
// Sprint X.10：从 strategy/common.go 搬到这里，作为 systemdRuntime 的实现细节。
func (r *systemdRuntime) HumanizeError(dump string) string {
	if dump == "" {
		return ""
	}
	lower := strings.ToLower(dump)
	switch {
	case strings.Contains(dump, "status=203/EXEC"):
		return "⚠ Java 可执行文件不存在或权限不足（systemd 203/EXEC）。请检查主机配置的 java_path，或确认远端已装 JDK。\n\n"
	case strings.Contains(dump, "status=200/CHDIR"):
		return "⚠ WorkingDirectory 不存在或无权访问（systemd 200/CHDIR）。请确认应用 deploy_path 在远端可写。\n\n"
	case strings.Contains(dump, "status=200/USER"):
		return "⚠ systemd User= 在远端不存在（200/USER）。请去掉应用的 systemd_user，或在远端创建该用户。\n\n"
	case strings.Contains(dump, "status=200/EXEC"):
		return "⚠ 进程启动时 exec 失败（200/EXEC）。常见原因：jar 文件损坏 / class not found。检查 jar 完整性与 JVM 参数。\n\n"
	case strings.Contains(dump, "status=143"):
		return "ℹ 进程收到 SIGTERM 退出（143）。一般是正常停止流程；若不是预期，检查 RestartSec 与外部信号源。\n\n"
	case strings.Contains(lower, "killed") && strings.Contains(lower, "signal=kill"),
		strings.Contains(dump, "status=137"),
		strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "oom-killer"):
		return "⚠ 进程被 OOM Killer 杀死（status=137 或 signal=KILL）。建议调大 -Xmx 或扩容主机内存。\n\n"
	case strings.Contains(lower, "address already in use"),
		strings.Contains(lower, "bindexception"),
		strings.Contains(dump, "Port already in use"):
		return "⚠ 端口已被占用。常见原因：上一进程未释放，或同主机已有别的应用占用此端口。检查 server.port 配置。\n\n"
	}
	return ""
}

func (r *systemdRuntime) Stop(ctx context.Context, spec AppSpec) error {
	return r.sysctl.Stop(ctx, spec.UnitName())
}

// StopOther 进入 systemd 模式前，best-effort 清理 nohup 模式残留：
// 读 <deploy_path>/app.pid → kill -15 → kill -9 → 删 pid 文件。
// 任何子命令失败都不返回错误，避免阻塞主流程。
func (r *systemdRuntime) StopOther(ctx context.Context, spec AppSpec) error {
	pidPath := pidFilePath(spec)
	cmd := fmt.Sprintf(`
if [ -f %s ]; then
  PID=$(cat %s 2>/dev/null)
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    kill -15 "$PID" 2>/dev/null || true
    for i in $(seq 1 20); do kill -0 "$PID" 2>/dev/null || break; sleep 0.5; done
    kill -9 "$PID" 2>/dev/null || true
  fi
  rm -f %s 2>/dev/null || true
fi
`, ShellQuote(pidPath), ShellQuote(pidPath), ShellQuote(pidPath))
	_, _ = r.client.Exec(ctx, cmd)
	return nil
}
