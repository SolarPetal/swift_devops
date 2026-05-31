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
func BuildDockerImage(ctx context.Context, plan Plan, jarPath, serviceCode string) (string, error) {
	if plan.BuildMode != "local-docker" && plan.BuildMode != "remote-docker" {
		return "", fmt.Errorf("BuildMode %q 不支持 Docker 镜像构建", plan.BuildMode)
	}

	// 1. 确定镜像名
	imageName := plan.DockerImageName
	if imageName == "" {
		imageName = fmt.Sprintf("%s/%s", plan.AppCode, serviceCode)
	}
	imageTag := plan.DockerImageTag
	if imageTag == "" {
		imageTag = "latest"
	}
	fullImageName := fmt.Sprintf("%s:%s", imageName, imageTag)
	if plan.DockerRegistry != "" {
		fullImageName = fmt.Sprintf("%s/%s", plan.DockerRegistry, fullImageName)
	}

	// 2. 生成或使用自定义 Dockerfile
	dockerfilePath := filepath.Join(filepath.Dir(jarPath), "Dockerfile")
	var dockerfileContent string
	if plan.Dockerfile != "" {
		dockerfileContent = plan.Dockerfile
	} else {
		dockerfileContent = generateDefaultDockerfile(jarPath)
	}
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		return "", fmt.Errorf("write Dockerfile: %w", err)
	}
	if plan.LogWriter != nil {
		fmt.Fprintf(plan.LogWriter, "[docker] Dockerfile 已生成：%s\n", dockerfilePath)
	}

	// 3. docker build
	buildArgs := []string{"build", "-t", fullImageName, "-f", dockerfilePath}
	if plan.DockerBuildArgs != "" {
		buildArgs = append(buildArgs, strings.Fields(plan.DockerBuildArgs)...)
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

// generateDefaultDockerfile 生成默认的 Dockerfile
func generateDefaultDockerfile(jarPath string) string {
	jarName := filepath.Base(jarPath)
	return fmt.Sprintf(`# Auto-generated Dockerfile by swift-devops
FROM eclipse-temurin:17-jre-alpine

WORKDIR /app

# 复制 jar 文件
COPY %s /app/app.jar

# 暴露端口（默认 8080，可通过 docker run -p 覆盖）
EXPOSE 8080

# 启动应用
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
`, jarName)
}
