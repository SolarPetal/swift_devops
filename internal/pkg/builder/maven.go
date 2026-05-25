package builder

import (
	"archive/zip"
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
	ExecEnv       []string      // 注入到 mvn 子进程的环境变量；nil = os.Environ()。Sprint 5.4
	JarPattern    string        // glob 显式指定主 jar，相对 WorkDir。非空 → 跳过自动扫描。Sprint 5.4.7
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
	if opts.ExecEnv != nil {
		cmd.Env = append([]string{}, opts.ExecEnv...)
	} else {
		cmd.Env = os.Environ()
	}
	if err := cmd.Run(); err != nil {
		return MavenResult{}, fmt.Errorf("mvn package: %w", err)
	}

	// 扫 target/*.jar；排除 -sources / -javadoc / original- 前缀
	jar, err := findArtifactJar(opts.WorkDir, opts.JarPattern)
	if err != nil {
		return MavenResult{}, err
	}
	fmt.Fprintf(logw, "[mvn] artifact = %s\n", jar)
	return MavenResult{JarPath: jar}, nil
}

// findArtifactJar 在 WorkDir/**/target/ 下找出"用户可部署"的 jar。
// 三级策略（Sprint 5.4.7）：
//   1. jarPattern 非空 → filepath.Glob(WorkDir + pattern)，唯一命中即用；多/0 报错
//   2. 扫所有 target/*.jar（排除 sources/javadoc/tests/original-），唯一 → 用
//   3. 多 jar → 探 Spring Boot fat jar（MANIFEST.MF 含 Spring-Boot-Lib），唯一 → 用
//   4. 仍多 / 0 → 报错列候选 + 引导填 build_jar_pattern
func findArtifactJar(workDir, jarPattern string) (string, error) {
	// 策略 1：显式 glob
	if p := strings.TrimSpace(jarPattern); p != "" {
		pat := p
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(workDir, pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			return "", fmt.Errorf("解析 build_jar_pattern %q 失败：%w", p, err)
		}
		// 过掉非 jar / 干扰
		var cleaned []string
		for _, m := range matches {
			name := filepath.Base(m)
			if !strings.HasSuffix(name, ".jar") {
				continue
			}
			if isAuxJar(name) {
				continue
			}
			cleaned = append(cleaned, m)
		}
		switch len(cleaned) {
		case 1:
			return cleaned[0], nil
		case 0:
			return "", fmt.Errorf("build_jar_pattern %q 在 %s 下未匹配到任何可部署 jar（注意 sources/javadoc/tests 已自动排除）",
				p, workDir)
		default:
			return "", fmt.Errorf("build_jar_pattern %q 匹配到多个 jar，请收窄：\n  %s",
				p, strings.Join(cleaned, "\n  "))
		}
	}

	// 策略 2：根 target/ 优先
	rootTarget := filepath.Join(workDir, "target")
	if cands := scanTarget(rootTarget); len(cands) == 1 {
		return cands[0], nil
	} else if len(cands) > 1 {
		// 同一 target 多 jar 罕见，但还是按 Spring Boot 探测一下
		if springBoot, sErr := pickSpringBootJar(cands); sErr == nil {
			return springBoot, nil
		}
		return "", multiJarHint(workDir, cands)
	}

	// 策略 3：递归各 module/target/
	var all []string
	_ = filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
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
	}
	// 多个 → Spring Boot 探测
	if springBoot, sErr := pickSpringBootJar(all); sErr == nil {
		return springBoot, nil
	}
	return "", multiJarHint(workDir, all)
}

// multiJarHint 多 jar 命中时的友好错误信息
func multiJarHint(workDir string, jars []string) error {
	// 相对路径展示更短
	rels := make([]string, 0, len(jars))
	for _, j := range jars {
		if rel, err := filepath.Rel(workDir, j); err == nil {
			rels = append(rels, rel)
		} else {
			rels = append(rels, j)
		}
	}
	return fmt.Errorf("命中多个候选 jar 且未识别 Spring Boot 主 jar，请：\n"+
		"  方案 A：去「应用管理」编辑应用，「构建」Tab 填 build_jar_pattern（glob 如 car-dealer-admin/target/*.jar）\n"+
		"  方案 B：填 build_module（如 car-dealer-admin），系统会自动 mvn -pl 缩窄编译范围\n"+
		"  方案 C：临时在「触发构建」时填上述字段\n"+
		"候选 jar 列表：\n  %s",
		strings.Join(rels, "\n  "))
}

// isAuxJar 是否是 sources/javadoc/tests/original 之类的辅助 jar
func isAuxJar(name string) bool {
	return strings.HasSuffix(name, "-sources.jar") ||
		strings.HasSuffix(name, "-javadoc.jar") ||
		strings.HasSuffix(name, "-tests.jar") ||
		strings.HasPrefix(name, "original-")
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
		if isAuxJar(name) {
			continue
		}
		out = append(out, filepath.Join(targetDir, name))
	}
	return out
}

// pickSpringBootJar 从候选 jar 里挑唯一一个 Spring Boot fat jar。
// 判定：解压 jar 读 META-INF/MANIFEST.MF，含 Spring-Boot-Lib / Spring-Boot-Classes / Spring-Boot-Version 任一行。
// 找到唯一 → 返回；多个 / 0 → 报 error，让上层报错列候选。
func pickSpringBootJar(jars []string) (string, error) {
	var hits []string
	for _, j := range jars {
		if isSpringBootJar(j) {
			hits = append(hits, j)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) == 0 {
		return "", errors.New("no spring boot jar detected")
	}
	return "", fmt.Errorf("multiple spring boot jars: %d", len(hits))
}

// isSpringBootJar 是否 Spring Boot 重新打包过的 fat jar
func isSpringBootJar(jarPath string) bool {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		// 行扫，只看前 50 行（manifest 一般很短）
		lines := strings.SplitN(string(data), "\n", 50)
		for _, ln := range lines {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "Spring-Boot-Lib:") ||
				strings.HasPrefix(ln, "Spring-Boot-Classes:") ||
				strings.HasPrefix(ln, "Spring-Boot-Version:") {
				return true
			}
		}
		return false
	}
	return false
}
