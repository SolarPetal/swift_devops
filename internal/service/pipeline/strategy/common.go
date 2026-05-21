package strategy

import (
	"context"
	"fmt"
	"time"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// deployHost 在单台主机上跑完 5 个部署阶段（dial → upload → write_unit → restart → health）。
// 任一阶段失败立即返回，hooks.OnStep 已经把当前失败步骤喷出去。
// 成功完成则 hooks.OnDeploymentSuccess 更新 deployment 表。
//
// 返回值：HostOutcome（含 status / stage / err）+ 该主机的所有 step 序列。
// ctx 应用于：拨号超时 + systemctl 命令超时 + 健康探针超时（probe 内部尊重 ctx）。
//
// 注意：本函数不会因 ctx 取消而提前 return ——已开始的部署应跑完当前 SSH 会话，
// 由策略层在调用前判断 ctx.Err() != nil 决定是否进入这台主机。
func deployHost(ctx context.Context, env Env, plan *Plan, dep *model.Deployment, hooks Hooks) (HostOutcome, []StepResult) {
	app := plan.App
	art := plan.Artifact

	started := time.Now()
	outcome := HostOutcome{
		HostID:       dep.HostID,
		DeploymentID: dep.ID,
		Status:       HostStatusRunning,
		StartedAt:    &started,
	}
	hooks.OnHostStatus(dep.ID, dep.HostID, HostStatusRunning, StageDial, "")

	var steps []StepResult
	emit := func(st StepResult) {
		steps = append(steps, st)
		hooks.OnStep(st)
	}
	fail := func(stage, errMsg string, stepStart time.Time, hostName, hostIP string, detail string) (HostOutcome, []StepResult) {
		emit(mkStepTimed(dep.HostID, hostName, hostIP, stage, false, detail, errMsg, stepStart))
		ended := time.Now()
		outcome.Status = HostStatusFailed
		outcome.CurrentStage = stage
		outcome.Err = errMsg
		outcome.EndedAt = &ended
		hooks.OnHostStatus(dep.ID, dep.HostID, HostStatusFailed, stage, errMsg)
		return outcome, steps
	}

	// 阶段 1：拨号
	target, auth, host, err := env.HostSvc.LoadAuth(dep.HostID)
	if err != nil {
		return fail(StageDial, "load auth: "+err.Error(), started, "", "", "")
	}
	dialStart := time.Now()
	client, err := sshpkg.Dial(target, auth, env.SSHOpts)
	if err != nil {
		return fail(StageDial, err.Error(), dialStart, host.Name, host.IP, "")
	}
	defer client.Close()
	if host.HostKey == "" && client.LearnedHostKey() != "" {
		env.HostSvc.RecordHostKey(host.ID, client.LearnedHostKey())
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageDial, true,
		fmt.Sprintf("connected %s@%s:%d", target.User, target.IP, target.Port), "", dialStart))

	dispatcher := deploy.NewDispatcher(client.SSHClient())
	sysctl := deploy.NewSystemctl(client)

	spec := deploy.AppSpec{
		AppCode:        app.AppCode,
		DeployPath:     app.DeployPath,
		JvmArgs:        app.JvmArgs,
		Port:           effectivePort(dep, app),
		HealthCheckURL: app.HealthCheckURL,
		EnvVars:        plan.EnvMap,
		User:           app.SystemdUser,
	}

	// 阶段 2：上传 jar
	upStart := time.Now()
	md5sum, err := dispatcher.Upload(art.FilePath, spec.JarPath())
	if err != nil {
		return fail(StageUpload, err.Error(), upStart, host.Name, host.IP, "")
	}
	if md5sum != art.FileMD5 {
		return fail(StageUpload,
			fmt.Sprintf("md5 mismatch: local=%s remote=%s", art.FileMD5, md5sum),
			upStart, host.Name, host.IP, "")
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageUpload, true,
		fmt.Sprintf("uploaded %s (%d bytes, md5=%s)", spec.JarPath(), art.FileSize, md5sum), "", upStart))

	// 阶段 3：写 unit 文件
	unitStart := time.Now()
	unitText, err := deploy.RenderUnit(spec)
	if err != nil {
		return fail(StageUnit, "render: "+err.Error(), unitStart, host.Name, host.IP, "")
	}
	if err := dispatcher.WriteFile(spec.UnitPath(), unitText, 0o644); err != nil {
		return fail(StageUnit, "write unit: "+err.Error(), unitStart, host.Name, host.IP, "")
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageUnit, true, spec.UnitPath(), "", unitStart))

	// 阶段 4：daemon-reload + restart
	rsStart := time.Now()
	if err := sysctl.DaemonReload(ctx); err != nil {
		return fail(StageRestart, "daemon-reload: "+err.Error(), rsStart, host.Name, host.IP, "")
	}
	if err := sysctl.EnableAndRestart(ctx, spec.UnitName()); err != nil {
		return fail(StageRestart, err.Error(), rsStart, host.Name, host.IP, "")
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageRestart, true, spec.UnitName(), "", rsStart))

	// 阶段 5：health probe
	hStart := time.Now()
	probeURL := deploy.BuildHealthURL(host.IP, spec.Port, app.HealthCheckURL)
	res := deploy.Probe(ctx, deploy.HealthOpts{
		URL:           probeURL,
		Timeout:       3 * time.Second,
		MaxAttempts:   30,
		Interval:      2 * time.Second,
		ExpectKeyword: "UP",
	})
	if !res.OK {
		detail := fmt.Sprintf("url=%s attempts=%d code=%d", probeURL, res.Attempts, res.LastCode)
		errStr := res.LastError
		if errStr == "" {
			errStr = "probe failed"
		}
		return fail(StageHealth, errStr, hStart, host.Name, host.IP, detail)
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageHealth, true,
		fmt.Sprintf("url=%s attempts=%d", probeURL, res.Attempts), "", hStart))

	// 成功收口
	ended := time.Now()
	outcome.Status = HostStatusSuccess
	outcome.CurrentStage = StageHealth
	outcome.EndedAt = &ended
	hooks.OnHostStatus(dep.ID, dep.HostID, HostStatusSuccess, StageHealth, "")
	hooks.OnDeploymentSuccess(dep, art.ID)
	return outcome, steps
}

// skipHost 标记一台尚未开始的主机为 skipped（fail-fast 或 cancel 时调用）。
func skipHost(dep *model.Deployment, reason string, hooks Hooks) HostOutcome {
	now := time.Now()
	hooks.OnHostStatus(dep.ID, dep.HostID, HostStatusSkipped, "", reason)
	return HostOutcome{
		HostID:       dep.HostID,
		DeploymentID: dep.ID,
		Status:       HostStatusSkipped,
		Err:          reason,
		StartedAt:    &now,
		EndedAt:      &now,
	}
}

func effectivePort(dep *model.Deployment, app *model.Application) int {
	if dep.Port > 0 {
		return dep.Port
	}
	return app.Port
}

func mkStepTimed(hostID uint, hostName, hostIP, stage string, ok bool, detail, errStr string, start time.Time) StepResult {
	return StepResult{
		HostID: hostID, HostName: hostName, HostIP: hostIP, Stage: stage,
		OK: ok, Detail: detail, Error: errStr,
		StartedAt: start.Format(time.RFC3339),
		EndedAt:   time.Now().Format(time.RFC3339),
	}
}
