package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	apperr "swift-devops/internal/pkg/errors"
)

// BuilderEnvInput 更新构建环境的入参。
type BuilderEnvInput struct {
	JavaHome       string `json:"java_home"`
	MavenHome      string `json:"maven_home"`
	GitPath        string `json:"git_path,omitempty"`
	MavenLocalRepo string `json:"maven_local_repo,omitempty"` // Sprint X.9：空 = 用 settings.xml 默认
	DockerImage    string `json:"docker_image,omitempty"`     // Sprint 5.6：docker 构建镜像
}

// BuilderEnvView 响应视图。
type BuilderEnvView struct {
	JavaHome       string `json:"java_home"`
	MavenHome      string `json:"maven_home"`
	GitPath        string `json:"git_path"`
	MavenLocalRepo string `json:"maven_local_repo"`
	DockerImage    string `json:"docker_image"`
	JavaVersion    string `json:"java_version"`
	MavenVersion   string `json:"maven_version"`
	GitVersion     string `json:"git_version"`
	DockerVersion  string `json:"docker_version"`
	DetectedAt     string `json:"detected_at,omitempty"`
	Valid          bool   `json:"valid"`
	DetectMessage  string `json:"detect_message"`
}

func toBuilderEnvView(e *model.BuilderEnv) BuilderEnvView {
	v := BuilderEnvView{
		JavaHome: e.JavaHome, MavenHome: e.MavenHome, GitPath: e.GitPath,
		MavenLocalRepo: e.MavenLocalRepo, DockerImage: e.DockerImage,
		JavaVersion:    e.JavaVersion, MavenVersion: e.MavenVersion, GitVersion: e.GitVersion,
		DockerVersion:  e.DockerVersion,
		Valid: e.Valid, DetectMessage: e.DetectMessage,
	}
	if e.DetectedAt != nil {
		v.DetectedAt = e.DetectedAt.Format(time.RFC3339)
	}
	return v
}

// BuilderEnvService 构建机环境管理（单例：DB 里固定 id=1）。
type BuilderEnvService struct {
	db *gorm.DB
}

func NewBuilderEnvService(db *gorm.DB) *BuilderEnvService {
	return &BuilderEnvService{db: db}
}

// Load 取（不存在则创建空记录）。
func (s *BuilderEnvService) Load() (*model.BuilderEnv, error) {
	var e model.BuilderEnv
	err := s.db.First(&e, 1).Error
	if err == nil {
		return &e, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.Wrap(err, "INTERNAL", "load builder env", 500)
	}
	e = model.BuilderEnv{ID: 1}
	if err := s.db.Create(&e).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "create builder env", 500)
	}
	return &e, nil
}

// View 给 handler 用。
func (s *BuilderEnvService) View() (BuilderEnvView, error) {
	e, err := s.Load()
	if err != nil {
		return BuilderEnvView{}, err
	}
	return toBuilderEnvView(e), nil
}

// Update 保存配置（不自动 detect）。
func (s *BuilderEnvService) Update(in BuilderEnvInput) (BuilderEnvView, error) {
	if err := validatePathAbs("java_home", in.JavaHome, true); err != nil {
		return BuilderEnvView{}, err
	}
	if err := validatePathAbs("maven_home", in.MavenHome, true); err != nil {
		return BuilderEnvView{}, err
	}
	if err := validatePathAbs("git_path", in.GitPath, false); err != nil {
		return BuilderEnvView{}, err
	}
	if err := validatePathAbs("maven_local_repo", in.MavenLocalRepo, false); err != nil {
		return BuilderEnvView{}, err
	}
	e, err := s.Load()
	if err != nil {
		return BuilderEnvView{}, err
	}
	e.JavaHome = normalizePath(in.JavaHome)
	e.MavenHome = normalizePath(in.MavenHome)
	e.GitPath = normalizePath(in.GitPath)
	e.MavenLocalRepo = normalizePath(in.MavenLocalRepo)
	e.DockerImage = strings.TrimSpace(in.DockerImage) // 镜像名（如 maven:3.9-...），不做路径校验
	// 改完路径要重新 detect，先把 valid 清掉
	e.Valid = false
	e.DetectMessage = "已更新配置，请点「检测」按钮验证"
	if err := s.db.Save(e).Error; err != nil {
		return BuilderEnvView{}, apperr.Wrap(err, "INTERNAL", "save builder env", 500)
	}
	return toBuilderEnvView(e), nil
}

// Detect 跑 java -version / mvn -v / git --version 收集版本写回库。
func (s *BuilderEnvService) Detect() (BuilderEnvView, error) {
	e, err := s.Load()
	if err != nil {
		return BuilderEnvView{}, err
	}
	if strings.TrimSpace(e.JavaHome) == "" || strings.TrimSpace(e.MavenHome) == "" {
		e.Valid = false
		e.DetectMessage = "请先填写 java_home / maven_home 路径再检测"
		_ = s.db.Save(e).Error
		return toBuilderEnvView(e), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var msgs []string
	allOK := true

	// 1. java -version（输出到 stderr）
	javaBin := filepath.Join(e.JavaHome, "bin", "java")
	if !fileExecutable(javaBin) {
		msgs = append(msgs, fmt.Sprintf("✗ java 不可执行：%s", javaBin))
		allOK = false
		e.JavaVersion = ""
	} else {
		cmd := exec.CommandContext(ctx, javaBin, "-version")
		out, _ := cmd.CombinedOutput()
		ver := firstLine(string(out))
		if ver == "" {
			msgs = append(msgs, "✗ java -version 输出为空")
			allOK = false
		} else {
			e.JavaVersion = ver
			msgs = append(msgs, "✓ java: "+ver)
		}
	}

	// 2. mvn -v（mvn 自己需要 JAVA_HOME，传给子进程）
	mvnBin := filepath.Join(e.MavenHome, "bin", "mvn")
	if !fileExecutable(mvnBin) {
		msgs = append(msgs, fmt.Sprintf("✗ mvn 不可执行：%s", mvnBin))
		allOK = false
		e.MavenVersion = ""
	} else {
		cmd := exec.CommandContext(ctx, mvnBin, "-v")
		cmd.Env = append(os.Environ(),
			"JAVA_HOME="+e.JavaHome,
			"PATH="+filepath.Join(e.JavaHome, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		ver := firstLine(string(out))
		if err != nil || ver == "" {
			msgs = append(msgs, "✗ mvn -v 失败: "+strings.TrimSpace(string(out)))
			allOK = false
		} else {
			e.MavenVersion = ver
			msgs = append(msgs, "✓ mvn: "+ver)
		}
	}

	// 3. git --version
	gitBin := strings.TrimSpace(e.GitPath)
	if gitBin == "" {
		// Sprint X.8：不再 fallback 到 "git" 走 LookPath（WSL 会翻到 /mnt/c/.../git.exe）。
		// 兜底用硬路径 /usr/bin/git；存在即用，否则要求用户去 UI 配 GitPath 绝对路径。
		if fileExecutable("/usr/bin/git") {
			gitBin = "/usr/bin/git"
		} else {
			msgs = append(msgs, "✗ git_path 未配且 /usr/bin/git 不存在；请在「构建环境」填 git 绝对路径")
			allOK = false
		}
	}
	if gitBin != "" {
		cmd := exec.CommandContext(ctx, gitBin, "--version")
		out, err := cmd.CombinedOutput()
		ver := firstLine(string(out))
		if err != nil || ver == "" {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			msgs = append(msgs, "✗ git 不可用: "+strings.TrimSpace(string(out))+" ("+errStr+")")
			allOK = false
			e.GitVersion = ""
		} else {
			e.GitVersion = ver
			msgs = append(msgs, "✓ git: "+ver)
		}
	}

	// 4. Sprint 5.6：附带探一下 docker（仅记录版本，不影响本机模式的 valid——
	//    没装 docker 的本机构建用户照样 valid）。
	if out, derr := exec.CommandContext(ctx, "docker", "--version").CombinedOutput(); derr == nil {
		e.DockerVersion = firstLine(string(out))
		msgs = append(msgs, "✓ docker: "+e.DockerVersion)
	} else {
		e.DockerVersion = ""
		msgs = append(msgs, "○ docker 不可用（仅 docker 构建模式需要）")
	}

	now := time.Now()
	e.DetectedAt = &now
	e.Valid = allOK
	e.DetectMessage = strings.Join(msgs, "\n")
	if err := s.db.Save(e).Error; err != nil {
		return BuilderEnvView{}, apperr.Wrap(err, "INTERNAL", "save builder env detect", 500)
	}
	return toBuilderEnvView(e), nil
}

// BuildExecEnv 给 pkg/builder 用：组装出注入到 mvn/git exec.Cmd.Env 的环境变量。
// 在 os.Environ() 基础上覆盖 PATH / JAVA_HOME / M2_HOME。
// 当 env 未配置或无效时返回 nil，调用方走默认环境（兼容老路径）。
func (s *BuilderEnvService) BuildExecEnv() ([]string, error) {
	e, err := s.Load()
	if err != nil {
		return nil, err
	}
	if !e.Valid {
		return nil, nil
	}
	envs := os.Environ()
	// 把 java/mvn/git 的 bin 目录前置进 PATH，让 mvn 子进程能找到 java
	extraPath := filepath.Join(e.JavaHome, "bin") + string(os.PathListSeparator) +
		filepath.Join(e.MavenHome, "bin")
	if e.GitPath != "" {
		extraPath = filepath.Dir(e.GitPath) + string(os.PathListSeparator) + extraPath
	}
	envs = appendOrReplace(envs, "PATH", extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	envs = appendOrReplace(envs, "JAVA_HOME", e.JavaHome)
	envs = appendOrReplace(envs, "M2_HOME", e.MavenHome)
	envs = appendOrReplace(envs, "MAVEN_HOME", e.MavenHome)
	return envs, nil
}

// RequireValid 给 BuildService.Trigger 用：未检测 / 检测失败时拒绝触发。
func (s *BuilderEnvService) RequireValid() error {
	e, err := s.Load()
	if err != nil {
		return err
	}
	if !e.Valid {
		return apperr.New("BAD_REQUEST",
			"构建环境未配置或检测失败，请去「⚙ 构建环境」配置 java_home / maven_home 并点「检测」", 400)
	}
	return nil
}

// BuildPlanInputs 给 BuildService 注入到 builder.Plan 的可执行路径与 maven 配置。
// Sprint X.9：从 Bins() 重构而来，加 MavenLocalRepo 字段。
type BuildPlanInputs struct {
	MvnBin         string // mvn 可执行绝对路径（必非空）
	GitBin         string // git 可执行绝对路径（必非空，空时兜底 /usr/bin/git）
	MavenLocalRepo string // -Dmaven.repo.local；空 = 不传，让 mvn 用 settings.xml 默认（推荐）
}

// ResolveBuildInputs 收集本次构建所需的可执行路径与 maven 仓库设置。
// Sprint X.9：替代旧 Bins()，扩展支持 maven_local_repo。
//   - mvnBin = MavenHome/bin/mvn（valid 状态下必非空，文件不可执行报 400）
//   - gitBin = GitPath；空则兜底 /usr/bin/git；不存在报 400
//   - mavenLocalRepo = e.MavenLocalRepo（允许空 = 用 settings.xml 里的 <localRepository>）
func (s *BuilderEnvService) ResolveBuildInputs() (BuildPlanInputs, error) {
	e, err := s.Load()
	if err != nil {
		return BuildPlanInputs{}, err
	}
	if !e.Valid {
		return BuildPlanInputs{}, apperr.New("BAD_REQUEST",
			"构建环境未配置或检测失败，请去「⚙ 构建环境」检测后再触发构建", 400)
	}
	mvnBin := filepath.Join(e.MavenHome, "bin", "mvn")
	if !fileExecutable(mvnBin) {
		return BuildPlanInputs{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("maven_home/bin/mvn 不可执行：%s。请「构建环境」重新检测", mvnBin), 400)
	}
	gitBin := strings.TrimSpace(e.GitPath)
	if gitBin == "" {
		if fileExecutable("/usr/bin/git") {
			gitBin = "/usr/bin/git"
		} else {
			return BuildPlanInputs{}, apperr.New("BAD_REQUEST",
				"git_path 未配且 /usr/bin/git 不存在；请在「构建环境」填 git 绝对路径", 400)
		}
	}
	if !fileExecutable(gitBin) {
		return BuildPlanInputs{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("git_path 不可执行：%s。请「构建环境」重新检测", gitBin), 400)
	}
	return BuildPlanInputs{
		MvnBin:         mvnBin,
		GitBin:         gitBin,
		MavenLocalRepo: strings.TrimSpace(e.MavenLocalRepo), // 允许空
	}, nil
}

// DockerBuildInputs docker 构建模式注入 builder.Plan 的输入（Sprint 5.6）。
type DockerBuildInputs struct {
	GitBin        string // 宿主机 git（clone 仍在宿主机）
	DockerImage   string // 构建镜像
	MavenCacheDir string // 宿主机 .m2，挂载进容器；空 = 容器内每次重下
}

// ResolveDockerInputs docker 构建模式所需输入（Sprint 5.6）。
// 与 ResolveBuildInputs 不同：不要求本机 java/maven（容器自带），但要求：
//   - docker_image 已配
//   - git 可用（clone 仍在宿主机）
//   - docker daemon 可达（实时探一下，避免触发后才在 build log 里失败）
func (s *BuilderEnvService) ResolveDockerInputs() (DockerBuildInputs, error) {
	e, err := s.Load()
	if err != nil {
		return DockerBuildInputs{}, err
	}
	image := strings.TrimSpace(e.DockerImage)
	if image == "" {
		return DockerBuildInputs{}, apperr.New("BAD_REQUEST",
			"docker 构建已开启（docker_enabled），但「构建环境」未配 docker_image，请填镜像如 maven:3.9-eclipse-temurin-17", 400)
	}
	gitBin := strings.TrimSpace(e.GitPath)
	if gitBin == "" {
		if fileExecutable("/usr/bin/git") {
			gitBin = "/usr/bin/git"
		} else {
			return DockerBuildInputs{}, apperr.New("BAD_REQUEST",
				"git_path 未配且 /usr/bin/git 不存在；请在「构建环境」填 git 绝对路径", 400)
		}
	}
	if !fileExecutable(gitBin) {
		return DockerBuildInputs{}, apperr.New("BAD_REQUEST",
			fmt.Sprintf("git_path 不可执行：%s", gitBin), 400)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		return DockerBuildInputs{}, apperr.New("BAD_REQUEST",
			"docker 不可用：请确认宿主机已装 docker 且 swift-devops 进程有权限（在 docker 组或 root）", 400)
	}
	return DockerBuildInputs{
		GitBin:        gitBin,
		DockerImage:   image,
		MavenCacheDir: strings.TrimSpace(e.MavenLocalRepo),
	}, nil
}

// --- 辅助 ---

func fileExecutable(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	// Unix: 检 0111 任一位
	return info.Mode()&0o111 != 0
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func validatePathAbs(name, p string, required bool) error {
	p = strings.TrimSpace(p)
	if p == "" {
		if required {
			return apperr.New("BAD_REQUEST", name+" 不能为空", 400)
		}
		return nil
	}
	// 拒绝 Windows 反斜杠 —— Linux 路径必须用 /
	if strings.Contains(p, `\`) {
		return apperr.New("BAD_REQUEST",
			name+" 含反斜杠 \\，请用 Linux 正斜杠 /（如 /mnt/d/develop/jdk-21）", 400)
	}
	if !filepath.IsAbs(p) {
		return apperr.New("BAD_REQUEST", name+" 必须是绝对路径（以 / 开头）", 400)
	}
	return nil
}

// normalizePath 归一化路径：trim 空白 + 去尾部 /（除非是根 /）
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// appendOrReplace 在 envs（KEY=VALUE 列表）里把 key 替换成 value，没有则追加。
func appendOrReplace(envs []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range envs {
		if strings.HasPrefix(kv, prefix) {
			envs[i] = prefix + value
			return envs
		}
	}
	return append(envs, prefix+value)
}
