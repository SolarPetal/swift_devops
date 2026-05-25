package model

import "time"

// Host 目标主机
type Host struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"size:100;not null" json:"name"`
	IP        string `gorm:"size:50;not null;index" json:"ip"`
	Port      int    `gorm:"default:22" json:"port"`
	AuthType  string `gorm:"size:20" json:"auth_type"` // password / key
	Username  string `gorm:"size:50" json:"username"`
	Secret    string `gorm:"type:text" json:"-"`       // 加密后的密码或私钥
	HostKey   string `gorm:"type:text" json:"-"`       // SSH host key (TOFU)
	Status    string `gorm:"size:20;default:'unknown'" json:"status"`
	Tags      string `gorm:"type:text" json:"tags"`    // JSON 数组
	GroupTag  string `gorm:"size:20" json:"group_tag"` // blue / green
	// JavaPath: 该主机上 java 可执行文件的绝对路径。空 = /usr/bin/java（默认）。
	// Sprint 3.7 蓝绿/部署前 env_check 用，避免远端 java 不在 PATH 或路径不同导致 203/EXEC。
	JavaPath  string `gorm:"size:255" json:"java_path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Application 业务应用
type Application struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	AppCode        string `gorm:"size:50;uniqueIndex;not null" json:"app_code"`
	Name           string `gorm:"size:100;not null" json:"name"`
	AppType        string `gorm:"size:30" json:"app_type"` // jar / spring-cloud
	GitURL         string `gorm:"size:255" json:"git_url"`
	GitCredID      string `gorm:"size:100" json:"git_cred_id"`
	DeployPath     string `gorm:"size:255;not null" json:"deploy_path"`
	Port           int    `gorm:"not null" json:"port"`
	HealthCheckURL string `gorm:"size:255;default:'/actuator/health'" json:"health_check_url"`
	JvmArgs        string `gorm:"type:text" json:"jvm_args"`
	EnvVars        string `gorm:"type:text" json:"env_vars"` // JSON
	SystemdUser    string `gorm:"size:32" json:"systemd_user"` // 空=root；非空写入 unit 的 User= 字段
	// JavaPath: 应用级 java 可执行路径覆盖（可选）。空 = 沿用主机 Host.JavaPath。
	// 适用场景：同一台主机跑多个 JDK 版本的应用（jdk8 / jdk17 等）。
	JavaPath          string `gorm:"size:255" json:"java_path"`
	// Sprint 4 蓝绿：nginx 配置。空 = 未启用蓝绿。
	// NginxHostID 指向 hosts 表中跑 nginx 的主机；NginxUpstreamName 是该 nginx 中的 upstream 名。
	// ActiveGroup 记录当前对外提供服务的组（blue/green/空）；空 = 首次部署前。
	NginxHostID       uint   `gorm:"index" json:"nginx_host_id"`
	NginxUpstreamName string `gorm:"size:100" json:"nginx_upstream_name"`
	ActiveGroup       string `gorm:"size:20" json:"active_group"` // blue / green / 空
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Artifact 制品
type Artifact struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	AppID        uint   `gorm:"index;not null" json:"app_id"`
	VersionTag   string `gorm:"size:50;not null" json:"version_tag"`
	FileName     string `gorm:"size:255;not null" json:"file_name"`
	FilePath     string `gorm:"size:255;not null" json:"file_path"`
	FileMD5      string `gorm:"size:32;not null" json:"file_md5"`
	FileSize     int64  `json:"file_size"`
	BuildStatus  string `gorm:"size:20;not null" json:"build_status"` // success / failed / building
	BuildLogPath string `gorm:"size:255" json:"build_log_path"`
	CreatedAt    time.Time `json:"created_at"`
}

// Deployment 应用×主机部署关系
type Deployment struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	AppID              uint   `gorm:"uniqueIndex:idx_app_host;not null" json:"app_id"`
	HostID             uint   `gorm:"uniqueIndex:idx_app_host;not null" json:"host_id"`
	GroupTag           string `gorm:"size:20" json:"group_tag"` // blue / green
	CurrentArtifactID  uint   `json:"current_artifact_id"`
	PreviousArtifactID uint   `json:"previous_artifact_id"` // 一键回滚用
	Port               int    `json:"port"`
	Status             string `gorm:"size:20" json:"status"` // running / stopped / failed
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// PipelineRun 一次发布或构建任务
type PipelineRun struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	AppID         uint       `gorm:"index;not null" json:"app_id"`
	ArtifactID    uint       `json:"artifact_id"`
	Strategy      string     `gorm:"size:20" json:"strategy"` // build / single / rolling / blue_green / rollback
	Status        string     `gorm:"size:20" json:"status"`   // pending / running / success / failed / cancelled
	StateSnapshot string     `gorm:"type:text" json:"state_snapshot"`
	TriggeredBy   string     `gorm:"size:50" json:"triggered_by"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PipelineRunHost 单次 run 在某台主机上的执行状态。
// 与 StateSnapshot.steps 互补：steps 是阶段级时序，本表是 host 级聚合，便于 SQL 查询和未来 dashboard。
type PipelineRunHost struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	RunID        uint       `gorm:"uniqueIndex:idx_run_host;not null;index" json:"run_id"`
	HostID       uint       `gorm:"uniqueIndex:idx_run_host;not null" json:"host_id"`
	DeploymentID uint       `gorm:"index" json:"deployment_id"`
	Status       string     `gorm:"size:20;not null" json:"status"`       // pending / running / success / failed / skipped
	CurrentStage string     `gorm:"size:20" json:"current_stage"`         // dial / upload / write_unit / restart / health
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
//   - 成功时 ArtifactID 指向落库的 Artifact，前端可直接基于此触发部署
//   - LogPath 是相对 server 文件系统的绝对路径，里面是 git clone + mvn package 的合并输出
type BuildRun struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	AppID       uint       `gorm:"index;not null" json:"app_id"`
	GitRef      string     `gorm:"size:100" json:"git_ref"`     // 分支 / tag / commit
	CommitSHA   string     `gorm:"size:40" json:"commit_sha"`   // clone 后从 git rev-parse 回填
	MvnArgs     string     `gorm:"size:255" json:"mvn_args"`    // 用户指定的额外 mvn 参数；空 = 默认
	CredID      uint       `gorm:"index" json:"cred_id"`        // 关联 GitCredential；0 = 无凭证（公网仓）
	Status      string     `gorm:"size:20;not null" json:"status"` // building / success / failed / cancelled
	LogPath     string     `gorm:"size:255" json:"log_path"`    // build log 文件绝对路径
	ArtifactID  uint       `gorm:"index" json:"artifact_id"`    // 成功时回填指向 Artifact
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
	ID            uint       `gorm:"primaryKey" json:"id"` // 固定 1
	JavaHome      string     `gorm:"size:255" json:"java_home"`
	MavenHome     string     `gorm:"size:255" json:"maven_home"`
	GitPath       string     `gorm:"size:255" json:"git_path"` // 一般 /usr/bin/git；空 = 走 PATH 找
	JavaVersion   string     `gorm:"size:100" json:"java_version"`
	MavenVersion  string     `gorm:"size:100" json:"maven_version"`
	GitVersion    string     `gorm:"size:100" json:"git_version"`
	DetectedAt    *time.Time `json:"detected_at"`
	Valid         bool       `json:"valid"`  // 上次检测是否全部命中
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
