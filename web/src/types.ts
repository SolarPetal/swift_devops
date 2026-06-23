// 共用类型 —— 与后端 service/handler 的 JSON 协议保持同步。

export type Host = {
  id: number
  name: string
  ip: string
  port: number
  auth_type: 'password' | 'key'
  username: string
  status: 'online' | 'offline' | 'unknown'
  tags?: string
  java_path: string  // Sprint 3.7：远端 java 可执行路径，默认 /usr/bin/java
  running_instance_count: number
  has_secret: boolean
  has_host_key: boolean
  created_at: string
  updated_at: string
}

export type HostDockerContainer = {
  id: string
  name: string
  image: string
  state: string
  status: string
  ports: string
  created_at: string
}

export type HostDockerLogs = {
  host_id: number
  host_name: string
  host_ip: string
  container_name: string
  lines: number
  status: string
  logs: string
  captured_at: string
}

export type HostMetrics = {
  host_id: number
  host_name: string
  host_ip: string
  host_status: 'online' | 'offline' | 'unknown' | string
  cpu_usage: number
  memory_usage: number
  disk_usage: number
  load1: number
  checked_at: string
  cached: boolean
  error?: string
}

export type HostInput = {
  name: string
  ip: string
  port?: number
  auth_type: 'password' | 'key'
  username: string
  password?: string
  private_key?: string
  passphrase?: string
  tags?: string
  java_path?: string  // 留空 = /usr/bin/java
}

export type TestResult = {
  ok: boolean
  latency_ms: number
  host_key?: string
  error?: string
}

export type ApiError = {
  error: { code: string; message: string }
}

// Sprint 5.1：Git 凭证
export type GitCredential = {
  id: number
  name: string
  type: 'token' | 'ssh_key'
  username: string
  has_secret: boolean
  created_at: string
  updated_at: string
}

export type GitCredentialInput = {
  name: string
  type: 'token' | 'ssh_key'
  username?: string
  secret?: string // Update 时留空保留旧值
}

// Sprint 5.2/5.3：构建任务
export type BuildRun = {
  id: number
  app_id: number
  git_ref: string
  commit_sha: string
  mvn_args: string
  cred_id: number
  status: 'building' | 'success' | 'failed' | 'cancelled'
  log_path: string
  artifact_id: number  // 旧链路兼容；多 service / 重构后默认 0
  bundle_id: number    // Sprint X.6：构建成功后回填的 ArtifactBundle id；旧构建为 0
  triggered_by: string
  error: string
  started_at?: string
  finished_at?: string
  created_at: string
}

export type BuildTriggerInput = {
  git_ref?: string
  mvn_args?: string
  cred_id?: number
  build_module?: string         // Sprint 5.4.7 临时覆盖
  build_jar_pattern?: string    // Sprint 5.4.7 临时覆盖
}

// Sprint 5.4：构建机环境配置（单例）
export type BuilderEnv = {
  java_home: string
  maven_home: string
  git_path: string
  maven_local_repo: string  // Sprint X.9：空 = 用 mvn settings.xml 默认 <localRepository>
  java_version: string
  maven_version: string
  git_version: string
  detected_at?: string
  valid: boolean
  detect_message: string
}

export type BuilderEnvInput = {
  java_home: string
  maven_home: string
  git_path?: string
  maven_local_repo?: string  // Sprint X.9
}

export type App = {
  id: number
  app_code: string
  name: string
  app_type: string
  git_url: string
  git_cred_id: string
  git_ref: string
  git_refs: string[]
  deploy_path: string
  port: number
  health_check_url: string
  jvm_args: string
  env_vars: string
  systemd_user: string
  java_path: string  // Sprint 3.7：应用级 java_path 覆盖；空 = 沿用 Host.java_path
  // Sprint 5.4.7 构建参数（multi-module 项目）
  build_module: string        // mvn -pl 用，如 "car-dealer-admin"
  build_jar_pattern: string   // glob 选 jar，如 "car-dealer-admin/target/*.jar"
  // Legacy traffic-switch fields：功能已下线，字段保留为后端兼容，写入会被清空。
  nginx_host_id: number
  nginx_upstream_name: string
  active_group: string
  // Sprint X.10 + X.11：构建 & 部署模式
  //   build_mode:
  //     'local-jar'      → 本机 Maven 打包 jar（默认）
  //     'local-docker'   → 本机 Maven → Dockerfile → docker build → push
  //     'remote-docker'  → 本机 Maven 打 jar → 部署时推 jar+Dockerfile 到目标机 docker build/run
  build_mode: 'local-jar' | 'local-docker' | 'remote-docker' | ''
  //   deploy_mode:
  //     'systemd' → systemd service + systemctl（默认，需 root/sudo）
  //     'nohup'   → nohup java -jar + app.pid（免 root，crash 不自愈）
  //     'docker'  → docker pull + docker run（Sprint X.11 新增）
  deploy_mode: 'systemd' | 'nohup' | 'docker' | ''
  // Sprint X.11：Docker 配置
  docker_registry: string      // 镜像仓库地址，如 docker.io / harbor.example.com
  docker_image_name: string    // 镜像名，如 myapp/user-service
  docker_image_tag: string     // 镜像标签模板，如 git-{sha}-{build_id} / latest
  dockerfile: string           // 自定义 Dockerfile（可选）
  docker_build_args: string    // docker build 参数
  docker_container_name: string // 容器名模板，如 {{APP_CODE}}-{{SERVICE_CODE}}；空 = devops-<service>
  docker_run_args: string      // docker run 参数，如 -p 8080:8080 -e ENV=prod
  created_at: string
  updated_at: string
}

export type AppInput = Omit<App, 'id' | 'created_at' | 'updated_at'>

// Sprint X.1/X.4：AppService 可部署服务层
export type AppService = {
  id: number
  app_id: number
  service_code: string
  name: string
  build_module: string
  build_jar_pattern: string
  port: number
  health_check_url: string
  jvm_args: string
  env_vars: string
  systemd_user: string
  java_path: string
  startup_order: number
  optional: boolean
  enabled: boolean
  // Legacy traffic-switch fields：功能已下线，字段保留为后端兼容，写入会被清空。
  nginx_host_id: number
  nginx_upstream_name: string
  active_group: string
  // Sprint X.10 + X.11：部署模式。空串 = 沿用 app 字段。
  deploy_mode: 'systemd' | 'nohup' | 'docker' | ''
  // Sprint X.11：Docker 配置（空时回退 Application 的对应字段）
  docker_registry: string
  docker_image_name: string
  docker_image_tag: string
  dockerfile_template_id: number
  dockerfile: string
  docker_build_args: string
  docker_container_name: string
  docker_run_args: string
  created_at: string
  updated_at: string
}

export type AppServiceInput = {
  service_code: string
  name?: string
  build_module?: string
  build_jar_pattern?: string
  port: number
  health_check_url?: string
  jvm_args?: string
  env_vars?: string
  systemd_user?: string
  java_path?: string
  startup_order?: number
  optional?: boolean
  enabled?: boolean
  // Sprint X.10 + X.11：部署模式（覆盖 app 字段；空 = 沿用）
  deploy_mode?: 'systemd' | 'nohup' | 'docker' | ''
  // Sprint X.11：Docker 配置（可选，空时回退 app 字段）
  docker_registry?: string
  docker_image_name?: string
  docker_image_tag?: string
  dockerfile_template_id?: number
  dockerfile?: string
  docker_build_args?: string
  docker_container_name?: string
  docker_run_args?: string
}

export type AppServiceSuggestion = {
  service_code: string
  name: string
  build_module: string
  build_jar_pattern: string
  port: number
  health_check_url: string
  startup_order: number
  optional: boolean
  enabled: boolean
  recommended: boolean
  confidence: 'high' | 'medium' | 'low' | string
  reason: string
  artifact_id: string
  packaging: string
  existing: boolean
}

export type AppServiceBatchImportResult = {
  items: AppService[]
  created: number
  updated: number
  skipped: number
  disabled_default: boolean
}

export type Deployment = {
  id: number
  app_id: number
  host_id: number
  service_code: string
  host_name: string
  host_ip: string
  host_status: 'online' | 'offline' | 'unknown'
  current_artifact_id: number
  previous_artifact_id: number
  current_artifact_item_id: number
  previous_artifact_item_id: number
  current_bundle_id: number
  current_bundle_version: string
  port: number
  status: 'pending' | 'running' | 'stopped' | 'failed'
  last_run_id: number
  last_deploy_status: 'pending' | 'running' | 'success' | 'failed' | 'skipped' | ''
  last_deploy_stage: string
  last_deploy_error: string
  last_deploy_at: string
  runtime_container_name: string
  runtime_checked_at: string
  runtime_status_detail: string
  runtime_check_error: string
  created_at: string
  updated_at: string
}

export type DeploymentInput = {
  host_id: number
  service_code?: string
  port?: number
}

export type DeploymentRuntimeLogs = {
  deployment_id: number
  app_id: number
  host_id: number
  host_name: string
  host_ip: string
  service_code: string
  deploy_mode: string
  container_name: string
  lines: number
  status: string
  logs: string
  captured_at: string
}

export type DeploymentRuntimeAction = {
  deployment_id: number
  app_id: number
  host_id: number
  host_name: string
  host_ip: string
  service_code: string
  deploy_mode: string
  container_name: string
  action: 'stop' | 'restart'
  status: 'pending' | 'running' | 'stopped' | 'failed'
  message: string
  operated_at: string
}

// --- 前端项目 Docker 部署 / 网关容器 ---

export type FrontendPackageManager = 'auto' | 'npm' | 'pnpm' | 'yarn'

export type FrontendAppConfig = {
  id: number
  app_id: number
  service_code: string
  package_manager: FrontendPackageManager
  install_command: string
  build_command: string
  dist_dir: string
  node_image: string
  nginx_image: string
  spa_fallback: boolean
  container_name: string
  target_port: number
  dockerfile: string
  nginx_config: string
  docker_build_args: string
  docker_run_args: string
  created_at: string
  updated_at: string
}

export type FrontendAppConfigInput = Partial<Omit<FrontendAppConfig, 'id' | 'app_id' | 'created_at' | 'updated_at'>>

export type FrontendTemplatePreview = {
  dockerfile: string
  nginx_config: string
}

export type FrontendGateway = {
  id: number
  host_id: number
  host_name: string
  host_ip: string
  container_name: string
  network_name: string
  image: string
  http_port: number
  https_port: number
  config_dir: string
  cert_dir: string
  log_dir: string
  status: string
  created_at: string
  updated_at: string
}

export type FrontendGatewayEnsureInput = {
  container_name?: string
  network_name?: string
  image?: string
  http_port?: number
  https_port?: number
  base_dir?: string
  config_dir?: string
  cert_dir?: string
  log_dir?: string
  start?: boolean
  force_recreate?: boolean
}

export type FrontendGatewayRoute = {
  id: number
  gateway_id: number
  host_id: number
  app_id: number
  app_code: string
  app_name: string
  service_code: string
  domain: string
  container_name: string
  target_port: number
  https: boolean
  cert_path: string
  key_path: string
  enabled: boolean
  status: string
  created_at: string
  updated_at: string
}

export type FrontendGatewayApply = {
  gateway: FrontendGateway
  routes: FrontendGatewayRoute[]
  applied_at: string
  message: string
}

export type FrontendGatewayPreview = {
  route_id: number
  file_name: string
  content: string
}

export type FrontendDeployInput = {
  host_id: number
  service_code?: string
  git_ref?: string
  cred_id?: number
  domain?: string
  host_port?: number
  https?: boolean
  cert_path?: string
  key_path?: string
  apply_gateway?: boolean
  force_recreate_gateway?: boolean
}

export type FrontendRollbackInput = {
  host_id: number
  service_code?: string
  domain?: string
  apply_gateway?: boolean
  force_recreate_gateway?: boolean
}

export type FrontendDeployResult = {
  app_id: number
  app_code: string
  service_code: string
  host_id: number
  pipeline_run_id: number
  pipeline_status: string
  domain: string
  container_name: string
  image: string
  commit_sha: string
  remote_work_dir: string
  gateway_route: FrontendGatewayRoute
  gateway_apply?: FrontendGatewayApply
  deployed_at: string
}

export type FrontendDeploymentState = {
  id: number
  app_id: number
  app_code: string
  app_name: string
  host_id: number
  host_name: string
  host_ip: string
  service_code: string
  domain: string
  container_name: string
  target_port: number
  current_image: string
  current_commit_sha: string
  current_remote_work_dir: string
  current_pipeline_run_id: number
  current_deployed_at: string
  previous_image: string
  previous_commit_sha: string
  previous_remote_work_dir: string
  previous_pipeline_run_id: number
  previous_deployed_at: string
  status: string
  created_at: string
  updated_at: string
}

// --- 制品 ---

export type Artifact = {
  id: number
  app_id: number
  version_tag: string
  file_name: string
  file_path: string
  file_md5: string
  file_size: number
  build_status: string
  created_at: string
}

export type ArtifactInput = {
  app_id: number
  version_tag: string
  file_path: string
  file_name?: string
}

// Sprint X.6：ArtifactBundle / ArtifactItem 整组制品
export type ArtifactItemView = {
  id: number
  bundle_id: number
  service_code: string
  file_name: string
  file_path: string
  file_md5: string
  file_size: number
  docker_image: string
  dockerfile_name: string
  dockerfile_content: string
  created_at: string
}

export type DockerfileTemplate = {
  id: number
  app_id: number
  name: string
  description: string
  content: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export type DockerfileTemplateInput = {
  name: string
  description?: string
  content: string
  is_default?: boolean
}

export type ArtifactBundle = {
  id: number
  app_id: number
  version_tag: string
  git_commit_sha: string
  build_status: string
  build_log_path: string
  triggered_by: string
  created_at: string
  items: ArtifactItemView[]
}

// --- 流水线 ---

export type PipelineStage =
  | 'dial'
  | 'env_check'
  | 'upload'
  | 'write_unit'
  | 'restart'
  | 'health'
  | 'nginx_apply'
  | 'frontend_prepare'
  | 'git_clone'
  | 'frontend_package'
  | 'docker_build'
  | 'docker_image_check'
  | 'docker_run'
  | 'gateway_route'
  | 'gateway_apply'

export type StepResult = {
  host_id: number
  host_name: string
  host_ip: string
  stage: PipelineStage
  ok: boolean
  detail?: string
  error?: string
  started_at: string
  ended_at: string
}

export type RunSnapshot = {
  strategy: string
  artifact_id: number
  started_at: string
  finished_at?: string
  steps?: StepResult[]
  error?: string
}

export type PipelineRun = {
  id: number
  app_id: number
  artifact_id: number
  bundle_id: number
  previous_bundle_id: number
  is_current?: boolean
  current_partial?: boolean
  strategy: string // 'single' | 'rolling' | 'rollback' | 'frontend' | 'frontend_rollback'
  status: 'pending' | 'running' | 'success' | 'failed' | 'cancelled'
  state_snapshot: string // JSON 字符串，前端 JSON.parse 成 RunSnapshot
  triggered_by: string
  started_at?: string
  finished_at?: string
  created_at: string
}
