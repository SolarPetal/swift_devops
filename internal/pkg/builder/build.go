package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Plan 一次构建的完整输入。service 层组装。
type Plan struct {
	AppCode       string      // 用于命名 workspace 子目录
	GitURL        string
	GitRef        string      // 分支 / tag / commit
	Cred          *Credential // nil = 公网匿名
	MvnArgs       string      // 留空 = "clean package -DskipTests"
	JarPattern    string      // glob 选 jar；空 = 自动扫描 + Spring Boot 探测。Sprint 5.4.7
	Workspace     string      // build workspace 根目录（如 ./data/build/）
	MavenCacheDir string      // -Dmaven.repo.local；空 = 走 ~/.m2
	LogWriter     io.Writer   // git + mvn 全部 stdout/stderr 都写这里
	ExecEnv       []string    // KEY=VALUE 列表，注入到 git/mvn 子进程；nil = 继承 os.Environ()。Sprint 5.4
	CloneTimeout  time.Duration
	BuildTimeout  time.Duration
	BuildID       uint        // 用于命名子目录（同 app_code 多并发时去重）
}

// Result 一次构建的产物。
type Result struct {
	JarPath   string // 构建出来的 jar 绝对路径（在 BuildSubDir 内）
	CommitSHA string
	BuildSubDir string // 本次构建的工作目录；调用方决定保留还是清理
}

// Build 一次完整的构建：clone + mvn package + 找 jar。
//   - 自动新建子目录 <Workspace>/<AppCode>-<BuildID> 作为工作区
//   - 失败时返回错误但保留工作区，供事后排查
//   - 成功后调用方需要把 jar 拷贝到 artifact_dir 并清理工作区
func Build(ctx context.Context, plan Plan) (Result, error) {
	if strings.TrimSpace(plan.AppCode) == "" {
		return Result{}, errors.New("app_code is empty")
	}
	if strings.TrimSpace(plan.Workspace) == "" {
		return Result{}, errors.New("workspace is empty")
	}
	if strings.TrimSpace(plan.GitURL) == "" {
		return Result{}, errors.New("git url is empty")
	}

	// 1. 创建工作区子目录：<workspace>/<app_code>-<build_id>
	subDir := filepath.Join(plan.Workspace,
		fmt.Sprintf("%s-%d", plan.AppCode, plan.BuildID))
	if err := os.MkdirAll(plan.Workspace, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir workspace: %w", err)
	}
	// 子目录必须不存在或为空，避免污染 git clone
	if entries, err := os.ReadDir(subDir); err == nil && len(entries) > 0 {
		// 已存在且非空 —— 直接清掉重建（调用方传 BuildID 保证唯一，这里多一层防御）
		if err := os.RemoveAll(subDir); err != nil {
			return Result{}, fmt.Errorf("clean stale subDir: %w", err)
		}
	}

	res := Result{BuildSubDir: subDir}

	// 2. git clone
	sha, err := Clone(ctx, CloneOptions{
		URL: plan.GitURL, Ref: plan.GitRef, TargetDir: subDir,
		Cred:      plan.Cred,
		LogWriter: plan.LogWriter,
		Timeout:   plan.CloneTimeout,
		ExecEnv:   plan.ExecEnv,
	})
	if err != nil {
		return res, fmt.Errorf("clone: %w", err)
	}
	res.CommitSHA = sha

	// 3. mvn package
	mvn, err := MvnPackage(ctx, MavenOptions{
		WorkDir:       subDir,
		ExtraArgs:     plan.MvnArgs,
		MavenCacheDir: plan.MavenCacheDir,
		LogWriter:     plan.LogWriter,
		Timeout:       plan.BuildTimeout,
		ExecEnv:       plan.ExecEnv,
		JarPattern:    plan.JarPattern,
	})
	if err != nil {
		return res, fmt.Errorf("maven: %w", err)
	}
	res.JarPath = mvn.JarPath
	return res, nil
}
