package model

import "time"

// Host 目标主机
type Host struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"size:100;not null" json:"name"`
	IP       string `gorm:"size:50;not null;index" json:"ip"`
	Port     int    `gorm:"default:22" json:"port"`
	AuthType string `gorm:"size:20" json:"auth_type"` // password / key
	Username string `gorm:"size:50" json:"username"`
	Secret   string `gorm:"type:text" json:"-"` // 加密后的密码或私钥
	HostKey  string `gorm:"type:text" json:"-"` // SSH host key (TOFU)
	Status   string `gorm:"size:20;default:'unknown'" json:"status"`
	Tags     string `gorm:"type:text" json:"tags"`    // JSON 数组
	GroupTag string `gorm:"size:20" json:"group_tag"` // blue / green
	// JavaPath: 该主机上 java 可执行文件的绝对路径。空 = /usr/bin/java（默认）。
	// Sprint 3.7 蓝绿/部署前 env_check 用，避免远端 java 不在 PATH 或路径不同导致 203/EXEC。
	JavaPath  string    `gorm:"size:255" json:"java_path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Application 业务应用
//
// Sprint X.1 重构：从"单体 jar"演化为"业务系统层"。
// per-runtime 字段（port/health/jvm/env/systemd_user/java_path/build_module/build_jar_pattern
// /nginx_*/active_group）将逐步下沉到 [AppService]，X.1 阶段仍保留以兼容老 service/strategy/handler 代码；
// 等 Sprint X.3/X.4 切流完成后再清掉。
//
// 新增 GitRef 字段：默认分支放顶层，多 service 共享一次构建。
type Application struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	AppCode   string `gorm:"size:50;uniqueIndex;not null" json:"app_code"`
	Name      string `gorm:"size:100;not null" json:"name"`
	AppType   string `gorm:"size:30" json:"app_type"` // jar / spring-cloud
	GitURL    string `gorm:"size:255" json:"git_url"`
	GitCredID string `gorm:"size:100" json:"git_cred_id"`
	// GitRef Sprint X.1：默认分支/tag/commit，多 service 一次构建共用。
	// 触发构建时若未显式传 git_ref，使用本字段；空则兜底 "main"。
	GitRef     string `gorm:"size:100" json:"git_ref"`
	GitRefs    string `gorm:"type:text" json:"git_refs"` // JSON array：应用可选构建 Ref 列表
	DeployPath string `gorm:"size:255;not null" json:"deploy_path"`

	// ===== 以下字段 Sprint X.1 标记为"待下沉到 AppService"，X.4 删除 =====
	Port           int    `gorm:"not null" json:"port"`
	HealthCheckURL string `gorm:"size:255;default:'/actuator/health'" json:"health_check_url"`
	JvmArgs        string `gorm:"type:text" json:"jvm_args"`
	EnvVars        string `gorm:"type:text" json:"env_vars"`   // JSON
	SystemdUser    string `gorm:"size:32" json:"systemd_user"` // 空=root；非空写入 unit 的 User= 字段
	// JavaPath: 应用级 java 可执行路径覆盖（可选）。空 = 沿用主机 Host.JavaPath。
	// 适用场景：同一台主机跑多个 JDK 版本的应用（jdk8 / jdk17 等）。
	JavaPath string `gorm:"size:255" json:"java_path"`
	// BuildMode 构建模式（Sprint X.11 重构）：
	//   "local-jar"      → 本机 Maven 打包 jar（默认，向后兼容）
	//   "local-docker"   → 本机 Maven 打包 → Dockerfile → docker build → push 到镜像仓库
	//   "remote-docker"  → 推送代码到远端构建机 → docker build → push 到镜像仓库
	BuildMode string `gorm:"size:20;default:'local-jar'" json:"build_mode"`

	// DeployMode 部署模式（Sprint X.10 + X.11 扩展）：
	//   "" / "systemd" → 写 /etc/systemd/system/devops-<app>.service + systemctl 管控（默认，向后兼容）
	//   "nohup"        → 写 <deploy_path>/start.sh + nohup java -jar + app.pid 守护（免 root，crash 不自愈）
	//   "docker"       → docker pull + docker run（Sprint X.11 新增）
	// 单 service 应用沿用本字段；多 service 链路下，AppService.DeployMode 覆盖本字段。
	DeployMode string `gorm:"size:20;default:'systemd'" json:"deploy_mode"`

	// Docker 构建配置（Sprint X.11）
	DockerRegistry  string `gorm:"size:255" json:"docker_registry"`    // 镜像仓库地址，如 docker.io / harbor.example.com
	DockerImageName string `gorm:"size:255" json:"docker_image_name"`  // 镜像名，如 myapp/user-service
	DockerImageTag  string `gorm:"size:100" json:"docker_image_tag"`   // 镜像标签模板，如 git-{sha}-{build_id} / latest
	Dockerfile      string `gorm:"type:text" json:"dockerfile"`        // 自定义 Dockerfile（可选，空则自动生成）
	DockerBuildArgs string `gorm:"type:text" json:"docker_build_args"` // docker build 参数，如 --build-arg ENV=prod

	// Docker 部署配置（Sprint X.11）
	DockerRunArgs string `gorm:"type:text" json:"docker_run_args"` // docker run 参数，如 -p 8080:8080 -e ENV=prod --restart=always
	// Sprint 5.4.7 构建：multi-module 项目用
	// BuildModule 非空 → mvn -pl <module> -am；只编译该模块及其依赖，加速 + 减少 jar 命中
	// BuildJarPattern 非空 → glob 在 workspace 下匹配 jar；为空走 builder 默认扫描+Spring Boot 探测
	BuildModule     string `gorm:"size:100" json:"build_module"`
	BuildJarPattern string `gorm:"size:255" json:"build_jar_pattern"`
	// Sprint 4 蓝绿：nginx 配置。空 = 未启用蓝绿。
	// NginxHostID 指向 hosts 表中跑 nginx 的主机；NginxUpstreamName 是该 nginx 中的 upstream 名。
	// ActiveGroup 记录当前对外提供服务的组（blue/green/空）；空 = 首次部署前。
	NginxHostID       uint   `gorm:"index" json:"nginx_host_id"`
	NginxUpstreamName string `gorm:"size:100" json:"nginx_upstream_name"`
	ActiveGroup       string `gorm:"size:20" json:"active_group"` // blue / green / 空
	// ===== 待下沉字段结束 =====

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppService 微服务层 —— Sprint X.1 新增。
// 一个 Application 可挂 N 个 AppService（如 nacos 微服务里的 eureka / gateway / user-service / order-service）。
//
// 设计原则：
//   - 单体 jar 应用 = 1 行 AppService（service_code 与 app_code 同名或 "default"）
//   - per-runtime 字段（port / health / jvm / env / systemd_user / java_path）全部下沉到这里
//   - 构建参数（build_module / build_jar_pattern）per-service：multi-module 项目每个 service 选一个 module
//   - 启动序：StartupOrder 整数排序，小先起，同值并发；nacos 一般不在 swift-devops 管控，所以业务服务可全部用同一 wave
//   - 蓝绿：per-service 蓝绿（Q1 答案 A）—— gateway 才挂 nginx_*，业务服务用 rolling
//   - Optional：true 时该 service 失败不阻塞整个 run，snapshot 标 warning
//   - Enabled：软下线开关，false 时部署/构建跳过该 service
type AppService struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	AppID       uint   `gorm:"uniqueIndex:idx_app_service;not null;index" json:"app_id"`
	ServiceCode string `gorm:"uniqueIndex:idx_app_service;size:50;not null" json:"service_code"` // ^[a-z][a-z0-9-]{1,49}$
	Name        string `gorm:"size:100" json:"name"`                                             // 展示名；空走 service_code

	// 构建参数（multi-module Spring Boot）
	BuildModule     string `gorm:"size:100" json:"build_module"`      // mvn -pl 用；空 = 全量 mvn package（单 module 项目）
	BuildJarPattern string `gorm:"size:255" json:"build_jar_pattern"` // glob，空走默认扫描 + Spring Boot 探测

	// per-runtime
	Port           int    `gorm:"not null" json:"port"`
	HealthCheckURL string `gorm:"size:255;default:'/actuator/health'" json:"health_check_url"`
	JvmArgs        string `gorm:"type:text" json:"jvm_args"`
	EnvVars        string `gorm:"type:text" json:"env_vars"` // JSON
	SystemdUser    string `gorm:"size:32" json:"systemd_user"`
	JavaPath       string `gorm:"size:255" json:"java_path"` // 空 = 沿用 Host.JavaPath
	// DeployMode 部署模式（Sprint X.10 + X.11 扩展）：
	//   "" / "systemd" → systemd unit + systemctl（默认，向后兼容）
	//   "nohup"        → nohup java -jar + app.pid（免 root，crash 不自愈）
	//   "docker"       → docker pull + docker run（Sprint X.11 新增）
	// 空时回退 Application.DeployMode；都空 → "systemd"。
	DeployMode string `gorm:"size:20;default:'systemd'" json:"deploy_mode"`

	// Docker 配置（Sprint X.11）—— 空时回退 Application 的对应字段
	DockerRegistry  string `gorm:"size:255" json:"docker_registry"`
	DockerImageName string `gorm:"size:255" json:"docker_image_name"`
	DockerImageTag  string `gorm:"size:100" json:"docker_image_tag"`
	Dockerfile      string `gorm:"type:text" json:"dockerfile"`
	DockerBuildArgs string `gorm:"type:text" json:"docker_build_args"`
	DockerRunArgs   string `gorm:"type:text" json:"docker_run_args"`

	// 编排
	StartupOrder int  `gorm:"default:100;index" json:"startup_order"` // 0=注册中心，10=网关，100=业务（默认）
	Optional     bool `gorm:"default:false" json:"optional"`          // true = 失败不阻塞 run
	Enabled      bool `gorm:"default:true" json:"enabled"`            // 软下线

	// 蓝绿（per-service）—— gateway 才用
	NginxHostID       uint   `gorm:"index" json:"nginx_host_id"`
	NginxUpstreamName string `gorm:"size:100" json:"nginx_upstream_name"`
	ActiveGroup       string `gorm:"size:20" json:"active_group"` // blue / green / 空

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Artifact 制品（旧表，Sprint X.1 之前的单 jar 模型）。
//
// Sprint X.1：保留兼容老 service/handler/strategy 代码；新代码请用 [ArtifactBundle] + [ArtifactItem]。
// 等 Sprint X.4 完成切换后，本表退役（数据已在 X.1 cleanup 清空）。
type Artifact struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AppID        uint      `gorm:"index;not null" json:"app_id"`
	VersionTag   string    `gorm:"size:50;not null" json:"version_tag"`
	FileName     string    `gorm:"size:255;not null" json:"file_name"`
	FilePath     string    `gorm:"size:255;not null" json:"file_path"`
	FileMD5      string    `gorm:"size:32;not null" json:"file_md5"`
	FileSize     int64     `json:"file_size"`
	BuildStatus  string    `gorm:"size:20;not null" json:"build_status"` // success / failed / building
	BuildLogPath string    `gorm:"size:255" json:"build_log_path"`
	CreatedAt    time.Time `json:"created_at"`
}

// ArtifactBundle 版本一致性层 —— Sprint X.1 新增。
// 一次构建 = 一个 Bundle = N 个 ArtifactItem（每个 AppService 一个 jar）。
//
// git_commit_sha 是"这 N 个 jar 是同一份代码出来的"的硬证据；回滚以 Bundle 为粒度（Q2 答案 A）。
type ArtifactBundle struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AppID        uint      `gorm:"uniqueIndex:idx_bundle_app_version;not null;index" json:"app_id"`
	VersionTag   string    `gorm:"uniqueIndex:idx_bundle_app_version;size:50;not null" json:"version_tag"`
	GitCommitSHA string    `gorm:"size:40;index" json:"git_commit_sha"`
	BuildStatus  string    `gorm:"size:20;not null" json:"build_status"` // success / failed / building
	BuildLogPath string    `gorm:"size:255" json:"build_log_path"`       // 整组共享一份构建日志
	TriggeredBy  string    `gorm:"size:50" json:"triggered_by"`
	CreatedAt    time.Time `json:"created_at"`
}

// ArtifactItem 产物明细 —— Sprint X.1 新增。
// 每行 = Bundle 内一个 AppService 的 jar 文件。
type ArtifactItem struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	BundleID    uint      `gorm:"uniqueIndex:idx_item_bundle_service;not null;index" json:"bundle_id"`
	ServiceCode string    `gorm:"uniqueIndex:idx_item_bundle_service;size:50;not null" json:"service_code"`
	FileName    string    `gorm:"size:255;not null" json:"file_name"`
	FilePath    string    `gorm:"size:255;not null" json:"file_path"`
	FileMD5     string    `gorm:"size:32;not null" json:"file_md5"`
	FileSize    int64     `json:"file_size"`
	CreatedAt   time.Time `json:"created_at"`
}

// Deployment 应用×主机×服务 部署关系。
//
// Sprint X.1：唯一索引从 (app_id, host_id) 升级为 (app_id, host_id, service_code)，
// 因为同一台主机可能跑多个 AppService。
//   - 旧索引 idx_app_host 由 X.1 cleanup 脚本手动 DROP（GORM AutoMigrate 不会删旧索引）
//   - 新增字段 ServiceCode：和 [AppService.ServiceCode] 配对
//   - 新增字段 CurrentArtifactItemID / PreviousArtifactItemID：替代旧的 CurrentArtifactID / PreviousArtifactID
//     旧 Artifact*ID 字段 X.4 删除
type Deployment struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	AppID       uint   `gorm:"uniqueIndex:idx_app_host_service;not null" json:"app_id"`
	HostID      uint   `gorm:"uniqueIndex:idx_app_host_service;not null" json:"host_id"`
	ServiceCode string `gorm:"uniqueIndex:idx_app_host_service;size:50;not null;default:''" json:"service_code"` // Sprint X.1
	GroupTag    string `gorm:"size:20" json:"group_tag"`                                                         // blue / green
	// 旧产物指针（X.4 删除）
	CurrentArtifactID  uint `json:"current_artifact_id"`
	PreviousArtifactID uint `json:"previous_artifact_id"`
	// 新产物指针（X.1 新增，指向 ArtifactItem）
	CurrentArtifactItemID  uint      `json:"current_artifact_item_id"`
	PreviousArtifactItemID uint      `json:"previous_artifact_item_id"`
	Port                   int       `json:"port"`
	Status                 string    `gorm:"size:20" json:"status"` // running / stopped / failed
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// PipelineRun 一次发布或构建任务。
//
// Sprint X.1：新增 BundleID / PreviousBundleID（指向 [ArtifactBundle]）；
// 旧 ArtifactID 保留兼容，X.4 删除。回滚走整组 Bundle 粒度（Q2 答案 A）。
type PipelineRun struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	AppID            uint       `gorm:"index;not null" json:"app_id"`
	ArtifactID       uint       `json:"artifact_id"`                     // 旧字段，X.4 删除
	BundleID         uint       `gorm:"index" json:"bundle_id"`          // Sprint X.1：当前发布的 Bundle
	PreviousBundleID uint       `gorm:"index" json:"previous_bundle_id"` // Sprint X.1：整组回滚指针
	Strategy         string     `gorm:"size:20" json:"strategy"`         // build / single / rolling / blue_green / rollback
	Status           string     `gorm:"size:20" json:"status"`           // pending / running / success / failed / cancelled
	StateSnapshot    string     `gorm:"type:text" json:"state_snapshot"`
	TriggeredBy      string     `gorm:"size:50" json:"triggered_by"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// PipelineRunHost 单次 run 在某台主机×服务上的执行状态。
// 与 StateSnapshot.steps 互补：steps 是阶段级时序，本表是 host×service 级聚合。
//
// Sprint X.1：唯一索引从 (run_id, host_id) 升级为 (run_id, host_id, service_code)，
// 因为同一 run 在同一 host 上可能部署多个 service。旧索引 idx_run_host 由 cleanup 脚本 DROP。
type PipelineRunHost struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	RunID        uint       `gorm:"uniqueIndex:idx_run_host_service;not null;index" json:"run_id"`
	HostID       uint       `gorm:"uniqueIndex:idx_run_host_service;not null" json:"host_id"`
	ServiceCode  string     `gorm:"uniqueIndex:idx_run_host_service;size:50;not null;default:''" json:"service_code"` // Sprint X.1
	DeploymentID uint       `gorm:"index" json:"deployment_id"`
	Status       string     `gorm:"size:20;not null" json:"status"` // pending / running / success / failed / skipped
	CurrentStage string     `gorm:"size:20" json:"current_stage"`   // dial / upload / write_unit / restart / health
	StartedAt    *time.Time `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at"`
	Error        string     `gorm:"type:text" json:"error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// GitCredential Git 仓库凭证（Sprint 5.1）。
// 同一份凭证可被多个 Application 复用（按 GitCredID 引用，但 Sprint 5 暂不绑 app，触发构建时按需选）。
//   - Type=token: Secret 存 HTTPS PAT，Username 存用户名（GitHub 一般是 token 拥有者；GitLab 可填 oauth2 等）
//   - Type=ssh_key: Secret 存 PEM 私钥，Username 一般为 "git"
type GitCredential struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:100;uniqueIndex;not null" json:"name"`
	Type      string    `gorm:"size:20;not null" json:"type"` // token / ssh_key
	Username  string    `gorm:"size:100" json:"username"`
	Secret    string    `gorm:"type:text" json:"-"` // AES-GCM 密文；外部一律 has_secret=true 占位
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BuildRun 一次构建任务（Sprint 5.2）。
//   - 与 PipelineRun 平级；构建/部署各自独立工作流
//   - 成功时 ArtifactID 指向落库的 Artifact（旧链路，X.4 删除），BundleID 指向 ArtifactBundle（X.2 新链路）
//   - LogPath 是相对 server 文件系统的绝对路径，里面是 git clone + mvn package 的合并输出
type BuildRun struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	AppID       uint       `gorm:"index;not null" json:"app_id"`
	GitRef      string     `gorm:"size:100" json:"git_ref"`        // 分支 / tag / commit
	CommitSHA   string     `gorm:"size:40" json:"commit_sha"`      // clone 后从 git rev-parse 回填
	MvnArgs     string     `gorm:"size:255" json:"mvn_args"`       // 用户指定的额外 mvn 参数；空 = 默认
	CredID      uint       `gorm:"index" json:"cred_id"`           // 关联 GitCredential；0 = 无凭证（公网仓）
	Status      string     `gorm:"size:20;not null" json:"status"` // building / success / failed / cancelled
	LogPath     string     `gorm:"size:255" json:"log_path"`       // build log 文件绝对路径
	ArtifactID  uint       `gorm:"index" json:"artifact_id"`       // 旧：单 jar 链路，X.4 删除
	BundleID    uint       `gorm:"index" json:"bundle_id"`         // Sprint X.2：成功时回填整组 Bundle
	TriggeredBy string     `gorm:"size:50" json:"triggered_by"`
	Error       string     `gorm:"type:text" json:"error"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// BuilderEnv 构建机环境配置（Sprint 5.4，单行表，固定 ID=1）。
// 用于把 swift-devops 进程所在主机的 JAVA_HOME / MAVEN_HOME / GIT 路径
// 透传给构建用的 mvn 子进程，避免依赖 systemd 启动时的环境变量。
type BuilderEnv struct {
	ID        uint   `gorm:"primaryKey" json:"id"` // 固定 1
	JavaHome  string `gorm:"size:255" json:"java_home"`
	MavenHome string `gorm:"size:255" json:"maven_home"`
	GitPath   string `gorm:"size:255" json:"git_path"` // 一般 /usr/bin/git；空 = 走 PATH 找
	// Sprint X.9：maven 本地仓库（覆盖 settings.xml 里的 <localRepository>）。
	// 空 = 不传 -Dmaven.repo.local，让 mvn 自己用 settings.xml 默认（推荐）。
	// 非空必须绝对路径，触发构建时作为 -Dmaven.repo.local=<dir> 传给 mvn。
	MavenLocalRepo string `gorm:"size:255" json:"maven_local_repo"`
	// Sprint 5.6：Docker 构建镜像（如 maven:3.9-eclipse-temurin-17）。
	// 仅 config.builder.docker_enabled=true 时生效；docker 模式下为空会拒绝触发构建。
	DockerImage   string     `gorm:"size:255" json:"docker_image"`
	JavaVersion   string     `gorm:"size:100" json:"java_version"`
	MavenVersion  string     `gorm:"size:100" json:"maven_version"`
	GitVersion    string     `gorm:"size:100" json:"git_version"`
	DockerVersion string     `gorm:"size:100" json:"docker_version"` // Sprint 5.6：detect 回填 docker --version
	DetectedAt    *time.Time `json:"detected_at"`
	Valid         bool       `json:"valid"` // 上次检测是否全部命中
	DetectMessage string     `gorm:"type:text" json:"detect_message"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AuditLog 审计日志
type AuditLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Actor        string    `gorm:"size:50;index" json:"actor"`
	Action       string    `gorm:"size:50" json:"action"`
	ResourceType string    `gorm:"size:50" json:"resource_type"`
	ResourceID   string    `gorm:"size:100" json:"resource_id"`
	Payload      string    `gorm:"type:text" json:"payload"`
	IP           string    `gorm:"size:50" json:"ip"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}
