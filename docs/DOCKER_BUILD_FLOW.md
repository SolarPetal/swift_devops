# Docker 构建流程梳理

## 一、触发入口

用户在前端点「从仓库构建」→ `POST /api/v1/apps/:id/build`

## 二、完整流程

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. BuildService.TriggerBuild (service/build_service.go:150)    │
│    - 校验 app / cred / 互斥锁                                    │
│    - 落库 BuildRun (status=building)                            │
│    - 准备 log 文件路径（workspace/<app_code>-<build_id>.log）   │
│    - 启动 goroutine: execute()                                  │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 2. BuildService.execute (service/build_service.go:248)         │
│    - 打开 log 文件，双写（文件 + WebSocket）                     │
│    - 【关键分支】根据 config.builder.docker_enabled 决定：       │
│                                                                 │
│    ┌─ docker_enabled = true ─────────────────────────────┐    │
│    │  - envSvc.ResolveDockerInputs()                      │    │
│    │    → 返回 GitBin / DockerImage / MavenCacheDir       │    │
│    │  - 写日志：[docker] image=xxx git=xxx maven_cache=xxx│    │
│    └──────────────────────────────────────────────────────┘    │
│                                                                 │
│    ┌─ docker_enabled = false ────────────────────────────┐    │
│    │  - envSvc.BuildExecEnv() → 返回 JAVA_HOME/PATH 环境  │    │
│    │  - envSvc.ResolveBuildInputs()                       │    │
│    │    → 返回 MvnBin / GitBin / MavenLocalRepo           │    │
│    │  - 写日志：[bins] mvn=xxx git=xxx maven_local_repo=xxx│   │
│    └──────────────────────────────────────────────────────┘    │
│                                                                 │
│    - 组装 builder.Plan（包含 DockerImage / MvnBin / GitBin）   │
│    - 调用 builder.Build(ctx, plan)                             │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 3. builder.Build (pkg/builder/build.go:65)                     │
│    - 创建工作区子目录：<workspace>/<app_code>-<build_id>        │
│    - git clone（宿主机执行，用 plan.GitBin）                    │
│    - 【关键】调用 runMvn(ctx, plan, subDir, mvnArgs)           │
│    - 按 JarPattern 提取 jar（宿主机文件系统）                   │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 4. builder.runMvn (pkg/builder/build.go:160)                   │
│    【路由逻辑】                                                  │
│                                                                 │
│    if plan.DockerImage != "" {                                 │
│        ┌─ Docker 模式 ─────────────────────────────────┐      │
│        │  MvnPackageDocker(ctx, DockerMavenOptions{    │      │
│        │    WorkDir:       workDir,  // 宿主机源码目录  │      │
│        │    Image:         plan.DockerImage,           │      │
│        │    MavenCacheDir: plan.MavenCacheDir,         │      │
│        │    LogWriter:     plan.LogWriter,             │      │
│        │  })                                            │      │
│        └────────────────────────────────────────────────┘      │
│    } else {                                                    │
│        ┌─ 本机模式 ─────────────────────────────────────┐     │
│        │  MvnPackage(ctx, MavenOptions{                 │     │
│        │    WorkDir:       workDir,                     │     │
│        │    MvnBin:        plan.MvnBin,  // 宿主机 mvn   │     │
│        │    MavenCacheDir: plan.MavenCacheDir,          │     │
│        │    ExecEnv:       plan.ExecEnv, // JAVA_HOME等 │     │
│        │  })                                             │     │
│        └─────────────────────────────────────────────────┘     │
│    }                                                           │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 5. Docker 模式细节 (pkg/builder/maven_docker.go:40)            │
│                                                                 │
│    docker run --rm \                                           │
│      --user <uid>:<gid> \          # 避免容器 root 写文件权限问题│
│      -e HOME=/tmp \                                            │
│      -v <WorkDir>:/src \           # 源码 bind mount           │
│      -v <MavenCacheDir>:/m2 \      # maven 缓存 bind mount     │
│      -w /src \                                                 │
│      <Image> \                                                 │
│      mvn -B -ntp -Dmaven.repo.local=/m2 clean package -DskipTests│
│                                                                 │
│    【关键设计】                                                  │
│    - git clone 在宿主机完成（凭证不进容器）                      │
│    - 只有 mvn 阶段在容器内跑（隔离 JDK/Maven 版本）              │
│    - 源码通过 bind mount 进容器（/src）                         │
│    - jar 产物落在 /src/target → 宿主机 WorkDir/target           │
│    - maven 缓存挂载到 /m2（复用依赖，避免每次重下）              │
│    - --user 用宿主机 uid:gid 跑，避免容器 root 写出的文件宿主机删不掉│
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 6. 产物回收 (builder.Build 继续)                               │
│    - findArtifactJar(subDir, jarPattern)                       │
│      → 在宿主机文件系统扫描 WorkDir/target/*.jar                │
│    - 返回 Result{JarPath, CommitSHA, BuildSubDir}              │
└─────────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│ 7. 制品入库 (BuildService.execute 继续)                        │
│    - artSvc.IngestBundle(appID, versionTag, commitSHA, ...)   │
│      → 拷贝 jar 到 artifact_dir/<app_code>/<bundle_id>/        │
│    - 更新 BuildRun (status=success, bundle_id=xxx)             │
│    - 清理历史 Bundle（保留最近 max_history 个）                 │
└─────────────────────────────────────────────────────────────────┘
```

## 三、配置开关

### 3.1 配置文件 `config.yaml`

```yaml
builder:
  docker_enabled: false  # true = Docker 隔离构建
```

### 3.2 UI 配置「构建环境」

**docker_enabled = false（本机模式）**
- 必填：`JAVA_HOME` / `MAVEN_HOME` / `Git 路径`
- 可选：`Maven 本地仓库`
- 检测：验证 `java -version` / `mvn -v` / `git --version`

**docker_enabled = true（Docker 模式）**
- 必填：`Git 路径`（clone 仍在宿主机）
- 必填：`Docker 构建镜像`（如 `maven:3.9-eclipse-temurin-17`）
- 可选：`Maven 本地仓库`（挂载到容器 `/m2`）
- 检测：验证 `docker ps` / `docker image inspect <image>` / `git --version`
- **不需要** `JAVA_HOME` / `MAVEN_HOME`（容器自带）

## 四、关键设计决策

### 4.1 为什么 git clone 不进容器？

✅ **安全**：Git 凭证（SSH 私钥 / HTTP 密码）不需要挂载进容器  
✅ **简单**：不需要在容器内配置 SSH agent / known_hosts  
✅ **复用**：宿主机 git 配置（credential helper / proxy）直接生效

### 4.2 为什么用 `--user <uid>:<gid>`？

❌ **不用的话**：容器内默认 root 跑 mvn → jar 文件 owner 是 root → 宿主机普通用户删不掉 → 构建工作区清理失败  
✅ **用了之后**：容器内用宿主机 uid:gid 跑 → jar 文件 owner 是宿主机用户 → 清理无障碍

### 4.3 为什么 maven 缓存挂载到 `/m2` 而不是 `/root/.m2`？

❌ **挂 `/root/.m2`**：容器内 `--user <uid>:<gid>` 跑时 HOME 不是 `/root`，mvn 读不到  
✅ **挂 `/m2` + `-Dmaven.repo.local=/m2`**：显式指定仓库路径，跟 HOME 无关

### 4.4 jar 提取为什么在宿主机做？

✅ **统一**：Docker / 本机模式用同一套 `findArtifactJar` 逻辑（Spring Boot 探测 / glob 匹配）  
✅ **简单**：不需要在容器内装额外工具（jq / find）  
✅ **可见**：jar 直接落在宿主机 `WorkDir/target`，bind mount 自动同步

## 五、验证点

### 5.1 本机模式（docker_enabled=false）

- [ ] 构建环境检测通过（java / mvn / git 版本都显示）
- [ ] 触发构建 → 日志显示 `[bins] mvn=xxx git=xxx maven_local_repo=xxx`
- [ ] 构建成功 → jar 入库 → 部署可用

### 5.2 Docker 模式（docker_enabled=true）

- [ ] 构建环境检测通过（docker / git 版本显示，java/mvn 版本为空）
- [ ] 触发构建 → 日志显示 `[docker] image=xxx git=xxx maven_cache=xxx`
- [ ] 日志显示 `$ docker run --rm --user <uid>:<gid> ...`
- [ ] 构建成功 → jar 入库 → 部署可用
- [ ] 构建工作区清理成功（无权限问题）

### 5.3 切换模式

- [ ] 改配置 `docker_enabled: true` → 重启后端 → UI 检测通过
- [ ] 改配置 `docker_enabled: false` → 重启后端 → UI 检测通过
- [ ] 两种模式构建的 jar 都能正常部署

## 六、已知约束

⚠ **Docker 模式需要宿主机 docker 可达**  
   - `docker ps` 能跑通（权限 / socket 路径都要对）
   - 镜像需提前拉取或能从 Docker Hub 拉取

⚠ **docker_enabled=false 时 docker_image 字段无效**  
   - UI 可以填，但不会用
   - 检测时不会验证 docker

⚠ **Maven 缓存目录需要宿主机可写**  
   - Docker 模式：容器内用宿主机 uid:gid 写 `/m2`
   - 本机模式：mvn 直接写宿主机路径

⚠ **构建超时默认 30 分钟**  
   - 首次构建（无缓存）可能较慢
   - 可在代码里调整 `DockerMavenOptions.Timeout`
