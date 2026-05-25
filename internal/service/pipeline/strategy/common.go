package strategy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"swift-devops/internal/model"
	"swift-devops/internal/pkg/deploy"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// deployHost 在单台主机上跑完 5 个部署阶段（dial → upload → write_unit → restart → health）。
// 任一阶段失败立即返回，hooks.OnStep 已经把当前失败步骤喷出去。
// 成功完成则 hooks.OnDeploymentSuccess 更新 deployment 表。
//
// art 参数：策略层传入 —— forward 模式（single/rolling/blue_green）传 plan.Artifact；
// rollback 模式按 dep 查 previous_artifact_id 对应的 art。
//
// 返回值：HostOutcome（含 status / stage / err）+ 该主机的所有 step 序列。
// ctx 应用于：拨号超时 + systemctl 命令超时 + 健康探针超时（probe 内部尊重 ctx）。
//
// 注意：本函数不会因 ctx 取消而提前 return ——已开始的部署应跑完当前 SSH 会话，
// 由策略层在调用前判断 ctx.Err() != nil 决定是否进入这台主机。
func deployHost(ctx context.Context, env Env, plan *Plan, dep *model.Deployment, art *model.Artifact, hooks Hooks) (HostOutcome, []StepResult) {
	app := plan.App

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

	// 计算 effective java_path：app 覆盖 host，最后兜底 /usr/bin/java
	effectiveJava := strings.TrimSpace(app.JavaPath)
	if effectiveJava == "" {
		effectiveJava = strings.TrimSpace(host.JavaPath)
	}
	if effectiveJava == "" {
		effectiveJava = "/usr/bin/java"
	}

	spec := deploy.AppSpec{
		AppCode:        app.AppCode,
		DeployPath:     app.DeployPath,
		JvmArgs:        app.JvmArgs,
		Port:           effectivePort(dep, app),
		HealthCheckURL: app.HealthCheckURL,
		EnvVars:        plan.EnvMap,
		User:           app.SystemdUser,
		JavaPath:       effectiveJava,
	}

	// 阶段 1.5：env_check —— 部署前预检远端 Java 可用性（Sprint 3.7）
	// 自动 resolve：用户填的 java_path 可能是 JDK 目录（/usr/local/jdk-21）也可能是
	// java 可执行文件（/usr/bin/java）。远端 shell 探测：
	//   - <path>/bin/java 可执行 → 用它（path 是 JDK 目录）
	//   - 否则 <path> 本身可执行 → 用它（path 是 java 文件）
	//   - 都不行 → 报错
	// resolved 结果回写 spec.JavaPath，保证 unit 文件 ExecStart 不再踩 203/EXEC
	ecStart := time.Now()
	q := deploy.ShellQuote(effectiveJava)
	resolveScript := fmt.Sprintf(`
if [ -x %s/bin/java ]; then RESOLVED=%s/bin/java
elif [ -x %s ]; then RESOLVED=%s
else echo "java not found at %s (also tried %s/bin/java)"; exit 1; fi
echo "RESOLVED=$RESOLVED"
"$RESOLVED" -version 2>&1 | head -1
`, q, q, q, q, effectiveJava, effectiveJava)
	envRes, err := client.Exec(ctx, resolveScript)
	if err != nil {
		return fail(StageEnvCheck, "exec env_check: "+err.Error(), ecStart, host.Name, host.IP, "")
	}
	if envRes.ExitCode != 0 {
		stderr := strings.TrimSpace(envRes.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(envRes.Stdout)
		}
		return fail(StageEnvCheck,
			fmt.Sprintf("远端 java 不可执行：%s（exit=%d）。请在「主机管理」修改主机的 java_path，或在「应用配置」覆盖；如未装 JDK，请先安装。\n\n%s",
				effectiveJava, envRes.ExitCode, stderr),
			ecStart, host.Name, host.IP, "")
	}
	// 解析 RESOLVED=xxx 行 + 取版本
	var resolvedJava, javaVersion string
	for _, line := range strings.Split(strings.TrimSpace(envRes.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "RESOLVED=") {
			resolvedJava = strings.TrimPrefix(line, "RESOLVED=")
		} else if line != "" && javaVersion == "" {
			javaVersion = line
		}
	}
	if resolvedJava == "" {
		resolvedJava = effectiveJava // 兜底
	}
	spec.JavaPath = resolvedJava // 写回，让 RenderUnit 拿到正确路径
	detail := fmt.Sprintf("%s → %s", resolvedJava, javaVersion)
	if resolvedJava != effectiveJava {
		detail = fmt.Sprintf("%s (resolved from %s) → %s", resolvedJava, effectiveJava, javaVersion)
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageEnvCheck, true, detail, "", ecStart))

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

	// 阶段 4：daemon-reload + restart + WaitActive（Sprint 3.6）
	rsStart := time.Now()
	if err := sysctl.DaemonReload(ctx); err != nil {
		return fail(StageRestart, "daemon-reload: "+err.Error(), rsStart, host.Name, host.IP, "")
	}
	if err := sysctl.EnableAndRestart(ctx, spec.UnitName()); err != nil {
		return fail(StageRestart, err.Error(), rsStart, host.Name, host.IP, "")
	}
	// systemctl restart 是异步的，主动等 10s 内进入 active —— 否则立刻拉 status + journal
	st, waited, waitErr := sysctl.WaitActive(ctx, spec.UnitName(), 10*time.Second)
	if waitErr != nil {
		dump := sysctl.StatusDump(ctx, spec.UnitName(), 200)
		hint := humanizeSystemdError(dump)
		errStr := fmt.Sprintf("%s%s\n\n--- diagnostics ---\n%s", hint, waitErr.Error(), dump)
		return fail(StageRestart, errStr, rsStart, host.Name, host.IP,
			fmt.Sprintf("is-active=%s waited=%s", st, waited.Round(time.Millisecond)))
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageRestart, true,
		fmt.Sprintf("%s active in %s", spec.UnitName(), waited.Round(time.Millisecond)), "", rsStart))

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
		// Sprint 3.6：health 失败时附 status + journal，便于看到 Java 异常 / OOM / 端口冲突
		dump := sysctl.StatusDump(ctx, spec.UnitName(), 200)
		if dump != "" {
			hint := humanizeSystemdError(dump)
			errStr = hint + errStr + "\n\n--- diagnostics ---\n" + dump
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

// humanizeSystemdError 从 systemctl status / journalctl 输出里识别常见错误码，
// 返回友好的中文 hint（"⚠ Java 可执行文件不存在/权限不足..."），便于用户立刻定位。
// 没命中任何已知模式时返回空串，调用方应直接展示原始 dump。
//
// Sprint 3.7：先支持 5 个最常见的 systemd 错误模式，覆盖 80% 部署失败场景。
func humanizeSystemdError(dump string) string {
	if dump == "" {
		return ""
	}
	lower := strings.ToLower(dump)
	switch {
	case strings.Contains(dump, "status=203/EXEC"):
		return "⚠ Java 可执行文件不存在或权限不足（systemd 203/EXEC）。请检查主机配置的 java_path，或确认远端已装 JDK。\n\n"
	case strings.Contains(dump, "status=200/CHDIR"):
		return "⚠ WorkingDirectory 不存在或无权访问（systemd 200/CHDIR）。请确认应用 deploy_path 在远端可写。\n\n"
	case strings.Contains(dump, "status=200/USER"):
		return "⚠ systemd User= 在远端不存在（200/USER）。请去掉应用的 systemd_user，或在远端创建该用户。\n\n"
	case strings.Contains(dump, "status=200/EXEC"):
		return "⚠ 进程启动时 exec 失败（200/EXEC）。常见原因：jar 文件损坏 / class not found。检查 jar 完整性与 JVM 参数。\n\n"
	case strings.Contains(dump, "status=143"):
		return "ℹ 进程收到 SIGTERM 退出（143）。一般是正常停止流程；若不是预期，检查 RestartSec 与外部信号源。\n\n"
	case strings.Contains(lower, "killed") && strings.Contains(lower, "signal=kill"),
		strings.Contains(dump, "status=137"),
		strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "oom-killer"):
		return "⚠ 进程被 OOM Killer 杀死（status=137 或 signal=KILL）。建议调大 -Xmx 或扩容主机内存。\n\n"
	case strings.Contains(lower, "address already in use"),
		strings.Contains(lower, "bindexception"),
		strings.Contains(dump, "Port already in use"):
		return "⚠ 端口已被占用。常见原因：上一进程未释放，或同主机已有别的应用占用此端口。检查 server.port 配置。\n\n"
	}
	return ""
}

func mkStepTimed(hostID uint, hostName, hostIP, stage string, ok bool, detail, errStr string, start time.Time) StepResult {
	return StepResult{
		HostID: hostID, HostName: hostName, HostIP: hostIP, Stage: stage,
		OK: ok, Detail: detail, Error: errStr,
		StartedAt: start.Format(time.RFC3339),
		EndedAt:   time.Now().Format(time.RFC3339),
	}
}
