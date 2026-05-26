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

// ServiceBuildSpec 多 service 构建规格 —— Sprint X.2 新增。
// 每个 spec 对应一个 AppService，构建后从 workspace 下按 JarPattern 提取一个 jar。
type ServiceBuildSpec struct {
	ServiceCode string // 用于 Result.JarPaths 的 key
	BuildModule string // 可空；非空时 mvn 加 -pl <module>（多个用逗号拼）
	JarPattern  string // 可空；空走 builder 默认扫描 + Spring Boot 探测
}

// Plan 一次构建的完整输入。service 层组装。
type Plan struct {
	AppCode       string      // 用于命名 workspace 子目录
	GitURL        string
	GitRef        string      // 分支 / tag / commit
	Cred          *Credential // nil = 公网匿名
	MvnArgs       string      // 留空 = "clean package -DskipTests"
	MvnBin        string      // mvn 可执行绝对路径（Sprint X.8 必填，杜绝 LookPath）
	GitBin        string      // git 可执行绝对路径（Sprint X.8 必填，杜绝 LookPath）
	JarPattern    string      // 旧字段：单 service 模式 glob；Sprint X.2 之后建议走 Services
	Workspace     string      // build workspace 根目录（如 ./data/build/）
	MavenCacheDir string      // -Dmaven.repo.local；空 = 走 ~/.m2
	LogWriter     io.Writer   // git + mvn 全部 stdout/stderr 都写这里
	ExecEnv       []string    // KEY=VALUE 列表，注入到 git/mvn 子进程；nil = 继承 os.Environ()。Sprint 5.4
	CloneTimeout  time.Duration
	BuildTimeout  time.Duration
	BuildID       uint        // 用于命名子目录（同 app_code 多并发时去重）

	// Sprint X.2：多 service 模式。非空时按每个 spec 提取一个 jar 进 Result.JarPaths；
	// 同时所有 service 的 BuildModule 合并成 mvn -pl <m1,m2,...> -am（缩窄编译范围）。
	// 空时退化为旧的单 service 模式。
	Services []ServiceBuildSpec
}

// Result 一次构建的产物。
type Result struct {
	JarPath     string            // 旧字段：单 service 模式的 jar；多 service 模式取 default 或第一个
	JarPaths    map[string]string // Sprint X.2：多 service 模式，map[service_code]→jar 绝对路径
	CommitSHA   string
	BuildSubDir string // 本次构建的工作目录；调用方决定保留还是清理
}

// Build 一次完整的构建：clone + mvn package + 找 jar。
//   - 自动新建子目录 <Workspace>/<AppCode>-<BuildID> 作为工作区
//   - 失败时返回错误但保留工作区，供事后排查
//   - 成功后调用方需要把 jar 拷贝到 artifact_dir 并清理工作区
//
// Sprint X.2：plan.Services 非空时走多 service 提取；否则单 service 兼容旧行为。
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
	if strings.TrimSpace(plan.MvnBin) == "" {
		return Result{}, errors.New("mvn_bin is empty (Sprint X.8 要求注入绝对路径)")
	}
	if strings.TrimSpace(plan.GitBin) == "" {
		return Result{}, errors.New("git_bin is empty (Sprint X.8 要求注入绝对路径)")
	}

	// 1. 创建工作区子目录：<workspace>/<app_code>-<build_id>
	subDir := filepath.Join(plan.Workspace,
		fmt.Sprintf("%s-%d", plan.AppCode, plan.BuildID))
	if err := os.MkdirAll(plan.Workspace, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir workspace: %w", err)
	}
	if entries, err := os.ReadDir(subDir); err == nil && len(entries) > 0 {
		if err := os.RemoveAll(subDir); err != nil {
			return Result{}, fmt.Errorf("clean stale subDir: %w", err)
		}
	}

	res := Result{BuildSubDir: subDir}

	// 2. git clone
	sha, err := Clone(ctx, CloneOptions{
		URL: plan.GitURL, Ref: plan.GitRef, TargetDir: subDir,
		GitBin:    plan.GitBin,
		Cred:      plan.Cred,
		LogWriter: plan.LogWriter,
		Timeout:   plan.CloneTimeout,
		ExecEnv:   plan.ExecEnv,
	})
	if err != nil {
		return res, fmt.Errorf("clone: %w", err)
	}
	res.CommitSHA = sha

	// 3. 多 service 模式：聚合 -pl 列表，跑一次 mvn，分别按 pattern 提取 jar
	if len(plan.Services) > 0 {
		mvnArgs := mergeMultiModuleArgs(plan.MvnArgs, plan.Services)
		mvn, err := MvnPackage(ctx, MavenOptions{
			WorkDir:       subDir,
			MvnBin:        plan.MvnBin,
			ExtraArgs:     mvnArgs,
			MavenCacheDir: plan.MavenCacheDir,
			LogWriter:     plan.LogWriter,
			Timeout:       plan.BuildTimeout,
			ExecEnv:       plan.ExecEnv,
			// MultiService 模式下不让 maven.go 做单 jar 收敛，所有 service 自己 pattern
			SkipJarScan: true,
		})
		if err != nil {
			return res, fmt.Errorf("maven: %w", err)
		}
		_ = mvn // SkipJarScan 时 JarPath 为空，不用
		res.JarPaths = map[string]string{}
		for _, spec := range plan.Services {
			jar, ferr := findArtifactJar(subDir, spec.JarPattern)
			if ferr != nil {
				return res, fmt.Errorf("service %s: %w", spec.ServiceCode, ferr)
			}
			res.JarPaths[spec.ServiceCode] = jar
			if plan.LogWriter != nil {
				fmt.Fprintf(plan.LogWriter, "[mvn] service=%s -> %s\n", spec.ServiceCode, jar)
			}
		}
		// 兼容字段：JarPath 取 default 或第一个
		if p, ok := res.JarPaths["default"]; ok {
			res.JarPath = p
		} else {
			for _, spec := range plan.Services {
				if p, ok := res.JarPaths[spec.ServiceCode]; ok {
					res.JarPath = p
					break
				}
			}
		}
		return res, nil
	}

	// 4. 单 service 模式（旧行为）
	mvn, err := MvnPackage(ctx, MavenOptions{
		WorkDir:       subDir,
		MvnBin:        plan.MvnBin,
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
	res.JarPaths = map[string]string{"default": mvn.JarPath}
	return res, nil
}

// mergeMultiModuleArgs 把 Services 里所有非空的 BuildModule 合并为 mvn -pl <m1,m2,...> -am。
// 如果用户已经在 MvnArgs 里手填了 -pl，直接尊重用户的；否则前置加。
// MvnArgs 空时给默认 "clean package -DskipTests"。
func mergeMultiModuleArgs(userArgs string, specs []ServiceBuildSpec) string {
	args := strings.TrimSpace(userArgs)
	if args == "" {
		args = "clean package -DskipTests"
	}
	if strings.Contains(args, "-pl ") || strings.Contains(args, "--projects") {
		return args
	}
	var mods []string
	seen := map[string]bool{}
	for _, sp := range specs {
		m := strings.TrimSpace(sp.BuildModule)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		mods = append(mods, m)
	}
	if len(mods) == 0 {
		return args
	}
	return fmt.Sprintf("-pl %s -am %s", strings.Join(mods, ","), args)
}
