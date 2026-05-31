# swift-devops · Design Notes

## 设计目标

- 以单二进制交付 Java 服务运维平台：前端静态产物 embed 进 Go binary，降低安装和升级复杂度。
- 覆盖从 Git 构建、制品入库、多主机部署、蓝绿/滚动发布到回滚的闭环。
- 目标主机零 Agent：通过 SSH、systemd/nohup/Docker runtime 完成分发和生命周期管理。
- 保持单机优先：SQLite + filesystem 足够支撑轻量团队，后续保留替换 Repository / PostgreSQL 的空间。

## 非目标

- 不替代 Kubernetes / ArgoCD 这类集群级编排系统。
- 不做多租户 SaaS 权限体系；当前以单实例管理员场景为主。
- 不把构建机、制品仓库、镜像仓库全部内置成重型平台能力。

## 架构概览

```text
React SPA (web/, embed.FS)
        │ HTTP / WebSocket
        ▼
Gin API + middleware(jwt / audit / ratelimit)
        ▼
Service layer(host / app / build / artifact / pipeline)
        ▼
GORM(SQLite) + filesystem + SSH/builder/deploy packages
```

主要边界：

- `cmd/swift-devops/`：CLI 入口与服务启动。
- `internal/api/`：HTTP/WS 接口、鉴权、审计和限流。
- `internal/service/`：业务编排、事务边界、构建/部署状态机。
- `internal/pkg/`：可复用基础能力，包括 crypto、ssh、builder、deploy、ws、errors。
- `web/`：React + TypeScript 前端，构建后由 Go embed 提供。

## 关键设计决策

| 决策 | 选择 | 理由 | 权衡 |
|---|---|---|---|
| 交付形态 | Go 单二进制 + embed 前端 | 安装简单，适合内网运维工具 | 前端资源需随后端一起重建 |
| 数据库 | SQLite (`glebarez/sqlite`) | 纯 Go、无需 CGO、部署轻 | 横向扩展和复杂查询能力有限 |
| 主机纳管 | SSH + TOFU host key | 零 Agent，兼容传统 VM | SSH 凭证和 host key 管理必须谨慎 |
| 凭证保护 | AES-256-GCM + `master_key` | 可加密保存 SSH / Git secret | `master_key` 丢失后历史密文不可恢复 |
| 构建模式 | 本机 Maven 或 Docker 隔离构建 | 本机模式轻，Docker 模式隔离环境 | Docker 模式依赖宿主机 daemon 权限 |
| 部署 runtime | systemd / nohup / Docker 抽象 | 适配 root 与非 root、进程与容器场景 | 各 runtime 行为差异需测试覆盖 |
| 实时反馈 | WebSocket + 一次性 ticket | 浏览器 WS 无法带 Authorization header，用短票降低泄露窗口 | ticket 生命周期和主题绑定需要严格校验 |
| 发布策略 | single / rolling / blue-green | 覆盖常见 Java 服务发布方式 | 蓝绿依赖 Nginx upstream 配置约定 |

## 安全考量

- HTTP API 使用 JWT；登录接口有 IP + 用户名维度限流。
- WebSocket 使用 5 秒 TTL、主题绑定、用后即焚的一次性 ticket。
- 写操作经 audit middleware 记录，payload 中 password / secret / token / private key 字段会脱敏。
- SSH 凭证、Git 凭证等敏感字段使用 `master_key` 加密；生产环境必须备份 `master_key`。
- `data/`、`logs/`、构建产物和本地数据库均不应进入 Git；构建 workspace 可能包含上游项目敏感配置。

## 已知限制

- SQLite 适合单实例部署，不适合多副本并发写入。
- `master_key` 暂无轮换机制；丢失后只能重录敏感凭证。
- Docker 构建/部署依赖宿主机或目标机 Docker 权限，需由部署环境保证。
- 前端生产 bundle 当前较大，后续可通过 route-level dynamic import 或 manualChunks 优化。
- 监控告警仍是后续模块，当前主线侧重构建和发布闭环。

## 变更历史

- Sprint 1~5：主机、应用、制品、构建和部署主线落地。
- Sprint X：从单 jar 应用扩展为多 AppService / ArtifactBundle 架构。
- Sprint X.10：引入 runtime 抽象，支持 systemd 与 nohup。
- Sprint X.11：扩展 Docker 构建与 Docker runtime 相关字段和执行路径。
