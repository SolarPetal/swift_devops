package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ListRemoteBranches 获取远程仓库的分支列表（Sprint X.11）
//   - 使用 git ls-remote 获取远程分支
//   - 返回分支名列表（不含 refs/heads/ 前缀）
func ListRemoteBranches(ctx context.Context, opts ListBranchesOptions) ([]string, error) {
	if strings.TrimSpace(opts.GitURL) == "" {
		return nil, fmt.Errorf("git_url is empty")
	}
	if strings.TrimSpace(opts.GitBin) == "" {
		return nil, fmt.Errorf("git_bin is empty")
	}

	// 构建 git ls-remote 命令
	args := []string{"ls-remote", "--heads"}

	// 环境变量：未显式传入则继承当前进程环境（含 PATH/HOME/SSH_AUTH_SOCK）。
	// 否则 cmd.Env 被设为空，git 子进程会因缺 HOME 找不到 ~/.ssh、缺 PATH 找不到
	// ssh / credential helper 而失败。
	env := opts.ExecEnv
	if len(env) == 0 {
		env = os.Environ()
	}

	if opts.Cred != nil {
		if opts.Cred.Type == "token" {
			// HTTPS Token 认证：通过 GIT_ASKPASS 注入
			env = append(env, fmt.Sprintf("GIT_USERNAME=%s", opts.Cred.Username))
			env = append(env, fmt.Sprintf("GIT_PASSWORD=%s", opts.Cred.Secret))
			// 使用 credential helper
			args = append([]string{"-c", "credential.helper=", "-c", "credential.helper=!f() { echo username=$GIT_USERNAME; echo password=$GIT_PASSWORD; }; f"}, args...)
		} else if opts.Cred.Type == "ssh_key" {
			// SSH Key 认证：通过 GIT_SSH_COMMAND 注入
			// 注意：这里需要先把 SSH Key 写到临时文件，实际使用时需要完善
			env = append(env, "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=no")
		}
	}

	args = append(args, opts.GitURL)

	// 设置超时
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行命令
	cmd := exec.CommandContext(ctx, opts.GitBin, args...)
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote failed: %w, output: %s", err, string(output))
	}

	// 解析输出
	branches := []string{}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 格式：<commit-sha>\trefs/heads/<branch-name>
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		if strings.HasPrefix(ref, "refs/heads/") {
			branch := strings.TrimPrefix(ref, "refs/heads/")
			branches = append(branches, branch)
		}
	}

	return branches, nil
}

// ListBranchesOptions 获取分支列表的参数
type ListBranchesOptions struct {
	GitURL  string
	GitBin  string
	Cred    *Credential
	ExecEnv []string
	Timeout time.Duration
}
