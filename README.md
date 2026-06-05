# swift-devops

> 单二进制 + 嵌入前端 + systemd/nohup 托管的 **Java 服务自动化运维平台**。
> 从 Git 源码构建到多主机部署、滚动发布、一键回滚，全程 Web 操作，零 Agent 侵入。

## 特性

- **主机管理** — SSH（密码 / 私钥）纳管，凭证 AES-256-GCM 加密，连通性测试 + TOFU host key
- **应用 / 微服务** — 一个业务系统挂 N 个微服务（AppService），per-service 端口 / JVM / 环境变量 / 启动序（Wave 调度）
- **源码构建** — 远端 Git 拉取 + 本机 Maven 构建；支持 local-jar / local-docker / remote-docker 应用级模式
- **部署策略** — 单实例 / 滚动（分批 + 健康门）；成功历史可作为回滚目标
- **一键回滚** — 以 Bundle 为粒度，跳过编译直接覆盖
- **实时反馈** — 构建日志 + 部署步骤经 WebSocket 实时推送（一次性 Ticket 鉴权）
- **进程托管** — systemd 单元（优雅停机 SIGTERM→SIGKILL）或 nohup（免 root），可按应用选择
- **制品治理** — 按 `max_history` 滚动清理历史 Bundle，自动跳过仍被部署 / 回滚链引用的版本

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.22+ · Gin · GORM · SQLite（`glebarez/sqlite` 纯 Go）· `golang.org/x/crypto/ssh` |
| 前端 | React 18 · TypeScript · Antd 5 · Vite（产物 embed 进二进制） |
| 部署 | 单静态二进制（`CGO_ENABLED=0`）· systemd / nohup / Docker |

## 快速开始

### 本地开发

```bash
# 前端热更 (5173) —— Vite dev server 反代后端 8088
make dev

# 另开一个终端跑后端
go run ./cmd/swift-devops serve --config data/config.dev.yaml

# 或一把梭：完整构建后直接跑（用 deploy/config.yaml.example）
make run
```

### 构建

```bash
make build          # 前端 pnpm build → 后端静态编译，产物 dist/swift-devops
make release        # 打 tar 包 + install.sh，产物 dist/release/
make release-arm64  # arm64 版本
```

> 前端需 pnpm（`packageManager: pnpm@11.3.0`）；无 pnpm 时 Makefile 回退 npm。

## 生产部署

`deploy/` 下有一键安装脚本（需 systemd + root）：

```bash
sudo bash deploy/install.sh                      # 安装最新版
sudo bash deploy/install.sh --port 9090          # 指定端口
sudo bash deploy/install.sh --upgrade            # 升级（保留配置与数据）
sudo bash deploy/install.sh --local ./pkg.tar.gz # 离线安装本地包
```

`install.sh` 自动完成：建 system 用户 → 装到 `/opt/swift-devops` → 数据落 `/var/lib` → **随机生成 `master_key` / `jwt_secret` / admin 密码** → 注册 systemd 服务（含 `NoNewPrivileges` / `ProtectSystem` 加固）。

| 路径 | 用途 |
|---|---|
| `/opt/swift-devops/` | 二进制 |
| `/etc/swift-devops/config.yaml` | 配置 |
| `/var/lib/swift-devops/` | SQLite + 制品 + 构建工作区 |
| `/var/log/swift-devops/` | stdout / stderr 日志 |

## 命令

```
swift-devops serve --config <path>         启动 HTTP 服务
swift-devops cleanup-apps --config <path>  清空应用链数据（保留主机/凭证/用户/构建环境/审计）
swift-devops hash-pwd <password>           生成 admin 密码的 bcrypt 哈希
swift-devops version                       打印版本
```

## 配置

参考 `deploy/config.yaml.example`，关键字段：

```yaml
security:
  master_key: "<base64 32B>"   # AES-256-GCM 主密钥，加密主机凭证
  jwt_secret: "<base64 32B>"
admin:
  username: admin
  password_bcrypt: "<hash-pwd 生成>"
storage:
  artifact_dir: ./data/artifacts    # 制品落盘根
  build_workspace: ./data/build     # 构建工作区
  max_history: 30                   # 每应用保留最近 N 个 Bundle，超出后构建时自动清理
builder:
  docker_enabled: false              # legacy 兼容字段；Maven 容器构建镜像功能已下线，当前不生效
```

### 构建环境

默认用 swift-devops 服务所在机器的本机 `git` / `mvn` / `java`。
登录后到「系统设置 / 构建环境」配置：

- `JAVA_HOME`
- `MAVEN_HOME`
- Git 可执行路径（可选）
- Maven 本地仓库（可选，留空使用 `settings.xml` 的 `<localRepository>`）

> 旧版「Docker 构建镜像」字段已下线，后续会重新设计为更清晰的构建执行器能力。
> 应用级 Docker 镜像构建 / remote-docker 部署仍在「应用详情 / 运行配置」与「服务 / Dockerfile 模板」里配置。

> ⚠ **`master_key` 是命门**：它加密所有主机 SSH 凭证。务必备份；一旦丢失，已纳管主机的密码 / 私钥无法解密，只能重录。

## 项目结构

```
cmd/swift-devops/   CLI 入口（serve / cleanup-apps / hash-pwd）
internal/
  api/              路由 + handler（含 WS）+ middleware
  service/          业务编排（host / app / artifact / build / pipeline）
  pkg/              crypto · ssh · builder · deploy · ws · errors
  model/            GORM 实体（13 表）
  store/            DB 连接 + AutoMigrate
web/                前端 React + Vite（embed 进二进制）
deploy/             config 样例 + install.sh + systemd unit
docs/               架构与路线图
```
