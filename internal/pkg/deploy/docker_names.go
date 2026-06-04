package deploy

import (
	"fmt"
	"regexp"
	"strings"
)

var dockerContainerNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ContainerName 返回 Docker 部署使用的容器名。
// 用户显式配置 DockerContainerName 时使用配置值；否则沿用历史默认 devops-<service>。
func (s AppSpec) ContainerName() string {
	name := strings.TrimSpace(s.DockerContainerName)
	if name != "" {
		return name
	}
	serviceName := strings.TrimSpace(s.ServiceName)
	if serviceName == "" {
		serviceName = strings.TrimSpace(s.AppCode)
	}
	if serviceName == "" {
		serviceName = "default"
	}
	return fmt.Sprintf("devops-%s", serviceName)
}

// ValidateDockerContainerName 校验 docker run --name 可接受的容器名。
func ValidateDockerContainerName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("容器名不能为空")
	}
	if !dockerContainerNameRE.MatchString(name) {
		return fmt.Errorf("容器名必须以字母或数字开头，长度 1-128，仅允许字母、数字、点、下划线、连字符")
	}
	return nil
}

// RenderDockerRuntimeNameTemplate 渲染运行时命名模板。
//
// 支持：
//   - {{APP_CODE}} / {{SERVICE_CODE}}
//   - {app} / {service}
func RenderDockerRuntimeNameTemplate(raw, appCode, serviceCode string) string {
	out := strings.TrimSpace(raw)
	repl := map[string]string{
		"{{APP_CODE}}":     strings.TrimSpace(appCode),
		"{{SERVICE_CODE}}": strings.TrimSpace(serviceCode),
		"{app}":            strings.TrimSpace(appCode),
		"{service}":        strings.TrimSpace(serviceCode),
	}
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return strings.TrimSpace(out)
}
