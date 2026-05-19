# swift-devops · 架构说明

> 单二进制 + 嵌入前端 + systemd 托管的 Java 服务自动化运维平台。

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
│  host / app / artifact / pipeline / monitor│
└────────────────────────────────────────────┘
                  ↓
┌──────────────┬──────────────┬──────────────┐
│ Repository   │  External    │  Toolkit     │
│ store/       │  pkg/ssh/    │  pkg/crypto/ │
│ (GORM)       │  pkg/builder/│  pkg/ws/     │
│              │  pkg/git/    │  pkg/errors/ │
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
| `cmd/swift-devops/` | CLI 入口；命令路由 |
| `internal/config/` | YAML 加载、默认值、校验 |
| `internal/store/` | DB 连接 + AutoMigrate |
| `internal/model/` | GORM 实体（6 表） |
| `internal/api/router.go` | 路由、SPA fallback |
| `internal/api/handler/` | HTTP handler |
| `internal/api/middleware/` | JWT / Logger / Audit / Ratelimit |
| `internal/service/` | 业务编排（事务边界、状态机） |
| `internal/pkg/crypto/` | bcrypt、AES-GCM |
| `internal/pkg/ssh/` | SSH 客户端 + 连接池 |
| `internal/pkg/builder/` | Docker 构建 |
| `internal/pkg/git/` | Git clone |
| `internal/pkg/deploy/` | systemd、滚动、蓝绿、回滚、Nginx 联动 |
| `internal/pkg/monitor/` | 采样、阈值、Webhook |
| `internal/pkg/ws/` | WebSocket 框架 |
| `internal/pkg/errors/` | 错误码 + Gin 响应壳 |
| `web/` | 前端 Vite + React，含 embed 子包 |

## 3. 横切关注点

- **认证 / 鉴权**：HTTP 走 JWT 中间件；WebSocket 升级时一次性校验 token（query 或首帧）
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
| Java 进程托管 | Systemd 单元 `devops-<app_code>.service` | 避免 nohup 僵尸；自带优雅停机（SIGTERM → SIGKILL 90s） |
| 构建隔离 | Docker Engine API（可选） | 不污染宿主机的 JDK/Maven 版本 |
| 蓝绿切流 | Nginx upstream 重写 + `nginx -s reload` | 毫秒级无损切换，无需 K8s |

## 5. 数据模型概览

```
Host ──┬── Deployment ── Application ── Artifact
       │       │             │
       │       └── PipelineRun
       │
       └── (metrics in-memory)

AuditLog  独立轴
```

详见 `internal/model/types.go`。
