package deploy

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// nohupRuntime 实现 Runtime 接口的"nohup java -jar"模式（Sprint X.10）。
//
// 与 systemd 的差异：
//   - 不需要 root / sudo NOPASSWD：写文件 + kill 自己启的进程即可
//   - 没有 RestartSec 自愈：crash 后进程消失，需用户自己加 cron 或 monit
//   - 没有 journalctl：日志写 <deploy_path>/logs/stdout.log，状态诊断靠 tail + ps
//
// 落盘文件（全部在 <deploy_path> 下，互不污染同主机其它应用）：
//   - start.sh   启动脚本（含 env 导出 + nohup java -jar）
//   - stop.sh    停止脚本（kill -15 → 等 → kill -9）
//   - app.pid    当前进程 PID（PrepareUnit 不写，由 start.sh 写）
//   - logs/stdout.log  应用 stdout/stderr 合并日志
type nohupRuntime struct {
	client     *sshpkg.Client
	dispatcher *Dispatcher
}

func newNohupRuntime(client *sshpkg.Client) *nohupRuntime {
	return &nohupRuntime{
		client:     client,
		dispatcher: NewDispatcher(client.SSHClient()),
	}
}

func (r *nohupRuntime) Name() string { return DeployModeNohup }

func (r *nohupRuntime) UnitArtifactPath(spec AppSpec) string {
	return startScriptPath(spec)
}

// PrepareUnit 渲染并上传 start.sh + stop.sh，确保 logs/ 目录存在。
func (r *nohupRuntime) PrepareUnit(ctx context.Context, spec AppSpec) error {
	startText := renderStartScript(spec)
	stopText := renderStopScript(spec)
	startPath := startScriptPath(spec)
	stopPath := stopScriptPath(spec)
	logDir := path.Join(spec.DeployPath, "logs")

	// 提前建好 logs/ 与 deploy_path/（dispatcher.WriteFile 自动建父目录，但 logs 子目录要单独建）
	if err := r.dispatcher.MkdirAll(logDir); err != nil {
		return fmt.Errorf("mkdir logs: %w", err)
	}
	if err := r.dispatcher.WriteFile(startPath, startText, 0o755); err != nil {
		return fmt.Errorf("write start.sh: %w", err)
	}
	if err := r.dispatcher.WriteFile(stopPath, stopText, 0o755); err != nil {
		return fmt.Errorf("write stop.sh: %w", err)
	}
	return nil
}

// RestartAndWait stop.sh → start.sh → 轮询 pid 文件 + 进程存活。
// 服务被认为"启动成功"的条件：app.pid 存在 + kill -0 命中。
// 健康探针由上层 deployHost 单独跑（http GET），不在此判定。
func (r *nohupRuntime) RestartAndWait(ctx context.Context, spec AppSpec, timeout time.Duration) (time.Duration, error) {
	start := time.Now()

	// 1. 停旧进程（如果有）
	stopCmd := fmt.Sprintf("bash %s", ShellQuote(stopScriptPath(spec)))
	if res, err := r.client.Exec(ctx, stopCmd); err != nil {
		return time.Since(start), fmt.Errorf("exec stop.sh: %w", err)
	} else if res.ExitCode != 0 {
		// stop.sh 即使脚本里没进程也应当返回 0；非 0 视为脚本本身有问题
		return time.Since(start), fmt.Errorf("stop.sh exit=%d stderr=%s",
			res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	// 2. 启动新进程
	//    用 `bash -lc` 拉一份 login shell 的 env（PATH 等），避免 ssh 默认非交互 PATH 太窄。
	//    cd 到 deploy_path 在脚本里也做了一次，双保险。
	startCmd := fmt.Sprintf("bash -lc %s", ShellQuote("bash "+ShellQuote(startScriptPath(spec))))
	if res, err := r.client.Exec(ctx, startCmd); err != nil {
		return time.Since(start), fmt.Errorf("exec start.sh: %w", err)
	} else if res.ExitCode != 0 {
		return time.Since(start), fmt.Errorf("start.sh exit=%d stderr=%s stdout=%s",
			res.ExitCode, strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout))
	}

	// 3. 轮询：app.pid 存在 + 进程活着
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	deadline := start.Add(timeout)
	for {
		alive, _ := r.processAlive(ctx, spec)
		if alive {
			return time.Since(start), nil
		}
		if time.Now().After(deadline) {
			return time.Since(start), fmt.Errorf("nohup 进程在 %s 内未稳定运行（pid 文件或进程已退出）", timeout)
		}
		select {
		case <-ctx.Done():
			return time.Since(start), ctx.Err()
		case <-tick.C:
		}
	}
}

// processAlive 检查 app.pid 对应的进程是否还活着。
func (r *nohupRuntime) processAlive(ctx context.Context, spec AppSpec) (bool, int) {
	pidPath := pidFilePath(spec)
	cmd := fmt.Sprintf(`
if [ -f %s ]; then
  PID=$(cat %s 2>/dev/null)
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    echo "$PID"
    exit 0
  fi
fi
exit 1
`, ShellQuote(pidPath), ShellQuote(pidPath))
	res, err := r.client.Exec(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return false, 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(res.Stdout))
	return pid > 0, pid
}

// StatusDump tail 日志 + ps 进程信息。
func (r *nohupRuntime) StatusDump(ctx context.Context, spec AppSpec, lines int) string {
	if lines <= 0 {
		lines = 200
	}
	logPath := path.Join(spec.DeployPath, "logs", "stdout.log")
	pidPath := pidFilePath(spec)

	var b strings.Builder

	// 1. 进程信息（如果 pid 还在）
	psCmd := fmt.Sprintf(`
if [ -f %s ]; then
  PID=$(cat %s 2>/dev/null)
  echo "pid file: %s = $PID"
  if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
    ps -p "$PID" -o pid,user,etime,rss,cmd 2>/dev/null || true
  else
    echo "(进程已退出，pid 文件残留)"
  fi
else
  echo "pid file %s 不存在（应用从未启动或已被清理）"
fi
`, ShellQuote(pidPath), ShellQuote(pidPath), pidPath, pidPath)
	if res, err := r.client.Exec(ctx, psCmd); err == nil {
		b.WriteString("=== process status ===\n")
		b.WriteString(strings.TrimSpace(res.Stdout))
		if strings.TrimSpace(res.Stderr) != "" {
			b.WriteString("\n[stderr] ")
			b.WriteString(strings.TrimSpace(res.Stderr))
		}
		b.WriteString("\n")
	}

	// 2. 日志末尾 N 行
	tailCmd := fmt.Sprintf("tail -n %d %s 2>&1 || true", lines, ShellQuote(logPath))
	if res, err := r.client.Exec(ctx, tailCmd); err == nil {
		b.WriteString("\n=== tail ")
		b.WriteString(logPath)
		b.WriteString(" (last ")
		fmt.Fprintf(&b, "%d", lines)
		b.WriteString(" lines) ===\n")
		b.WriteString(strings.TrimSpace(res.Stdout))
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

// HumanizeError 识别 nohup 模式下应用日志里的常见错误。
// 与 systemd 不同：这里读到的是 Java 应用输出，不是 systemd 状态码。
func (r *nohupRuntime) HumanizeError(dump string) string {
	if dump == "" {
		return ""
	}
	lower := strings.ToLower(dump)
	switch {
	case strings.Contains(lower, "address already in use"),
		strings.Contains(lower, "bindexception"):
		return "⚠ 端口已被占用。常见原因：上一进程未真正退出（kill 后未释放），或同主机已有别的应用占用此端口。\n\n"
	case strings.Contains(lower, "outofmemoryerror"),
		strings.Contains(lower, "java.lang.outofmemory"):
		return "⚠ Java 进程 OOM。建议调大 -Xmx 或排查内存泄漏；nohup 模式下 OOM 不会自动重启，需要手动拉起。\n\n"
	case strings.Contains(lower, "classnotfoundexception"),
		strings.Contains(lower, "noclassdeffounderror"):
		return "⚠ Class not found。常见原因：jar 文件不完整 / 依赖缺失。检查上传 MD5 是否一致。\n\n"
	case strings.Contains(lower, "no such file or directory") && strings.Contains(lower, "java"):
		return "⚠ Java 可执行文件不存在。检查主机或应用配置的 java_path。\n\n"
	case strings.Contains(dump, "pid 文件或进程已退出"),
		strings.Contains(lower, "进程已退出"):
		return "⚠ 进程启动后立即退出。查看下方应用日志定位异常（Spring Boot 启动失败一般会打出 Application run failed）。\n\n"
	}
	return ""
}

// Stop 调用 stop.sh 停止当前服务。
func (r *nohupRuntime) Stop(ctx context.Context, spec AppSpec) error {
	cmd := fmt.Sprintf("bash %s", ShellQuote(stopScriptPath(spec)))
	res, err := r.client.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("exec stop.sh: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("stop.sh exit=%d stderr=%s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// StopOther 进入 nohup 模式前，best-effort 清理 systemd 模式残留：
// systemctl stop + disable 同名 unit。
// 任何子命令失败都不返回错误，避免阻塞主流程。
func (r *nohupRuntime) StopOther(ctx context.Context, spec AppSpec) error {
	unitName := spec.UnitName()
	// `|| true` 保证未启用 / 不存在的 unit 不会让整条命令失败
	cmd := fmt.Sprintf(`systemctl stop %s 2>/dev/null || true; systemctl disable %s 2>/dev/null || true`,
		unitName, unitName)
	_, _ = r.client.Exec(ctx, cmd)
	return nil
}

// startScriptPath / stopScriptPath / pidFilePath 共享的路径拼装。
// 同一 spec（同 AppCode + DeployPath）三处路径必须一致，否则 start.sh 写 pid 后 stop.sh 找不到。
func startScriptPath(spec AppSpec) string { return path.Join(spec.DeployPath, "start.sh") }
func stopScriptPath(spec AppSpec) string  { return path.Join(spec.DeployPath, "stop.sh") }
func pidFilePath(spec AppSpec) string     { return path.Join(spec.DeployPath, "app.pid") }

// renderStartScript 生成 start.sh：
//   - cd 到 deploy_path，避免相对路径踩坑
//   - 准备 logs/ 目录
//   - export env vars（按 key 排序，输出稳定）
//   - nohup java ... -jar app.jar --server.port=<port>，stdout/stderr 合并到 logs/stdout.log
//   - $! 写到 app.pid，让 stop.sh 找得到
//   - 末尾 sleep 0.5 确保 nohup 子进程已 fork 出来 + pid 文件已 flush
func renderStartScript(spec AppSpec) string {
	javaPath := strings.TrimSpace(spec.JavaPath)
	if javaPath == "" {
		javaPath = "/usr/bin/java"
	}
	jvmArgs := strings.TrimSpace(spec.JvmArgs)
	user := strings.TrimSpace(spec.User)

	// env vars 按 key 排序，输出稳定（便于 diff 和测试）
	envKeys := make([]string, 0, len(spec.EnvVars))
	for k := range spec.EnvVars {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	var envLines strings.Builder
	for _, k := range envKeys {
		envLines.WriteString(fmt.Sprintf("export %s=%s\n", k, ShellQuote(spec.EnvVars[k])))
	}

	// 如果配置了 systemd_user，nohup 模式下用 sudo -u <user> 切用户启动
	// 没配则用当前 ssh 登录用户（一般 root 或 deployer）
	startPrefix := ""
	if user != "" && user != "root" {
		// runuser 比 sudo 兼容性好（不要求 NOPASSWD），但需要 root；
		// 当前用户是 root → runuser，不是 root → 退化为不切用户（打个警告日志在 stdout）
		startPrefix = fmt.Sprintf(`if [ "$(id -un)" = "root" ]; then RUN_AS="runuser -u %s --"; else echo "[warn] 当前 SSH 用户非 root，无法切到 %s，按当前用户启动" >&2; RUN_AS=""; fi
`, user, user)
	} else {
		startPrefix = `RUN_AS=""
`
	}

	jarPath := spec.JarPath()
	logFile := path.Join(spec.DeployPath, "logs", "stdout.log")
	pidPath := pidFilePath(spec)

	return fmt.Sprintf(`#!/bin/bash
# Auto-generated by swift-devops (Sprint X.10 / nohup mode)
# 修改本文件无效——下次部署会被覆盖。要改启动参数请在 UI 修改 jvm_args / env_vars / port。
set -e

cd %s
mkdir -p logs

%s
%s$RUN_AS nohup %s %s -jar %s --server.port=%d >> %s 2>&1 &
echo $! > %s

# 等 nohup 子进程 fork + pid flush，再让脚本退出（让上层 SSH 立刻拿到 exit 0）
sleep 0.5
echo "[swift-devops] started pid=$(cat %s) jar=%s port=%d log=%s"
`,
		ShellQuote(spec.DeployPath),
		envLines.String(),
		startPrefix,
		javaPath, jvmArgs, ShellQuote(jarPath), spec.Port,
		ShellQuote(logFile),
		ShellQuote(pidPath),
		ShellQuote(pidPath), jarPath, spec.Port, logFile,
	)
}

// renderStopScript 生成 stop.sh：
//   - 没 pid 文件 → 直接 exit 0（视为已停）
//   - 进程不存在 → 清理残留 pid 文件后 exit 0
//   - 进程存在 → kill -15 → 最多等 10s → kill -9 → 删 pid 文件
func renderStopScript(spec AppSpec) string {
	pidPath := pidFilePath(spec)
	return fmt.Sprintf(`#!/bin/bash
# Auto-generated by swift-devops (Sprint X.10 / nohup mode)
set -e

PID_FILE=%s

if [ ! -f "$PID_FILE" ]; then
  echo "[swift-devops] no pid file at $PID_FILE, nothing to stop"
  exit 0
fi

PID=$(cat "$PID_FILE" 2>/dev/null)
if [ -z "$PID" ]; then
  echo "[swift-devops] pid file empty, cleaning up"
  rm -f "$PID_FILE"
  exit 0
fi

if ! kill -0 "$PID" 2>/dev/null; then
  echo "[swift-devops] pid $PID not running, cleaning up stale pid file"
  rm -f "$PID_FILE"
  exit 0
fi

echo "[swift-devops] sending SIGTERM to pid $PID"
kill -15 "$PID" 2>/dev/null || true

for i in $(seq 1 20); do
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "[swift-devops] pid $PID exited gracefully after $((i*500))ms"
    rm -f "$PID_FILE"
    exit 0
  fi
  sleep 0.5
done

echo "[swift-devops] pid $PID still alive after 10s, sending SIGKILL"
kill -9 "$PID" 2>/dev/null || true
sleep 0.5
rm -f "$PID_FILE"
echo "[swift-devops] pid $PID force-killed"
`, ShellQuote(pidPath))
}
