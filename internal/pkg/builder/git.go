// Package builder 把"从远端拉代码 + 跑 Maven 构建"封成本机进程级 API。
//
// 范围（Sprint 5.2）：
//   - git clone（公网 / HTTPS PAT / SSH 私钥三种鉴权）
//   - mvn package（log 流式写到 io.Writer）
//   - 找 jar 产物
//
// 不范围：
//   - 远端构建机（SSH 过去跑）—— 留 Sprint 5.5
//   - Docker 容器构建 —— 留 Sprint 5.5
//   - 工件签名 / SBOM —— 不在 swift-devops 当前定位
package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Credential 一份 Git 凭证。
//   - Type="token": HTTPS PAT，Username 用户名，Secret 是 token
//   - Type="ssh_key": SSH 私钥（PEM），Username 一般 "git"
//   - Type="" / nil: 匿名访问公网
type Credential struct {
	Type     string
	Username string
	Secret   string
}

// CloneOptions clone 的输入。
type CloneOptions struct {
	URL       string
	Ref       string        // 分支 / tag / commit；空 = 远端默认分支
	TargetDir string        // clone 目标目录（必须为空或不存在）
	Cred      *Credential   // nil = 匿名
	LogWriter io.Writer     // 命令输出（stdout+stderr）流到这里；nil = 丢弃
	Timeout   time.Duration // 0 = 5 分钟兜底
	ExecEnv   []string      // 调用方注入的环境变量（Sprint 5.4 builder env）；nil = os.Environ()
}

// Clone 拉代码到 TargetDir，返回 HEAD commit SHA。
// 任一步失败返回错误；TargetDir 不会自动清理（调用方决定保留供排障还是删除）。
func Clone(ctx context.Context, opts CloneOptions) (string, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return "", errors.New("git url is empty")
	}
	if strings.TrimSpace(opts.TargetDir) == "" {
		return "", errors.New("target dir is empty")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	logw := opts.LogWriter
	if logw == nil {
		logw = io.Discard
	}

	cloneURL := opts.URL
	extraEnv, cleanup, err := prepareCredential(opts.Cred, &cloneURL)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// 1. git clone（浅克隆加速；指定 ref 失败回退到全 clone + checkout）
	args := []string{"clone", "--depth", "50"}
	if opts.Ref != "" {
		// 直接 -b 既支持分支也支持 tag，但不支持纯 commit SHA
		args = append(args, "-b", opts.Ref)
	}
	args = append(args, "--", cloneURL, opts.TargetDir)
	fmt.Fprintf(logw, "$ git %s\n", redactArgs(args))

	if err := runCmd(cctx, "", logw, opts.ExecEnv, extraEnv, "git", args...); err != nil {
		// fallback：纯 commit SHA 时 -b 会报错，去掉 -b 重试 + 后续 checkout
		if opts.Ref != "" && looksLikeSHA(opts.Ref) {
			_ = os.RemoveAll(opts.TargetDir)
			fmt.Fprintf(logw, "[fallback] -b %s 失败，回退为 clone 默认分支 + checkout\n", opts.Ref)
			retry := []string{"clone", "--", cloneURL, opts.TargetDir}
			if err2 := runCmd(cctx, "", logw, opts.ExecEnv, extraEnv, "git", retry...); err2 != nil {
				return "", fmt.Errorf("git clone retry: %w", err2)
			}
			if err2 := runCmd(cctx, opts.TargetDir, logw, opts.ExecEnv, extraEnv, "git", "checkout", opts.Ref); err2 != nil {
				return "", fmt.Errorf("git checkout %s: %w", opts.Ref, err2)
			}
		} else {
			return "", fmt.Errorf("git clone: %w", err)
		}
	}

	// 2. 取 HEAD commit SHA
	revCmd := exec.CommandContext(cctx, "git", "-C", opts.TargetDir, "rev-parse", "HEAD")
	if opts.ExecEnv != nil {
		revCmd.Env = append([]string{}, opts.ExecEnv...)
	}
	out, err := revCmd.Output()
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	fmt.Fprintf(logw, "[git] HEAD = %s\n", sha)
	return sha, nil
}

// prepareCredential 根据凭证类型改造 URL 或准备 GIT_SSH_COMMAND。
// 返回额外环境变量 + cleanup 函数（删临时私钥文件）。
func prepareCredential(c *Credential, cloneURL *string) ([]string, func(), error) {
	noop := func() {}
	if c == nil || c.Type == "" {
		return nil, noop, nil
	}
	switch c.Type {
	case "token":
		u, err := url.Parse(*cloneURL)
		if err != nil {
			return nil, noop, fmt.Errorf("parse url: %w", err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return nil, noop, fmt.Errorf("token 凭证仅适用于 HTTPS 仓库，当前 scheme=%s", u.Scheme)
		}
		user := c.Username
		if user == "" {
			user = "oauth2" // GitLab 风格兜底
		}
		u.User = url.UserPassword(user, c.Secret)
		*cloneURL = u.String()
		return nil, noop, nil

	case "ssh_key":
		// 把私钥写入临时文件（限 0600），让 git 通过 GIT_SSH_COMMAND 加载
		tmp, err := os.CreateTemp("", "swift-devops-gitkey-*.pem")
		if err != nil {
			return nil, noop, fmt.Errorf("create temp key: %w", err)
		}
		if err := os.Chmod(tmp.Name(), 0o600); err != nil {
			_ = os.Remove(tmp.Name())
			return nil, noop, fmt.Errorf("chmod temp key: %w", err)
		}
		if _, err := tmp.WriteString(ensureTrailingNewline(c.Secret)); err != nil {
			_ = os.Remove(tmp.Name())
			return nil, noop, fmt.Errorf("write temp key: %w", err)
		}
		_ = tmp.Close()
		cleanup := func() { _ = os.Remove(tmp.Name()) }
		env := []string{
			"GIT_SSH_COMMAND=ssh -i " + tmp.Name() + " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes",
		}
		return env, cleanup, nil

	default:
		return nil, noop, fmt.Errorf("unknown credential type: %s", c.Type)
	}
}

// runCmd 在 dir 下跑 cmd，stdout/stderr 都重定向到 logw。
// baseEnv 是基础环境（一般是 opts.ExecEnv 或 os.Environ()）；extraEnv 追加在后面（如 GIT_SSH_COMMAND）。
func runCmd(ctx context.Context, dir string, logw io.Writer, baseEnv, extraEnv []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = logw
	cmd.Stderr = logw
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	cmd.Env = append(append([]string{}, baseEnv...), extraEnv...)
	return cmd.Run()
}

// redactArgs 把 args 里可能含 token 的 URL 替换掉，避免日志泄露。
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = redactURL(a)
	}
	return strings.Join(out, " ")
}

func redactURL(s string) string {
	if !strings.Contains(s, "://") || !strings.Contains(s, "@") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "REDACTED")
		return u.String()
	}
	return s
}

func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func ensureTrailingNewline(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

// 让编译器看到 filepath 用过（rev-parse 路径在 maven.go 用）
var _ = filepath.Base
