# swift-devops · 开发路线图

> 当前位置：v0.1 骨架（鉴权 + 数据模型 + 编译链路完成）。本文档跟踪到 v1.0 之间的工作。

## 1. 缺口全景

| 业务域 | 模型 | 业务实现 | 前端 |
|---|---|---|---|
| 鉴权 | ✓ | ✓ 登录 / JWT / Me | ✓ 登录页（裸样式） |
| 主机管理 | ✓ | ✗ | ✗ |
| 应用管理 | ✓ | ✗ | ✗ |
| 制品库 | ✓ | ✗ | ✗ |
| 部署控制 | ✓ | ✗ | ✗ |
| 监控告警 | ✗ | ✗ | ✗ |

骨架约 5%，业务约 95% 待写。

## 2. 业务域拆分

### P0 · 横切基础设施
- `pkg/crypto/aesgcm.go` 凭证 AES-GCM 加解密
- `pkg/errors/` 错误码 + 响应壳
- `middleware/audit.go` 审计中间件
- `middleware/ratelimit.go` 登录限流（IP + 用户名）
- `pkg/ws/` WebSocket 框架（升级 + 心跳 + 广播）

### P1 · 主机管理
**后端**
- `handler/host.go` CRUD + `group_tag`
- `service/host_service.go` 透明加解密
- `pkg/ssh/client.go` 单连接 + TOFU host key
- `pkg/ssh/pool.go` 连接池 + 空闲回收
- 连通性测试 endpoint（返回握手延迟）

**前端**
- `HostList.tsx` 列表 + 状态卡片（CPU/Mem/Disk/Load 快照）
- `HostForm.tsx` 录入/编辑 + 测连按钮

### P2 · 应用管理
**后端**
- `handler/app.go` + `service/app_service.go`
- `Deployment` 关联 CRUD（应用 × 主机）
- 环境变量 / JVM args 编辑接口

**前端**
- `AppList.tsx` / `AppForm.tsx` / `AppDetail.tsx`

### P3 · 制品与构建
**后端**
- 手动上传（multipart + MD5 + 大小限制）
- `pkg/git/clone.go`（HTTP/SSH 凭证）
- `pkg/builder/docker.go` Docker Engine API
- `ws/build/:run_id` 构建日志流
- 历史清理（保留 `max_history` 次）

**前端**
- `ArtifactList.tsx` / `BuildPage.tsx`
- `LogTerminal.tsx`（xterm.js）

### P4 · 部署控制
**后端**
- `pkg/deploy/dispatcher.go` SCP 分发
- `pkg/deploy/systemd.go` 单元文件 template + `systemctl`
- `pkg/deploy/health.go` actuator HTTP 探针
- `pkg/deploy/rolling.go` 滚动状态机（分批 + 健康门）
- `pkg/deploy/bluegreen.go` 双组并存
- `pkg/deploy/nginx.go` upstream 改写 + `nginx -s reload`
- `pkg/deploy/rollback.go` 反向部署
- `service/pipeline.go` Run 编排（事务 + 状态快照）

**前端**
- `PipelineDashboard.tsx`（Antd Steps + 实时日志）
- `BlueGreenSwitch.tsx`
- `DeployHistory.tsx`

### P5 · 监控告警
**后端**
- `pkg/monitor/syssampler.go` SSH 采样（vmstat / free / top）
- `pkg/monitor/jvmsampler.go` actuator metrics
- `pkg/monitor/store.go` 内存环形缓冲（24h × 5s ≈ 17280 点）
- `ws/metrics/:host_id` 推流
- `pkg/alert/rules.go` 阈值判定
- `pkg/alert/webhook.go` 钉钉 / 飞书

**前端**
- `HostMonitor.tsx` / `AppMonitor.tsx`
- `LogStream.tsx`（tail -f WS）
- Antd Charts 折线 / 仪表

### P6 · 加固（按需）
工作空间多环境隔离 · RBAC · 单测 / 集测 · GitHub Actions CI · 切 PG/MySQL · OAuth

## 3. 依赖关系

```
P0 加密 ─┐
P0 审计 ─┼──→ P1 主机 ──→ P2 应用 ──→ P3 制品 ──→ P4 部署 ──→ P5 监控
P0 WS   ─┘                              └─ Docker 构建        └─ 实时日志
                                                              └─ 告警
前端 Antd 壳子 ── 与 P1 同步开始
```

**Critical path**：`P0 加密 → P1 主机 → P2 应用 → P4 部署`。蓝绿 / Docker / 监控可后插。

## 4. Sprint 划分

### Sprint 1 · 主机端到端（约 1 周）✅ 已完成
- [x] `pkg/crypto/aesgcm.go` + 单测
- [x] `pkg/errors/` 错误壳
- [x] `middleware/audit.go`
- [x] `middleware/ratelimit.go`
- [x] router 接线 + auth 切错误壳
- [x] `pkg/ssh/client.go` + `pool.go` + TOFU
- [x] `service/host_service.go` + `handler/host.go` CRUD + 测连
- [x] `main.go` master_key fail-fast
- [x] 前端 Antd Layout + 路由 + AuthGuard
- [x] `HostList.tsx` + `HostForm.tsx`
- [x] 冒烟：登录 → 录主机 → 测连 → 编辑 → 删除（audit_logs 落 4 条）

### Sprint 2 · 应用 + 单主机部署（约 1 周）
- [x] 2.1 应用 CRUD + 配置中心（JVM args / env vars / 健康检查 URL）
- [x] 2.2 应用 × 主机关联（Deployment + 蓝绿分组 + 删除保护）
- [x] 2.3 制品 multipart 上传 + 路径白名单（`POST /artifacts/upload` 流式 + MD5 + 大小限制 + `app_code/版本-时间戳` 落地；旧 `POST /artifacts` 注册保留作兼容）—— 历史清理 `max_history` 仍留到 Sprint 5
- [x] 2.4 单主机部署最小闭环（SCP 分发 + systemd 单元 + 健康探针 + PipelineRun 状态机）
- [x] 2.4 polish-1 Docker sshd 容器集成测（dial → SFTP → write unit → fake systemctl → health 全链路）
- [x] 2.4 polish-2 systemd unit `User=` 透传（model + 校验 + 前端表单 + e2e 断言）
- [x] 2.4 polish-3 WebSocket 实时步骤推送（一次性 ticket 鉴权 + Hub 广播 + 前端 Drawer 接 WS）

### Sprint 3 · 滚动 + 回滚（约 1 周）
- [x] 3.1 抽 Strategy 接口 + PipelineRunHost 表 + Cancel 机制（`ctx.WithCancel` + `map[runID]cancelFunc`）
- [x] 3.2 Rolling 策略（固定 batch_size + 批内并行 + 批级 fail-fast；body 加 `batch_size`）
- [x] 3.3 一键回滚（`POST /apps/:id/rollback`，per-dep 用 `previous_artifact_id`；strategy.Rollback 顺序 fail-fast）
- [ ] 3.4 前端 PipelineDashboard 升级（Cancel 按钮 + 回滚按钮 + 批次进度卡片）
- [ ] 3.5 rolling 双容器集成测（基于 docker sshd × 2 验证端到端）

### Sprint 4 · 蓝绿 + Nginx（约 1 周）
- [ ] 蓝绿双组部署
- [ ] Nginx upstream 改写
- [ ] 前端 BlueGreenSwitch

### Sprint 5 · Docker 构建（约 1 周）
- [ ] Git clone
- [ ] Docker 容器构建
- [ ] WebSocket 日志流
- [ ] 制品历史清理（`max_history` 滚动删旧版 + 对应文件）

### Sprint 6 · 监控告警（约 1 周）
- [ ] 系统采样
- [ ] JVM actuator
- [ ] Webhook 告警
- [ ] 监控图表

## 5. 风险登记

| 风险 | 影响 | 缓解 |
|---|---|---|
| `master_key` 丢失 = 加密数据永久无解 | 致命 | P0 内规划备份方案（导出 / 双份配置） |
| SQLite 单机（并发写 / 远程 / HA） | 中 | store 抽象 Repository interface，留切换口 |
| WSL2 + `/mnt/e` 跨 fs 慢 | 开发效率 | 仓库 `cp -r` 到 `~/code/` 跑构建 |
| WebSocket 鉴权独立设计 | 安全 | P3 落地前定方案（首帧 / query / cookie） |
| Docker 构建 workspace 跨 fs | 性能 | `build_workspace` 配 `/var/lib/swift-devops/build` |
| pnpm + esbuild 警告每次报 | 体验 | `package.json` 加 `onlyBuiltDependencies` 白名单 |
| JWT 撤销靠 TTL（24h） | 安全 | 多用户 / RBAC 时改成 jti + 黑名单 |
