package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maven_docker.go —— Sprint 5.6：Docker 隔离构建。
//
// 只把 mvn 这一段放进容器（git clone 仍在宿主机，凭证不进容器）：
//   docker run --rm --user <uid>:<gid> -e HOME=/tmp \
//     -v <WorkDir>:/src [-v <MavenCacheDir>:/m2] -w /src \
//     <Image> mvn -B -ntp [-Dmaven.repo.local=/m2] <args>
//
// 设计要点：
//   - --user 用宿主机 uid:gid 跑，避免容器 root 写出的 jar 宿主机删不掉（构建工作区清理失败）
//   - .m2 挂到 /m2 + -Dmaven.repo.local=/m2：复用缓存，且避开非 root HOME 不可写的问题
//   - 源码经 bind mount 出现在容器 /src，jar 落 /src/target = 宿主机 WorkDir/target，
//     提取仍用宿主机侧 findArtifactJar（与本机模式同一套逻辑）

// DockerMavenOptions docker 容器内跑 mvn package 的输入。
type DockerMavenOptions struct {
	WorkDir       string // 宿主机源码目录（含 pom.xml），bind mount 进容器 /src
	Image         string // 构建镜像（必填）
	DockerBin     string // docker 可执行；空 = "docker"
	ExtraArgs     string // mvn 额外参数；空 = "clean package -DskipTests"
	MavenCacheDir string // 宿主机 .m2 缓存目录，bind mount 到 /m2；空 = 不挂（每次重下依赖）
	LogWriter     io.Writer
	Timeout       time.Duration // 0 = 30 分钟
}

// MvnPackageDocker 在 docker 容器里跑 mvn package。jar 落在 bind mount 的 WorkDir/target。
func MvnPackageDocker(ctx context.Context, opts DockerMavenOptions) error {
	if strings.TrimSpace(opts.WorkDir) == "" {
		return errors.New("work_dir is empty")
	}
	if strings.TrimSpace(opts.Image) == "" {
		return errors.New("docker image is empty")
	}
	if _, err := os.Stat(filepath.Join(opts.WorkDir, "pom.xml")); err != nil {
		return fmt.Errorf("pom.xml not found in %s: %w", opts.WorkDir, err)
	}
	dockerBin := strings.TrimSpace(opts.DockerBin)
	if dockerBin == "" {
		dockerBin = "docker"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	logw := opts.LogWriter
	if logw == nil {
		logw = io.Discard
	}

	args := buildDockerRunArgs(opts)
	dctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	fmt.Fprintf(logw, "$ %s %s\n", dockerBin, strings.Join(args, " "))
	cmd := exec.CommandContext(dctx, dockerBin, args...)
	cmd.Stdout = logw
	cmd.Stderr = logw
	cmd.Env = os.Environ() // docker CLI 用宿主机环境（DOCKER_HOST 等）
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker mvn: %w", err)
	}
	return nil
}

// buildDockerRunArgs 纯函数拼 docker run 参数（便于单测，不实际拉容器）。
func buildDockerRunArgs(opts DockerMavenOptions) []string {
	args := []string{
		"run", "--rm",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"-e", "HOME=/tmp",
		"-v", opts.WorkDir + ":/src",
		"-w", "/src",
	}
	mvnArgs := []string{"-B", "-ntp"}
	if strings.TrimSpace(opts.MavenCacheDir) != "" {
		args = append(args, "-v", opts.MavenCacheDir+":/m2")
		mvnArgs = append(mvnArgs, "-Dmaven.repo.local=/m2")
	}
	args = append(args, opts.Image, "mvn")
	args = append(args, mvnArgs...)
	extra := strings.TrimSpace(opts.ExtraArgs)
	if extra == "" {
		extra = "clean package -DskipTests"
	}
	args = append(args, strings.Fields(extra)...)
	return args
}
