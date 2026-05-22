package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	sshpkg "swift-devops/internal/pkg/ssh"
)

// NginxBackend 一台后端主机
type NginxBackend struct {
	IP   string
	Port int
}

// NginxUpstream 渲染 upstream 文件所需的全部数据。
// Sprint 4 蓝绿：Servers 只包含当前活跃组的主机，切换 = 重写文件 + reload。
type NginxUpstream struct {
	AppCode      string // 用作文件名（必须满足 app_code 命名约束）
	UpstreamName string // upstream <name> 块名
	Servers      []NginxBackend
}

// UpstreamConfPath 拼装 nginx conf.d 路径。
// 约定每个 app 独占一个文件，避免和其他 app 冲突。
func UpstreamConfPath(appCode string) string {
	return "/etc/nginx/conf.d/swift-devops-" + appCode + ".conf"
}

// RenderUpstreamConf 生成 nginx upstream 配置文件内容。
//
//	# Managed by swift-devops. Do not edit by hand.
//	# App: <code>, generated at <ts>
//	upstream <name> {
//	    least_conn;
//	    server <ip1>:<port1>;
//	    server <ip2>:<port2>;
//	}
//
// least_conn：连接数最少优先，比默认 round-robin 更适合 Java 长连接场景。
// 想换 ip_hash 或 random 等再在模板里加可选项。
func RenderUpstreamConf(u NginxUpstream) (string, error) {
	if strings.TrimSpace(u.UpstreamName) == "" {
		return "", fmt.Errorf("upstream_name 不能为空")
	}
	if len(u.Servers) == 0 {
		return "", fmt.Errorf("servers 不能为空")
	}
	for i, s := range u.Servers {
		if strings.TrimSpace(s.IP) == "" {
			return "", fmt.Errorf("server[%d].ip 不能为空", i)
		}
		if s.Port < 1 || s.Port > 65535 {
			return "", fmt.Errorf("server[%d].port 非法：%d", i, s.Port)
		}
	}
	var b strings.Builder
	b.WriteString("# Managed by swift-devops. Do not edit by hand.\n")
	fmt.Fprintf(&b, "# App: %s, generated at %s\n", u.AppCode, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "upstream %s {\n", u.UpstreamName)
	b.WriteString("    least_conn;\n")
	for _, s := range u.Servers {
		fmt.Fprintf(&b, "    server %s:%d;\n", s.IP, s.Port)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// NginxApplier 通过 SSH 在 nginx 主机上 apply upstream 配置。
type NginxApplier struct {
	client     *sshpkg.Client
	dispatcher *Dispatcher
}

func NewNginxApplier(c *sshpkg.Client) *NginxApplier {
	return &NginxApplier{
		client:     c,
		dispatcher: NewDispatcher(c.SSHClient()),
	}
}

// Apply 把 upstream 配置应用到远端 nginx：
//  1. 备份老 conf 到 .bak（不存在时静默跳过）
//  2. 写新 conf
//  3. nginx -t 预检 —— 失败回滚 .bak、返回错误
//  4. nginx -s reload —— 失败回滚 .bak + 再 reload，返回错误
//
// 任一阶段失败都尽力把现场恢复到 apply 前的状态，但若回滚自身也失败，
// 远端可能落到中间态——返回错误中包含 stderr 供排障，但不会再 retry。
func (a *NginxApplier) Apply(ctx context.Context, u NginxUpstream) error {
	content, err := RenderUpstreamConf(u)
	if err != nil {
		return fmt.Errorf("render upstream: %w", err)
	}
	path := UpstreamConfPath(u.AppCode)
	bak := path + ".bak"

	// 1. 备份老 conf。`test -f && cp; true` 模式：文件不存在时退到 true，整体 exit 0
	if _, err := a.client.Exec(ctx,
		fmt.Sprintf("test -f %s && cp -af %s %s; true", ShellQuote(path), ShellQuote(path), ShellQuote(bak))); err != nil {
		return fmt.Errorf("backup old conf: %w", err)
	}

	// 2. 写新 conf
	if err := a.dispatcher.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write new conf: %w", err)
	}

	// 3. nginx -t 预检
	res, err := a.client.Exec(ctx, "nginx -t")
	if err != nil {
		return fmt.Errorf("nginx -t exec: %w", err)
	}
	if res.ExitCode != 0 {
		a.rollback(ctx, path, bak, false)
		return fmt.Errorf("nginx -t failed (exit %d): %s",
			res.ExitCode, strings.TrimSpace(res.Stderr+res.Stdout))
	}

	// 4. reload
	res, err = a.client.Exec(ctx, "nginx -s reload")
	if err != nil {
		return fmt.Errorf("nginx -s reload exec: %w", err)
	}
	if res.ExitCode != 0 {
		a.rollback(ctx, path, bak, true)
		return fmt.Errorf("nginx -s reload failed (exit %d): %s",
			res.ExitCode, strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

// rollback 回滚到 .bak。alsoReload=true 时回滚后再 reload 一次让 nginx 恢复服务。
// 回滚错误只 log（已经在错误返回链上了，不二次包装）。
func (a *NginxApplier) rollback(ctx context.Context, path, bak string, alsoReload bool) {
	cmd := fmt.Sprintf("test -f %s && cp -af %s %s; true", ShellQuote(bak), ShellQuote(bak), ShellQuote(path))
	if alsoReload {
		cmd += "; nginx -s reload; true"
	}
	_, _ = a.client.Exec(ctx, cmd)
}

// ShellQuote 把路径包成 shell 单引号安全格式。
// 路径里含单引号时用 '"'"' 拼接 —— 调用方传的路径一般经过 regex 限制（无单引号），
// 但加这一层防御不亏；strategy 包也复用此函数做 env_check 时的路径转义。
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
