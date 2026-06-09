# swift-devops · 架构说明

> 单二进制 + 嵌入前端 + systemd/nohup 托管的 Java 服务自动化运维平台。

## 1. 分层架构

```
┌────────────────────────────────────────────┐
│  React SPA  (web/, embed.FS)               │
└────────────────────────────────────────────┘
                  ↑ HTTP / WebSocket
┌────────────────────────────────────────────┐
│  API Layer    internal/api/handler         │
│  + middleware jwt / audit / ratelimit / logger
└────────────────────────────────────────────┘
                  ↓
┌────────────────────────────────────────────┐
│  Service Layer  internal/service           │
│  host / app / artifact / build / pipeline …│
└────────────────────────────────────────────┘
                  ↓
┌──────────────┬──────────────┬──────────────┐
│ Repository   │  External    │  Toolkit     │
│ store/       │  pkg/ssh/    │  pkg/crypto/ │
│ (GORM)       │  pkg/builder/│  pkg/ws/     │
│              │  pkg/deploy/ │  pkg/errors/ │
└──────────────┴──────────────┴──────────────┘
                  ↓
┌────────────────────────────────────────────┐
│  Storage  SQLite (WAL)  |  Filesystem      │
│           swift.db      |  data/artifacts  │
└────────────────────────────────────────────┘
```

## 2. 包职责

| 包路径 | 职责 |
|---|---|
| `cmd/swift-devops/` | CLI 入口（`main` 启动 + `cleanup-apps` 子命令） |
| `internal/config/` | YAML 加载、默认值、校验 |
| `internal/store/` | DB 连接 + AutoMigrate |
| `internal/model/` | GORM 实体（13 表，见 §5） |
| `internal/api/router.go` | 路由、SPA fallback |
| `internal/api/handler/` | HTTP handler + WS handler（pipeline / build 日志流） |
| `internal/api/middleware/` | JWT / Logger / Audit / Ratelimit |
| `internal/service/` | 业务编排（事务边界、状态机、实时事件发布） |
| `internal/service/pipeline/strategy/` | 部署策略（single / rolling / blue_green）+ Wave 调度 |
| `internal/pkg/crypto/` | bcrypt、AES-GCM |
| `internal/pkg/ssh/` | SSH 客户端 + 连接池 + TOFU host key |
| `internal/pkg/builder/` | 本机构建：git clone + mvn package（环境由 `BuilderEnv` 注入） |
| `internal/pkg/deploy/` | Runtime 抽象（systemd / nohup）+ 滚动、蓝绿、回滚、Nginx 联动 |
| `internal/pkg/ws/` | WebSocket 框架（Hub 广播 + 一次性 Ticket + 心跳 Conn） |
| `internal/pkg/errors/` | 错误码 + Gin 响应壳 |
| `internal/pkg/monitor/` | 采样、阈值、Webhook —— **Sprint 6 规划，尚未实现** |
| `web/` | 前端 Vite + React，含 embed 子包 |

## 3. 横切关注点

- **认证 / 鉴权**：HTTP 走 JWT 中间件；WebSocket 升级走一次性 Ticket（query 传递，5s TTL + 主题绑定 + 用后即焚）
- **审计**：写操作经 `middleware/audit.go` 自动落 `audit_logs`
- **限流**：`middleware/ratelimit.go`，登录接口按 IP + 用户名双维度计数
- **加密**：`master_key`（base64 32B）→ AES-256-GCM；用于 `Host.Secret` 等敏感字段
- **错误响应**：`{"error","code","trace_id"}` 统一格式
- **日志**：标准库 `slog`，访问日志 + 业务事件分级

## 4. 关键技术决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 数据库 | SQLite (`glebarez/sqlite`,纯 Go) | 单机部署优先；后续 Repository 抽象后可换 PG |
| 嵌入前端 | `web/embed.go` 独立包 | Go embed 路径相对包目录解析、不允许 `..`，独立包是唯一干净方案 |
| SSH 客户端 | `golang.org/x/crypto/ssh` | 原生 Go，不依赖系统 ssh，便于在容器内运行 |
| Java 进程托管 | Runtime 抽象：systemd 单元（默认）/ nohup（可选，Sprint X.10） | systemd 自带优雅停机（SIGTERM→SIGKILL 90s）；nohup 免 root，适配受限环境 |
| 构建方式 | 本机 git + mvn，环境经 `BuilderEnv` 注入（Sprint 5.4） | 免装 Docker；UI 配 JAVA_HOME/MAVEN_HOME + 检测。Docker 隔离构建列 Sprint 5.6 规划 |
| 多 service 一致性 | 一次构建 = `ArtifactBundle` + N×`ArtifactItem`，`git_commit_sha` 锚定（Sprint X.2） | 微服务整组同源；回滚以 Bundle 为粒度 |
| 实时推送 | WebSocket Hub 广播 + 一次性 Ticket（pipeline 步骤 / build 日志） | 浏览器原生 WS 不带 header，5s 一次性票替代 JWT 进 query |
| 蓝绿切流 | Nginx upstream 重写 + `nginx -s reload` | 毫秒级无损切换，无需 K8s |
| 回滚保护 | 目标版本预检必须早于停止当前实例 | previous jar / image 缺失时应失败在预检或上传阶段，而不是先中断线上服务 |

## 5. 数据模型概览

13 张表，按四层组织（随 Sprint X 重构：单 jar → 业务系统 × 微服务）：

```
应用层    Application ──< AppService           一个业务系统挂 N 个微服务
              │              │                 (per-runtime 字段下沉到 AppService)
制品层    ArtifactBundle ──< ArtifactItem       一次构建一组 jar，git_commit_sha 锚定一致性
              ┊                                 (旧单 jar：Artifact，Sprint X.4 后退役)
部署层    Deployment >── Host                   应用 × 主机 × service，蓝绿分组
              │
流水线    PipelineRun ──< PipelineRunHost        一次发布，host×service 级状态聚合
          BuildRun                              一次构建任务（与 PipelineRun 平级）

配置 / 凭证    GitCredential · BuilderEnv(单例) · AuditLog(独立轴)
```

详见 `internal/model/types.go`。回滚以 Bundle 为粒度；`PipelineRun.previous_bundle_id` 与 `Deployment.*_artifact_item_id` 构成回滚链（制品清理时受保护）。

## 6. 部署 / 回滚执行约束

回滚链路遵循一个跨后端与前端的安全不变量：**目标版本可用性确认必须发生在停止当前服务之前**。

- 后端 jar runtime（systemd/nohup）：先确认 previous artifact 的本地文件存在且可上传；制品缺失时停在 `upload` 阶段，不进入 `restart`。
- 后端 Docker runtime：`local-docker` 先 `docker pull`，`remote-docker` 先 `docker image inspect`；成功后才允许 `docker stop` / `docker rm` 旧容器。
- 前端回滚：按 `(app_id, host_id, service_code, domain)` 读取 `FrontendDeploymentState.previous_image`，其中 `domain=""` 表示无域名测试部署；在目标主机通过 `docker image inspect` 记录 `docker_image_check`，成功后才重建容器。有域名才更新 Gateway route，无域名则跳过 Gateway。
- 成功回滚后，账本的 current / previous 指针互换，保证下一次回滚仍能找到反向目标。
