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
	Strategy      string     `gorm:"size:20" json:"strategy"` // build / rolling / blue_green / rollback
	Status        string     `gorm:"size:20" json:"status"`   // pending / running / success / failed / interrupted
	StateSnapshot string     `gorm:"type:text" json:"state_snapshot"`
	TriggeredBy   string     `gorm:"size:50" json:"triggered_by"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	CreatedAt     time.Time  `json:"created_at"`
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
