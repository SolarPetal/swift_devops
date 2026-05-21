// Package strategy 把 pipeline 的策略层（如何编排多主机部署）从 service 主流程解耦。
// 拆分动机：
//   - service 主流程只管"触发→落库→分派"，策略只管"按什么节奏跑、失败怎么停"
//   - 后续 Rolling / Rollback / BlueGreen 各自独立文件，互不污染
//   - 策略对副作用通过 Hooks 表达，service 层注入实现，测试可 mock
package strategy

import (
	"time"

	"swift-devops/internal/model"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// 部署阶段名（前端按这些固定 string 渲染时序卡片）。
const (
	StageDial    = "dial"
	StageUpload  = "upload"
	StageUnit    = "write_unit"
	StageRestart = "restart"
	StageHealth  = "health"
)

// Host 级状态机：与 model.PipelineRunHost.Status 字段一一对应。
const (
	HostStatusPending = "pending"
	HostStatusRunning = "running"
	HostStatusSuccess = "success"
	HostStatusFailed  = "failed"
	HostStatusSkipped = "skipped" // fail-fast 或 cancel 时未跑到的主机
)

// StepResult 单步骤结果（按 host × stage 粒度）。
// 前端按 host 聚合渲染时序卡片。
type StepResult struct {
	HostID    uint   `json:"host_id"`
	HostName  string `json:"host_name"`
	HostIP    string `json:"host_ip"`
	Stage     string `json:"stage"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
}

// RunSnapshot 流水线快照（写到 PipelineRun.StateSnapshot）。
type RunSnapshot struct {
	Strategy   string       `json:"strategy"`
	ArtifactID uint         `json:"artifact_id"`
	StartedAt  string       `json:"started_at"`
	FinishedAt string       `json:"finished_at,omitempty"`
	Steps      []StepResult `json:"steps"`
	Error      string       `json:"error,omitempty"`
}

// HostOutcome 一台主机本次 run 的最终结局。
// 策略 Run 完返回数组，给上层 finishRun 用来落 PipelineRunHost 表 / 决定整体 status。
type HostOutcome struct {
	HostID       uint
	DeploymentID uint
	Status       string // strategy.HostStatus*
	CurrentStage string
	Err          string
	StartedAt    *time.Time
	EndedAt      *time.Time
}

// Plan 一次 run 的执行计划（数据 + 配置）。
// service 层组装，策略只读。
type Plan struct {
	RunID           uint
	App             *model.Application
	Artifact        *model.Artifact            // forward 模式（single/rolling）的统一新版本；rollback 不用
	ArtifactByDepID map[uint]*model.Artifact // rollback 用：每个 deployment 的 previous_artifact_id 对应的 art
	Deps            []model.Deployment        // 已按 ID ASC 排好序
	EnvMap          map[string]string         // 已解析的 env vars
	BatchSize       int                       // rolling 专用；single/rollback 忽略；0/1 退化为单批
}

// HostLoader 从主机 ID 还原拨号参数（隔离对 HostService 的依赖，便于 mock）。
type HostLoader interface {
	LoadAuth(id uint) (sshpkg.HostTarget, sshpkg.AuthMethod, *model.Host, error)
	RecordHostKey(id uint, key string)
}

// Env 策略执行环境：跨策略共享的工具与配置。
type Env struct {
	HostSvc HostLoader
	SSHOpts sshpkg.DialOptions
}

// Hooks 策略向外抛副作用（推送 WS、写 PipelineRunHost、更新 deployment 制品指针）。
// 实现需 goroutine 安全；策略可能并发调用（Rolling 同批多主机）。
type Hooks interface {
	// OnStep 每完成一个阶段（成功或失败）调一次，用于实时 WS 推送。
	OnStep(step StepResult)
	// OnHostStatus host 级状态变更：进入 running / 成功 / 失败 / 跳过。
	OnHostStatus(deploymentID, hostID uint, status, currentStage, errMsg string)
	// OnDeploymentSuccess 单台主机部署成功，更新 deployment 表的制品指针。
	OnDeploymentSuccess(dep *model.Deployment, newArtifactID uint)
}
