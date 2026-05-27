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

	// Sprint X.3：根据 plan.Service 决定 service-aware 字段
	// - 有 Service：service-level 配置覆盖 app-level，jar/unit 加 service_code 维度
	// - 无 Service（旧链路）：完全用 app 字段，保持 Sprint X.3 之前的行为
	// Sprint X.10：增加 deploy_mode 维度（systemd / nohup）
	svcCode, svcPort, svcHealth, svcJvm, svcEnvMap, svcSystemdUser, svcJavaPath, svcDeployMode := resolveServiceConfig(plan, app, dep)

	// Sprint X.10：按 deploy_mode 选 runtime。systemd / nohup 共用 dial/env_check/upload/health 阶段，
	// 只在 write_unit / restart / status_dump 这三处分派。
	runtime := deploy.PickRuntime(svcDeployMode, client)

	// 计算 effective java_path：service 覆盖 app，再覆盖 host，最后兜底 /usr/bin/java
	effectiveJava := strings.TrimSpace(svcJavaPath)
	if effectiveJava == "" {
		effectiveJava = strings.TrimSpace(host.JavaPath)
	}
	if effectiveJava == "" {
		effectiveJava = "/usr/bin/java"
	}

	spec := deploy.AppSpec{
		AppCode:        unitNameKey(app.AppCode, svcCode), // unit 名前缀
		DeployPath:     serviceDeployPath(app.DeployPath, svcCode),
		JvmArgs:        svcJvm,
		Port:           svcPort,
		HealthCheckURL: svcHealth,
		EnvVars:        svcEnvMap,
		User:           svcSystemdUser,
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

	// 阶段 3：写启动文件（systemd → unit 文件；nohup → start.sh + stop.sh）
	// Sprint X.10：先 best-effort 清理另一种模式的残留，避免切模式时新旧并存
	unitStart := time.Now()
	_ = runtime.StopOther(ctx, spec)
	if err := runtime.PrepareUnit(ctx, spec); err != nil {
		return fail(StageUnit, fmt.Sprintf("[%s] %s", runtime.Name(), err.Error()),
			unitStart, host.Name, host.IP, "")
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageUnit, true,
		fmt.Sprintf("%s (mode=%s)", runtime.UnitArtifactPath(spec), runtime.Name()), "", unitStart))

	// 阶段 4：重启 + 等 active
	// systemd：daemon-reload + enable + restart + WaitActive
	// nohup：stop.sh → start.sh → 轮询 pid 文件 + kill -0
	rsStart := time.Now()
	waited, waitErr := runtime.RestartAndWait(ctx, spec, 10*time.Second)
	if waitErr != nil {
		dump := runtime.StatusDump(ctx, spec, 200)
		hint := runtime.HumanizeError(dump)
		errStr := fmt.Sprintf("%s[%s] %s\n\n--- diagnostics ---\n%s",
			hint, runtime.Name(), waitErr.Error(), dump)
		return fail(StageRestart, errStr, rsStart, host.Name, host.IP,
			fmt.Sprintf("mode=%s waited=%s", runtime.Name(), waited.Round(time.Millisecond)))
	}
	emit(mkStepTimed(host.ID, host.Name, host.IP, StageRestart, true,
		fmt.Sprintf("%s started in %s (mode=%s)",
			spec.UnitName(), waited.Round(time.Millisecond), runtime.Name()), "", rsStart))

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
		// Sprint 3.6 / X.10：health 失败时附 runtime 诊断，便于看到 Java 异常 / OOM / 端口冲突
		dump := runtime.StatusDump(ctx, spec, 200)
		if dump != "" {
			hint := runtime.HumanizeError(dump)
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
	// Sprint X.6/X.7：多 service 链路时回填 current_artifact_item_id
	//   - forward：plan.Item 指向本 service 在 Bundle 内的 item
	//   - rollback：plan.ItemByDepID[dep.ID] 指向本 dep 的 previous item
	var itemID uint
	if plan.Item != nil {
		itemID = plan.Item.ID
	} else if it, ok := plan.ItemByDepID[dep.ID]; ok && it != nil {
		itemID = it.ID
	}
	hooks.OnDeploymentSuccess(dep, art.ID, itemID)
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

// resolveServiceConfig 解析当前 (host × service) 部署所需的 per-runtime 配置。
//
// Sprint X.3 兼容矩阵：
//   - plan.Service 非 nil → 从 AppService 取（多 service 链路）
//   - plan.Service 为 nil → 从 Application 取（X.3 之前的单 jar 兼容）
//
// Sprint X.10：新增 deploy_mode 返回值。规则：
//   - service.DeployMode 非空 → 用 service
//   - 落空 → app.DeployMode
//   - 都空 → "systemd"（向后兼容）
//
// 返回值：service_code, port, health_url, jvm_args, env_map, systemd_user, java_path, deploy_mode
func resolveServiceConfig(plan *Plan, app *model.Application, dep *model.Deployment) (
	svcCode string, port int, health, jvm string, envMap map[string]string, systemdUser, javaPath, deployMode string,
) {
	if plan.Service != nil {
		s := plan.Service
		svcCode = strings.TrimSpace(s.ServiceCode)
		port = s.Port
		if dep.Port > 0 {
			port = dep.Port // dep 级覆盖仍生效（蓝绿/特殊 host 改端口场景）
		}
		health = s.HealthCheckURL
		jvm = s.JvmArgs
		systemdUser = s.SystemdUser
		javaPath = s.JavaPath
		// deploy_mode：service 优先，落空回 app，再落空 → 由 deploy.PickRuntime 兜底
		deployMode = strings.TrimSpace(s.DeployMode)
		if deployMode == "" {
			deployMode = strings.TrimSpace(app.DeployMode)
		}
		// env_vars 优先 service-level，落空回 plan.EnvMap（兼容老调用方）
		if parsed, err := deploy.ParseEnvVarsJSON(s.EnvVars); err == nil && len(parsed) > 0 {
			envMap = parsed
		} else {
			envMap = plan.EnvMap
		}
		return
	}
	// 旧链路：完全从 app 取
	svcCode = ""
	port = effectivePort(dep, app)
	health = app.HealthCheckURL
	jvm = app.JvmArgs
	systemdUser = app.SystemdUser
	javaPath = app.JavaPath
	deployMode = strings.TrimSpace(app.DeployMode)
	envMap = plan.EnvMap
	return
}

// unitNameKey 决定 systemd unit 命名的 app_code 部分。
//   - svcCode 空 / "default" → 沿用旧名 "devops-<app>.service"
//   - 其他 → "devops-<app>-<svc>.service"，避免同 app 多 service 的 unit 冲突
func unitNameKey(appCode, svcCode string) string {
	c := strings.TrimSpace(svcCode)
	if c == "" || c == "default" {
		return appCode
	}
	return appCode + "-" + c
}

// serviceDeployPath 决定 jar 在远端的部署根目录。
//   - svcCode 空 / "default" → 直接用 app.DeployPath（旧布局，兼容单 jar 应用）
//   - 其他 → "<deploy_path>/<svc>/"（多 service 独立子目录，日志/配置自然隔离）
func serviceDeployPath(deployPath, svcCode string) string {
	c := strings.TrimSpace(svcCode)
	if c == "" || c == "default" {
		return deployPath
	}
	return strings.TrimRight(deployPath, "/") + "/" + c
}

// Sprint X.10：humanizeSystemdError 已搬到 internal/pkg/deploy/systemd_runtime.go
// （作为 systemdRuntime.HumanizeError 方法）。deployHost 通过 Runtime 接口调用，本包不再持有。

func mkStepTimed(hostID uint, hostName, hostIP, stage string, ok bool, detail, errStr string, start time.Time) StepResult {
	return StepResult{
		HostID: hostID, HostName: hostName, HostIP: hostIP, Stage: stage,
		OK: ok, Detail: detail, Error: errStr,
		StartedAt: start.Format(time.RFC3339),
		EndedAt:   time.Now().Format(time.RFC3339),
	}
}
