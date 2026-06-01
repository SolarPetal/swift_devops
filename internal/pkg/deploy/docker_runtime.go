package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// dockerRuntime Docker 容器部署模式（Sprint X.11）
type dockerRuntime struct {
	client *sshpkg.Client
}

func newDockerRuntime(client *sshpkg.Client) Runtime {
	return &dockerRuntime{client: client}
}

func (r *dockerRuntime) Name() string {
	return "docker"
}

// PrepareUnit Docker 模式：
//   - local-docker：镜像已在构建阶段 push，目标机只需 pull/run
//   - remote-docker：jar + Dockerfile 已上传到 DeployPath，这里在目标机 docker build -t
func (r *dockerRuntime) PrepareUnit(ctx context.Context, spec AppSpec) error {
	if !spec.RemoteDockerBuild {
		return nil
	}
	if strings.TrimSpace(spec.DockerImage) == "" {
		return fmt.Errorf("DockerImage 为空，无法远端构建")
	}
	buildArgs := shellJoinFields(spec.DockerBuildArgs)
	if buildArgs != "" {
		buildArgs += " "
	}
	cmd := fmt.Sprintf("cd %s && docker build %s-t %s -f %s .",
		ShellQuote(spec.DeployPath),
		buildArgs,
		ShellQuote(spec.DockerImage),
		ShellQuote(spec.DockerfilePath()),
	)
	res, err := r.client.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("远端 docker build 执行失败: %w", err)
	}
	if res.ExitCode != 0 {
		out := strings.TrimSpace(res.Stderr)
		if out == "" {
			out = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("远端 docker build 失败（exit=%d）：%s", res.ExitCode, out)
	}
	return nil
}

func (r *dockerRuntime) UnitArtifactPath(spec AppSpec) string {
	return fmt.Sprintf("docker container: devops-%s", spec.ServiceName)
}

// RestartAndWait 停止旧容器 → docker pull → docker run → 健康检查
func (r *dockerRuntime) RestartAndWait(ctx context.Context, spec AppSpec, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	containerName := fmt.Sprintf("devops-%s", spec.ServiceName)

	// 1. 停止并删除旧容器（如果存在）
	stopCmd := fmt.Sprintf("docker stop %s 2>/dev/null || true", containerName)
	if _, err := r.client.Exec(ctx, stopCmd); err != nil {
		return 0, fmt.Errorf("停止旧容器失败: %w", err)
	}

	rmCmd := fmt.Sprintf("docker rm %s 2>/dev/null || true", containerName)
	if _, err := r.client.Exec(ctx, rmCmd); err != nil {
		return 0, fmt.Errorf("删除旧容器失败: %w", err)
	}

	// 2. docker pull（local-docker 从镜像仓库拉取；remote-docker 已在目标机 build，跳过 pull）
	imageName := spec.DockerImage
	if imageName == "" {
		return 0, fmt.Errorf("DockerImage 为空，无法部署")
	}

	if !spec.RemoteDockerBuild {
		pullCmd := fmt.Sprintf("docker pull %s", ShellQuote(imageName))
		if _, err := r.client.Exec(ctx, pullCmd); err != nil {
			return 0, fmt.Errorf("拉取镜像失败: %w", err)
		}
	}

	// 3. docker run
	runArgs := spec.DockerRunArgs
	if runArgs == "" {
		// 默认参数：后台运行 + 自动重启 + 端口映射
		runArgs = fmt.Sprintf("-d --restart=unless-stopped -p %d:%d", spec.Port, spec.Port)
	}

	// 环境变量
	if len(spec.EnvVars) > 0 {
		for k, v := range spec.EnvVars {
			runArgs += fmt.Sprintf(" -e %s=%s", k, v)
		}
	}

	runCmd := fmt.Sprintf("docker run --name %s %s %s", ShellQuote(containerName), runArgs, ShellQuote(imageName))
	if _, err := r.client.Exec(ctx, runCmd); err != nil {
		return 0, fmt.Errorf("启动容器失败: %w", err)
	}

	// 4. 等待容器进入 running 状态
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inspectCmd := fmt.Sprintf("docker inspect -f '{{.State.Running}}' %s 2>/dev/null || echo false", containerName)
		out, err := r.client.Exec(ctx, inspectCmd)
		if err == nil && strings.TrimSpace(out.Stdout) == "true" {
			return time.Since(start), nil
		}
		time.Sleep(2 * time.Second)
	}

	return time.Since(start), fmt.Errorf("容器启动超时（%v）", timeout)
}

func shellJoinFields(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = ShellQuote(f)
	}
	return strings.Join(quoted, " ")
}

// StatusDump 返回容器状态 + 日志
func (r *dockerRuntime) StatusDump(ctx context.Context, spec AppSpec, lines int) string {
	containerName := fmt.Sprintf("devops-%s", spec.ServiceName)
	var sb strings.Builder

	// 容器状态
	inspectCmd := fmt.Sprintf("docker inspect %s 2>&1 || echo 'container not found'", containerName)
	out, _ := r.client.Exec(ctx, inspectCmd)
	sb.WriteString("=== Docker 容器状态 ===\n")
	sb.WriteString(out.Stdout)
	if out.Stderr != "" {
		sb.WriteString("\n[stderr]\n")
		sb.WriteString(out.Stderr)
	}
	sb.WriteString("\n\n")

	// 容器日志
	logsCmd := fmt.Sprintf("docker logs --tail %d %s 2>&1 || echo 'no logs'", lines, containerName)
	logs, _ := r.client.Exec(ctx, logsCmd)
	sb.WriteString("=== 容器日志（最后 ")
	sb.WriteString(fmt.Sprintf("%d", lines))
	sb.WriteString(" 行）===\n")
	sb.WriteString(logs.Stdout)
	if logs.Stderr != "" {
		sb.WriteString("\n[stderr]\n")
		sb.WriteString(logs.Stderr)
	}

	return sb.String()
}

// HumanizeError 识别常见 Docker 错误
func (r *dockerRuntime) HumanizeError(dump string) string {
	lower := strings.ToLower(dump)
	switch {
	case strings.Contains(lower, "no such image"):
		return "镜像不存在，请检查镜像名称或先构建镜像"
	case strings.Contains(lower, "port is already allocated"):
		return "端口已被占用，请检查端口配置或停止占用端口的进程"
	case strings.Contains(lower, "container not found"):
		return "容器不存在"
	case strings.Contains(lower, "permission denied"):
		return "权限不足，请确保用户在 docker 组或使用 sudo"
	case strings.Contains(lower, "cannot connect to the docker daemon"):
		return "无法连接 Docker 守护进程，请检查 Docker 是否已启动"
	default:
		return ""
	}
}

// Stop 停止容器
func (r *dockerRuntime) Stop(ctx context.Context, spec AppSpec) error {
	containerName := fmt.Sprintf("devops-%s", spec.ServiceName)
	stopCmd := fmt.Sprintf("docker stop %s 2>/dev/null || true", containerName)
	if _, err := r.client.Exec(ctx, stopCmd); err != nil {
		return fmt.Errorf("停止容器失败: %w", err)
	}

	rmCmd := fmt.Sprintf("docker rm %s 2>/dev/null || true", containerName)
	if _, err := r.client.Exec(ctx, rmCmd); err != nil {
		return fmt.Errorf("删除容器失败: %w", err)
	}

	return nil
}

// StopOther 清理其他部署模式的残留（systemd unit + nohup 进程）
func (r *dockerRuntime) StopOther(ctx context.Context, spec AppSpec) error {
	// 1. 停止 systemd unit
	unitName := fmt.Sprintf("devops-%s.service", spec.ServiceName)
	stopSystemdCmd := fmt.Sprintf("systemctl stop %s 2>/dev/null || true", unitName)
	r.client.Exec(ctx, stopSystemdCmd)

	disableSystemdCmd := fmt.Sprintf("systemctl disable %s 2>/dev/null || true", unitName)
	r.client.Exec(ctx, disableSystemdCmd)

	// 2. 停止 nohup 进程
	pidFile := fmt.Sprintf("%s/app.pid", spec.DeployPath)
	stopNohupCmd := fmt.Sprintf(`
		if [ -f %s ]; then
			pid=$(cat %s)
			kill $pid 2>/dev/null || true
			rm -f %s
		fi
	`, pidFile, pidFile, pidFile)
	r.client.Exec(ctx, stopNohupCmd)

	return nil
}
