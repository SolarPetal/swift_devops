package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildDockerImage 构建 Docker 镜像（Sprint X.11）
//   - 前置条件：jar 已经构建完成
//   - 生成 Dockerfile（或使用自定义）
//   - docker build
//   - docker push（可选）
func BuildDockerImage(ctx context.Context, plan Plan, sp ServiceBuildSpec, jarPath, serviceCode, commitSHA string) (string, error) {
	if plan.BuildMode != "local-docker" {
		return "", fmt.Errorf("BuildMode %q 不支持 Docker 镜像构建", plan.BuildMode)
	}

	// 1. 确定镜像名
	fullImageName := ResolveDockerImageName(plan, sp, serviceCode, commitSHA)

	// 2. 生成或使用自定义 Dockerfile
	dockerfilePath := filepath.Join(filepath.Dir(jarPath), "Dockerfile")
	dockerfileContent := ResolveDockerfileSnapshot(plan, sp, jarPath).Content
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		return "", fmt.Errorf("write Dockerfile: %w", err)
	}
	if plan.LogWriter != nil {
		fmt.Fprintf(plan.LogWriter, "[docker] Dockerfile 已生成：%s\n", dockerfilePath)
	}

	// 3. docker build
	buildArgs := []string{"build", "-t", fullImageName, "-f", dockerfilePath}
	buildArgString := strings.TrimSpace(firstNonEmpty(sp.DockerBuildArgs, plan.DockerBuildArgs))
	if buildArgString != "" {
		buildArgs = append(buildArgs, strings.Fields(buildArgString)...)
	}
	buildArgs = append(buildArgs, filepath.Dir(jarPath))

	dockerBin := plan.DockerBin
	if dockerBin == "" {
		dockerBin = "docker"
	}

	if plan.LogWriter != nil {
		fmt.Fprintf(plan.LogWriter, "[docker] 构建镜像：%s %s\n", dockerBin, strings.Join(buildArgs, " "))
	}

	buildCmd := exec.CommandContext(ctx, dockerBin, buildArgs...)
	buildCmd.Stdout = plan.LogWriter
	buildCmd.Stderr = plan.LogWriter
	buildCmd.Env = plan.ExecEnv
	if buildCmd.Env == nil {
		buildCmd.Env = os.Environ()
	}

	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("docker build 失败: %w", err)
	}

	if plan.LogWriter != nil {
		fmt.Fprintf(plan.LogWriter, "[docker] 镜像构建成功：%s\n", fullImageName)
	}

	// 4. docker push（如果配置了镜像仓库）
	if plan.DockerRegistry != "" {
		// 先登录（如果有认证信息）
		if plan.DockerPushAuth != nil && plan.DockerPushAuth.Username != "" {
			loginCmd := exec.CommandContext(ctx, dockerBin, "login",
				"-u", plan.DockerPushAuth.Username,
				"-p", plan.DockerPushAuth.Password,
				plan.DockerRegistry)
			loginCmd.Stdout = plan.LogWriter
			loginCmd.Stderr = plan.LogWriter
			if err := loginCmd.Run(); err != nil {
				return "", fmt.Errorf("docker login 失败: %w", err)
			}
			if plan.LogWriter != nil {
				fmt.Fprintf(plan.LogWriter, "[docker] 登录镜像仓库成功：%s\n", plan.DockerRegistry)
			}
		}

		// 推送镜像
		pushCmd := exec.CommandContext(ctx, dockerBin, "push", fullImageName)
		pushCmd.Stdout = plan.LogWriter
		pushCmd.Stderr = plan.LogWriter
		if err := pushCmd.Run(); err != nil {
			return "", fmt.Errorf("docker push 失败: %w", err)
		}

		if plan.LogWriter != nil {
			fmt.Fprintf(plan.LogWriter, "[docker] 镜像推送成功：%s\n", fullImageName)
		}
	}

	return fullImageName, nil
}

// ResolveDockerImageName 生成完整镜像名（registry/name:tag）。
func ResolveDockerImageName(plan Plan, sp ServiceBuildSpec, serviceCode, commitSHA string) string {
	imageName := strings.TrimSpace(firstNonEmpty(sp.DockerImageName, plan.DockerImageName))
	if imageName == "" {
		imageName = fmt.Sprintf("%s/%s", plan.AppCode, serviceCode)
	}
	imageTag := strings.TrimSpace(firstNonEmpty(sp.DockerImageTag, plan.DockerImageTag))
	if imageTag == "" {
		imageTag = "latest"
	}
	imageTag = renderDockerToken(imageTag, plan, sp, serviceCode, commitSHA, "")
	fullImageName := fmt.Sprintf("%s:%s", imageName, imageTag)
	registry := strings.TrimSpace(firstNonEmpty(sp.DockerRegistry, plan.DockerRegistry))
	if registry != "" {
		fullImageName = fmt.Sprintf("%s/%s", registry, fullImageName)
	}
	return fullImageName
}

// ResolveDockerfileSnapshot 渲染 Dockerfile 模板，并返回可落库快照。
func ResolveDockerfileSnapshot(plan Plan, sp ServiceBuildSpec, jarPath string) DockerfileSnapshot {
	name := strings.TrimSpace(sp.DockerfileName)
	if name == "" {
		name = "Dockerfile"
	}
	content := strings.TrimSpace(sp.DockerfileContent)
	if content == "" {
		content = strings.TrimSpace(plan.Dockerfile)
	}
	if content == "" {
		content = DefaultDockerfileTemplate()
	}
	content = renderDockerToken(content, plan, sp, sp.ServiceCode, "", filepath.Base(jarPath))
	return DockerfileSnapshot{Name: name, Content: content}
}

// DefaultDockerfileTemplate 生成可展示/可编辑的默认 Dockerfile 模板。
func DefaultDockerfileTemplate() string {
	return `# Default Dockerfile template by swift-devops
FROM eclipse-temurin:21-jdk

WORKDIR /app

COPY {{JAR_FILE}} /app/app.jar

EXPOSE {{PORT}}

ENTRYPOINT ["java", "-jar", "/app/app.jar"]
`
}

func renderDockerToken(s string, plan Plan, sp ServiceBuildSpec, serviceCode, commitSHA, jarFile string) string {
	short := commitSHA
	if len(short) > 7 {
		short = short[:7]
	}
	port := sp.Port
	if port <= 0 {
		port = 8080
	}
	repl := map[string]string{
		"{{APP_CODE}}":      plan.AppCode,
		"{{SERVICE_CODE}}":  serviceCode,
		"{{JAR_FILE}}":      jarFile,
		"{{PORT}}":          fmt.Sprintf("%d", port),
		"{{GIT_SHA}}":       commitSHA,
		"{{GIT_SHORT_SHA}}": short,
		"{{BUILD_ID}}":      fmt.Sprintf("%d", plan.BuildID),
		"{sha}":             short,
		"{build_id}":        fmt.Sprintf("%d", plan.BuildID),
	}
	out := s
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// generateDefaultDockerfile 生成默认的 Dockerfile（兼容旧测试/调用）。
func generateDefaultDockerfile(jarPath string) string {
	jarName := filepath.Base(jarPath)
	plan := Plan{}
	return renderDockerToken(DefaultDockerfileTemplate(), plan, ServiceBuildSpec{Port: 8080}, "default", "", jarName)
}
