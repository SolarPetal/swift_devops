## Java 服务自动化运维管理平台
### 详细需求文档 (PRD) 与技术方案 (Tech Spec)
#### 第一部分：详细产品需求文档 (PRD)
1. 概念与核心实体关系
系统内部核心实体包含：工作空间（Workspace）、主机（Host）、应用（Application）、制品（Artifact）、流水线（Pipeline）。

一个工作空间可包含多台主机和多个应用（实现环境隔离，如：开发、测试、生产）。

一个应用可以部署在多台主机上（构成集群）。

应用每次构建或上传，生成一个唯一的制品。

2. 核心功能模块详细需求
2.1 主机管理模块 (Host Management)
功能描述：录入并管理目标服务器的连接凭证，监控主机的健康状态。

详细需求：

凭证支持：支持 用户名/密码 和 SSH 密钥对（Private Key） 两种认证方式。

连通性测试：添加主机时必须提供“测试连接”按钮，实时返回 SSH 握手结果及延迟。

状态看板：每台主机卡片展示核心指标快照：CPU 使用率、内存使用率、磁盘剩余空间、系统负载（LoadAvg）。

2.2 应用管理与配置中心 (Application & Config)
功能描述：定义业务服务属性，统一管理运行时的环境变量。

详细需求：

应用元数据：应用名称、唯一标识（AppCode）、服务类型（单体 Jar 包 / Spring Cloud 微服务）。

进程端口管理：定义应用在目标机上运行的默认端口、健康检查路径（例如 /actuator/health 或 /health）。

动态配置中心：支持针对不同主机环境配置环境变量（如 SPRING_PROFILES_ACTIVE=test）或 JVM 启动参数（如 -Xms512m -Xmx512m），在部署时由系统动态注入。

2.3 双模构建与流水线 (CI/CD Pipeline)
功能描述：支持“源码编译”与“直接分发”双轨制，生成标准制品。

详细需求：

手动上传流程（模式 A）：用户直接在网页拖拽上传 *.jar 文件，系统自动计算 MD5 校验码，存入制品库，标记版本号（如 v1.0.0-build1）。

Git 源码构建流程（模式 B）：

用户配置 Git 仓库地址（支持 HTTP/SSH 凭证）及分支（Branch/Tag）。

选择构建环境（如 JDK 8 / 11 / 17 / 21，Maven 3.6+）。

触发构建后，系统提供实时滚动日志窗口，展示 Maven 编译、打包的全过程。

制品库历史：保留最近 30 次的构建物，支持对任意历史制品执行“一键分发部署”。

2.4 发版、回滚与蓝绿发布 (Deployment Control)
功能描述：控制应用制品在目标主机集群上的上线、下线与平滑切换。

详细需求：

标准滚动发布：支持分批发布。例如一个应用部署在 4 台主机上，可配置每批发布 2 台。第一批发布成功并通过健康检查后，才允许推进第二批。

蓝绿发布：

系统支持将主机划分为“蓝组”和“绿组”。

发布时，新版本全部部署到“绿组”；此时“蓝组”继续支撑线上流量。

提供一个“切换流量”按钮，点击后通过修改网关（如 Nginx/网关服务）路由，将流量引向“绿组”。

一键回滚：当新版出现故障，用户在发布历史中点击“回滚”，系统自动拉取上一版本的备份制品，直接覆盖部署，跳过编译步骤，要求 30 秒内完成单个实例的重启。

2.5 服务与资源监控 (Monitoring & Alert)
功能描述：无需安装第三方 Agent，直接在 Web 端查看主机和 Java 进程的运行状态。

详细需求：

实时日志流：在 Web 端提供类似 tail -f 的功能，实时查看指定主机的特定应用日志，支持关键词过滤。

JVM 深度监控：针对 Java 应用，实时抓取并以图表展示：堆内存（Heap）使用曲线、非堆内存（Non-Heap）使用曲线、线程总数/死锁检测、GC（YGC/FGC）次数及耗时。

异常告警：当 CPU > 90%、内存 > 95% 或服务健康检查连续 3 次失败时，通过 Webhook 向指定通道（钉钉/飞书）推送告警消息。
#### 第二部分：详细技术方案 (Tech Spec)
1. 系统架构与技术栈
本系统采用全栈轻量化架构，前端通过 HTTP 状态轮询及 WebSocket 与 Go 后端保持实时通信。

前端生态（React）：React 18 + TypeScript + Ant Design (Antd 5.x) + UmiJS/Vite + Ant Design Charts (基于 G2Plot，用于监控图表)。

后端生态（Go）：Go 1.22+ + Gin (Web 框架) + GORM (ORM 框架) + Go-SSH (原生 SSH 客户端库)。

数据存储：支持关系型数据库（MySQL 5.7+/8.0 或 PostgreSQL 12+），利用 ORM 实现屏蔽。

2. 数据库设计 (Schema Spec)
选择关系型数据库，核心表结构设计如下（以 MySQL 为例）：

2.1 主机表 (hosts)
SQL
CREATE TABLE `hosts` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(100) NOT NULL COMMENT '主机别名',
  `ip` varchar(50) NOT NULL COMMENT 'IP地址',
  `port` int NOT NULL DEFAULT '22' COMMENT 'SSH端口',
  `auth_type` varchar(20) NOT NULL COMMENT '认证类型: password, key',
  `username` varchar(50) NOT NULL COMMENT 'SSH用户名',
  `password` text COMMENT '加密存储的密码',
  `private_key` text COMMENT 'SSH私钥',
  `status` varchar(20) DEFAULT 'unknown' COMMENT '健康状态: online, offline, unknown',
  `created_at` datetime DEFAULT NULL,
  `updated_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
2.2 应用表 (applications)
SQL
CREATE TABLE `applications` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `app_code` varchar(50) NOT NULL COMMENT '应用唯一标识',
  `name` varchar(100) NOT NULL COMMENT '应用名称',
  `git_url` varchar(255) DEFAULT '' COMMENT 'Git仓库地址',
  `git_credential` varchar(100) DEFAULT '' COMMENT 'Git凭证ID',
  `deploy_path` varchar(255) NOT NULL COMMENT '服务器部署绝对路径',
  `port` int NOT NULL COMMENT '运行端口',
  `health_check_url` varchar(255) DEFAULT '/actuator/health' COMMENT '健康检查URL',
  `jvm_args` text COMMENT 'JVM启动参数',
  `env_vars` text COMMENT '环境变量JSON字符串',
  `created_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_app_code` (`app_code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
2.3 制品表 (artifacts)
SQL
CREATE TABLE `artifacts` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `app_id` bigint unsigned NOT NULL,
  `version_tag` varchar(50) NOT NULL COMMENT '版本标签',
  `file_name` varchar(255) NOT NULL COMMENT '文件名',
  `file_path` varchar(255) NOT NULL COMMENT '服务器本地存储绝对路径',
  `file_md5` varchar(32) NOT NULL COMMENT 'MD5校验值',
  `build_status` varchar(20) NOT NULL COMMENT '状态: success, failed, building',
  `created_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
3. 核心技术实现方案
3.1 基于 Docker 容器的隔离构建（CI 落地）
为了避免在控制机安装多个版本的 Maven 和 JDK，系统采用 Docker Engine API 动态拉起构建容器。

技术流转步骤：

Go 后端调用 Git 库（或执行 git clone）将代码拉取到宿主机的临时工作目录 /tmp/build/<task_id>。

调用 Docker 接口，创建并启动容器，挂载宿主机目录：

将源码目录挂载至容器内的 /src。

将宿主机的 .m2/repository 挂载至容器内的 /root/.m2/repository（确保 Maven 本地缓存生效，避免重复下载依赖）。

容器启动命令设置为：mvn clean package -DskipTests。

Go 后端通过 Docker 的 ContainerLogs API 获取标准输出流，并通过 WebSocket 实时推给 React 前端。

编译完成后，提取容器内 target/*.jar 到制品目录，随后调用 ContainerRemove 销毁容器。

3.2 基于 Systemd 的 Java 进程托管与优雅停机（CD 落地）
传统 nohup java -jar & 极难管理，容易产生僵尸进程。本系统统一采用 Linux 原生的 Systemd 服务脚本自动生成与托管技术。

当应用首次发布到目标主机时，系统通过 Go SSH 在目标机 /etc/systemd/system/ 目录下生成一个名为 devops-<app_code>.service 的标准服务文件：

Ini, TOML
[Unit]
Description=DevOps Managed Java Service: {{.AppName}}
After=network.target

[Service]
Type=simple
User={{.User}}
Environment={{.EnvVars}}
ExecStart=/usr/bin/java {{.JvmArgs}} -jar {{.DeployPath}}/{{.JarName}}
SuccessExitStatus=143
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
控制指令逻辑：

启动服务：systemctl daemon-reload && systemctl enable devops-app.service && systemctl start devops-app.service

优雅停机：执行 systemctl stop devops-app.service。Systemd 默认会向 Java 进程发送 SIGTERM (kill -15) 信号。Java 进程（特别是 Spring Boot）接收到信号后，会触发 ContextClosedEvent，拒绝新请求并处理完当前存量请求后优雅退出。若 90 秒内未退出，Systemd 会强制发送 SIGKILL (kill -9)。

3.3 传统 VM 蓝绿发布与 Nginx 联动切换方案
在没有容器集群的情况下，蓝绿发布通过控制前端反向代理 Nginx 的 upstream 实现。

基础设施准备：一个应用在目标服务器上同时部署两套独立进程。

蓝组（Blue）：运行在 192.168.1.10:8080

绿组（Green）：运行在 192.168.1.10:8081

Nginx 配置模版化：在 Nginx 服务器上，为该应用维护一个独立的 conf.d/app_upstream.conf 文件：

Nginx
upstream app_backend {
    server 192.168.1.10:8080 weight=100; # 蓝色组
    server 192.168.1.10:8081 weight=0 down; # 绿色组（初始下线）
}
技术流转：

发布绿组：系统将新制品发往 8081 端口，启动并等待健康检查通过。此时线上流量仍全部在 8080。

流量切替：用户在 React 前端点击“切流”，Go 后端通过 SSH 连接到 Nginx 服务器，直接修改 app_upstream.conf 文件，将 8080 标记为 down，将 8081 权重设为 100。

重载生效：远程执行 nginx -s reload。由于 Nginx 的 master-worker 机制，此操作为毫秒级无损切换，长连接不会中断。

3.4 零 Agent 监控：SSH 远程采样与 Actuator 整合
系统算力监控：Go 后端维持一个定时的 Goroutine 线程池，每隔 5 秒通过已建立的 SSH 连接向主机发送复合指令：





# 采集CPU、内存与负载
    vmstat 1 2 | tail -n 1; free -m | grep Mem; top -b -n 1 | head -n 5
    ```
    后端解析返回的文本字符串，结构化后存入时序结构，并通过 WebSocket 广播给打开了监控页面的 React 前端。
*   **JVM 深度监控指标提取**：Go 后端定时向应用配置的 `http://<target_ip>:<port>/actuator/metrics/...` 发起 HTTP 请求：
    *   堆内存：请求 `/actuator/metrics/jvm.memory.used?tag=area:heap`
    *   GC 情况：请求 `/actuator/metrics/jvm.gc.pause`
    前端 React 拿到这些增量 JSON 数据后，直接喂给 `Ant Design Charts` 的折线图组件，实现近乎实时的 JVM 性能看板。

---

## 第三部分：React 前端交互与核心状态设计

### 1. 技术栈落地清单
*   **脚手架与路由**：Vite + React Router v6。
*   **状态管理**：Zustand（相比 Redux 更轻量，适合控制复杂的发布流状态）。
*   **网络请求**：Axios + SSE（Server-Sent Events，用于轻量监控数据推送）。

### 2. 核心页面交互流设计

#### 2.1 流水线发布大屏（Pipeline Dashboard）
*   **组件设计**：使用 Antd 的 `Steps`（步骤条）组件展示：`[代码拉取] -> [容器构建] -> [分发制品] -> [集群部署] -> [健康探针]`。
*   **日志组件**：使用 `Xterm.js`（或标准文本框配合 `Fira Code` 字体）作为终端容器。通过 WebSocket 接收 Go 后端推送的编译日志或 `tail -f` 业务日志。当监测到滚动条处于最底部时，开启自动滚屏。

#### 2.2 蓝绿双子星切换开关（Component Design）
*   **UI 交互**：采用可视化仪表盘，左边为“Blue 节点组”，右边为“Green 节点组”。中间由一个通过 `Canvas` 或 `CSS3` 绘制的“流量水管/箭头”连接。
*   **状态控制**：
    *   当点击“执行绿组发布”时，绿组卡片进入 Loading 状态，步骤条展示绿组部署进展。
    *   当绿组部署成功，中间的“流量切替”滑动开关激活。用户向右滑动，触发 `POST /api/v1/pipeline/switch-traffic`，成功后箭头动画指向绿组，蓝组节点变为灰色待命状态。