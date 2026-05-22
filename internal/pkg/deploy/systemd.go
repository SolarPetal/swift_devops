package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// AppSpec 渲染 systemd 单元所需的应用规格
type AppSpec struct {
	AppCode        string            // → devops-<AppCode>.service
	DeployPath     string            // jar 解析路径（绝对路径）
	JarFileName    string            // 默认 "app.jar"
	JvmArgs        string            // 透传到 java 命令行
	Port           int               // --server.port
	HealthCheckURL string            // 留给 health 探针，不进 unit
	EnvVars        map[string]string // 解析后的 env vars
	User           string            // systemd User=，留空走默认（root）
}

// UnitName 单元名（不含路径）
func (s AppSpec) UnitName() string {
	return fmt.Sprintf("devops-%s.service", s.AppCode)
}

// UnitPath 远端绝对路径
func (s AppSpec) UnitPath() string {
	return "/etc/systemd/system/" + s.UnitName()
}

// JarPath jar 在远端的绝对路径
func (s AppSpec) JarPath() string {
	name := s.JarFileName
	if name == "" {
		name = "app.jar"
	}
	return path.Join(s.DeployPath, name)
}

// ParseEnvVarsJSON 把 Application.EnvVars 的 JSON 字符串解析为 map。
// 空串返回空 map，非对象返回错误。
func ParseEnvVarsJSON(s string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("env_vars not a JSON object: %w", err)
	}
	return out, nil
}

const unitTmpl = `[Unit]
Description=swift-devops managed: {{.AppCode}}
After=network.target

[Service]
Type=simple
{{- if .User}}
User={{.User}}
{{- end}}
WorkingDirectory={{.DeployPath}}
{{- range $k, $v := .EnvVarsSorted}}
Environment="{{$k}}={{$v}}"
{{- end}}
ExecStart=/usr/bin/java {{.JvmArgs}} -jar {{.JarPath}} --server.port={{.Port}}
Restart=on-failure
RestartSec=5
SuccessExitStatus=143

[Install]
WantedBy=multi-user.target
`

// RenderUnit 渲染 systemd unit 文件文本。
//   - JvmArgs 原样拼接到 java 后；调用方需保证字符串安全
//   - EnvVars 按 key 排序，输出稳定（便于 diff / 测试）
func RenderUnit(s AppSpec) (string, error) {
	type tmplData struct {
		AppCode       string
		DeployPath    string
		JvmArgs       string
		Port          int
		JarPath       string
		User          string
		EnvVarsSorted map[string]string // text/template range 按字典序遍历 map，天然稳定
	}
	t, err := template.New("unit").Parse(unitTmpl)
	if err != nil {
		return "", err
	}
	// 拷一份 EnvVars，避免外部修改影响输出（值已是 string，浅拷即可）
	envs := make(map[string]string, len(s.EnvVars))
	keys := make([]string, 0, len(s.EnvVars))
	for k := range s.EnvVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		envs[k] = s.EnvVars[k]
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, tmplData{
		AppCode:       s.AppCode,
		DeployPath:    s.DeployPath,
		JvmArgs:       strings.TrimSpace(s.JvmArgs),
		Port:          s.Port,
		JarPath:       s.JarPath(),
		User:          strings.TrimSpace(s.User),
		EnvVarsSorted: envs,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Systemctl 通过 SSH 调远端 systemctl。
type Systemctl struct {
	client *sshpkg.Client
}

func NewSystemctl(c *sshpkg.Client) *Systemctl {
	return &Systemctl{client: c}
}

// DaemonReload 重新加载 systemd 单元。
func (s *Systemctl) DaemonReload(ctx context.Context) error {
	return s.run(ctx, "systemctl daemon-reload")
}

// EnableAndRestart enable + restart。
func (s *Systemctl) EnableAndRestart(ctx context.Context, unitName string) error {
	if err := s.run(ctx, fmt.Sprintf("systemctl enable %s", unitName)); err != nil {
		return err
	}
	return s.run(ctx, fmt.Sprintf("systemctl restart %s", unitName))
}

// Restart 仅重启。
func (s *Systemctl) Restart(ctx context.Context, unitName string) error {
	return s.run(ctx, fmt.Sprintf("systemctl restart %s", unitName))
}

// Stop 停止。
func (s *Systemctl) Stop(ctx context.Context, unitName string) error {
	return s.run(ctx, fmt.Sprintf("systemctl stop %s", unitName))
}

// IsActive 查 active 状态。返回 stdout，调用方判断 "active" 子串。
func (s *Systemctl) IsActive(ctx context.Context, unitName string) (string, error) {
	res, err := s.client.Exec(ctx, fmt.Sprintf("systemctl is-active %s", unitName))
	if err != nil {
		return res.Stdout, err
	}
	// is-active 返回非 0 但不是 ssh 层错误，调用方按 Stdout 决断
	return strings.TrimSpace(res.Stdout), nil
}

// WaitActive 轮询 systemctl is-active 直到状态稳定或超时。
//   - 每 500ms 探一次
//   - 命中 "active" → 返回 nil + 总耗时
//   - 命中 "failed" / "inactive" → 立即返回 error（不等超时，让上层尽早诊断）
//   - 期间状态 "activating" → 继续等
//   - 超时 → 返回 error 含最后一次状态
//
// Sprint 3.6：systemd Type=simple + Restart=on-failure 时，systemctl restart 是
// 异步的 —— 必须主动等才能区分"启动成功"和"立刻 crash"。
func (s *Systemctl) WaitActive(ctx context.Context, unitName string, timeout time.Duration) (string, time.Duration, error) {
	start := time.Now()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	last := "unknown"
	deadline := start.Add(timeout)
	for {
		// is-active 立刻探一次（首次不等 tick）
		st, _ := s.IsActive(ctx, unitName)
		last = st
		switch st {
		case "active":
			return st, time.Since(start), nil
		case "failed", "inactive":
			return st, time.Since(start), fmt.Errorf("unit %s is %s after %s", unitName, st, time.Since(start).Round(time.Millisecond))
		}
		// activating / unknown / 临时空 → 继续等
		if time.Now().After(deadline) {
			return last, time.Since(start), fmt.Errorf("unit %s still %q after %s timeout", unitName, last, timeout)
		}
		select {
		case <-ctx.Done():
			return last, time.Since(start), ctx.Err()
		case <-tick.C:
		}
	}
}

// StatusDump 拼装一份失败诊断信息：systemctl status 摘要 + journalctl 末尾 N 行。
// 用于 step.error / step.detail，让前端 Drawer 直接看到 Java 栈和 systemd 错误。
// 任一子命令失败时仍把可获取的部分返回（不阻塞主流程）。
func (s *Systemctl) StatusDump(ctx context.Context, unitName string, journalLines int) string {
	if journalLines <= 0 {
		journalLines = 200
	}
	var b strings.Builder

	// 1. systemctl status —— 短摘要 + Active: 行
	statusCmd := fmt.Sprintf("systemctl status %s --no-pager -l --lines=0", unitName)
	if res, err := s.client.Exec(ctx, statusCmd); err == nil {
		b.WriteString("=== systemctl status ===\n")
		b.WriteString(strings.TrimSpace(res.Stdout))
		if strings.TrimSpace(res.Stderr) != "" {
			b.WriteString("\n[stderr] ")
			b.WriteString(strings.TrimSpace(res.Stderr))
		}
		b.WriteString("\n")
	}

	// 2. journalctl —— 应用日志末尾 N 行
	// --no-pager 防止挂死；-n 限制行数；-o cat 去掉时间前缀让输出更紧凑
	journalCmd := fmt.Sprintf("journalctl -u %s --no-pager -n %d -o cat 2>&1 || true",
		unitName, journalLines)
	if res, err := s.client.Exec(ctx, journalCmd); err == nil {
		b.WriteString("\n=== journalctl -u ")
		b.WriteString(unitName)
		b.WriteString(" (last ")
		fmt.Fprintf(&b, "%d", journalLines)
		b.WriteString(" lines) ===\n")
		b.WriteString(strings.TrimSpace(res.Stdout))
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func (s *Systemctl) run(ctx context.Context, cmd string) error {
	res, err := s.client.Exec(ctx, cmd)
	if err != nil {
		return fmt.Errorf("exec %q: %w", cmd, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("exec %q exit=%d stderr=%s", cmd, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
