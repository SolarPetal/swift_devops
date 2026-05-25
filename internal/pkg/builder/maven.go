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

// MavenOptions mvn package 的输入。
type MavenOptions struct {
	WorkDir       string        // pom.xml 所在目录
	ExtraArgs     string        // 用户指定的额外 mvn 参数（空 → 默认 "clean package -DskipTests"）
	MavenCacheDir string        // -Dmaven.repo.local=<dir>；空 = 走 ~/.m2
	LogWriter     io.Writer     // 命令输出
	Timeout       time.Duration // 0 = 30 分钟兜底
}

// MavenResult 构建产物
type MavenResult struct {
	JarPath string // target/*.jar 的绝对路径（已经排除 sources/javadoc/original-）
}

// MvnPackage 在 WorkDir 跑 mvn package，返回扫到的唯一 jar 路径。
//   - WorkDir 必须含 pom.xml
//   - 多 jar 命中（如 multi-module）返回错误并列出候选；用户应缩窄 scope（如 -pl 子模块）
//   - 返回前已 fsync 文件（exec 完毕保证写盘）
func MvnPackage(ctx context.Context, opts MavenOptions) (MavenResult, error) {
	if strings.TrimSpace(opts.WorkDir) == "" {
		return MavenResult{}, errors.New("work_dir is empty")
	}
	if _, err := os.Stat(filepath.Join(opts.WorkDir, "pom.xml")); err != nil {
		return MavenResult{}, fmt.Errorf("pom.xml not found in %s: %w", opts.WorkDir, err)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	logw := opts.LogWriter
	if logw == nil {
		logw = io.Discard
	}

	args := []string{"-B", "-ntp"} // batch + no transfer progress（日志干净点）
	if opts.MavenCacheDir != "" {
		args = append(args, "-Dmaven.repo.local="+opts.MavenCacheDir)
	}
	extra := strings.TrimSpace(opts.ExtraArgs)
	if extra == "" {
		extra = "clean package -DskipTests"
	}
	args = append(args, strings.Fields(extra)...)

	mctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	fmt.Fprintf(logw, "$ cd %s && mvn %s\n", opts.WorkDir, strings.Join(args, " "))
	cmd := exec.CommandContext(mctx, "mvn", args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdout = logw
	cmd.Stderr = logw
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return MavenResult{}, fmt.Errorf("mvn package: %w", err)
	}

	// 扫 target/*.jar；排除 -sources / -javadoc / original- 前缀
	jar, err := findArtifactJar(opts.WorkDir)
	if err != nil {
		return MavenResult{}, err
	}
	fmt.Fprintf(logw, "[mvn] artifact = %s\n", jar)
	return MavenResult{JarPath: jar}, nil
}

// findArtifactJar 在 WorkDir/**/target/ 下找出"用户可部署"的 jar。
//   - 优先找根 target/，无果再递归 module/target/
//   - 排除 -sources.jar / -javadoc.jar / original-*.jar / tests.jar
//   - 命中多个 → 报错列出候选，让上层指定具体模块
func findArtifactJar(workDir string) (string, error) {
	rootTarget := filepath.Join(workDir, "target")
	if cands := scanTarget(rootTarget); len(cands) == 1 {
		return cands[0], nil
	} else if len(cands) > 1 {
		return "", fmt.Errorf("target/ 下命中多个 jar，请用 mvn -pl 指定单模块或调整 ExtraArgs：\n  %s",
			strings.Join(cands, "\n  "))
	}

	// 根 target/ 没有 → 递归找各 module/target/
	var all []string
	_ = filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		// 跳过 root target（已扫过）/ 隐藏目录
		if path == rootTarget {
			return filepath.SkipDir
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			return filepath.SkipDir
		}
		if base == "target" {
			all = append(all, scanTarget(path)...)
			return filepath.SkipDir
		}
		return nil
	})
	switch len(all) {
	case 0:
		return "", errors.New("未在 target/ 找到可部署 jar；检查 mvn 输出是否含 BUILD SUCCESS")
	case 1:
		return all[0], nil
	default:
		return "", fmt.Errorf("多个模块 target/ 命中 jar，请用 mvn -pl 缩窄：\n  %s",
			strings.Join(all, "\n  "))
	}
}

// scanTarget 列出指定 target/ 下"用户可部署"的 jar 候选
func scanTarget(targetDir string) []string {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jar") {
			continue
		}
		// 排除典型干扰
		if strings.HasSuffix(name, "-sources.jar") ||
			strings.HasSuffix(name, "-javadoc.jar") ||
			strings.HasSuffix(name, "-tests.jar") ||
			strings.HasPrefix(name, "original-") {
			continue
		}
		out = append(out, filepath.Join(targetDir, name))
	}
	return out
}
