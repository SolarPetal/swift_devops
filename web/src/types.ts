// 共用类型 —— 与后端 service/handler 的 JSON 协议保持同步。

export type Host = {
  id: number
  name: string
  ip: string
  port: number
  auth_type: 'password' | 'key'
  username: string
  status: 'online' | 'offline' | 'unknown'
  group_tag?: string
  tags?: string
  java_path: string  // Sprint 3.7：远端 java 可执行路径，默认 /usr/bin/java
  has_secret: boolean
  has_host_key: boolean
  created_at: string
  updated_at: string
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
  group_tag?: string
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
  artifact_id: number
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
}

// Sprint 5.4：构建机环境配置（单例）
export type BuilderEnv = {
  java_home: string
  maven_home: string
  git_path: string
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
}

export type App = {
  id: number
  app_code: string
  name: string
  app_type: string
  git_url: string
  git_cred_id: string
  deploy_path: string
  port: number
  health_check_url: string
  jvm_args: string
  env_vars: string
  systemd_user: string
  java_path: string  // Sprint 3.7：应用级 java_path 覆盖；空 = 沿用 Host.java_path
  // Sprint 4 蓝绿配置（可选）
  nginx_host_id: number       // 0 = 未启用蓝绿
  nginx_upstream_name: string // upstream block 名，与 nginx_host_id 同填同空
  active_group: string        // 'blue' | 'green' | ''；蓝绿部署成功后由后端写入
  created_at: string
  updated_at: string
}

export type AppInput = Omit<App, 'id' | 'created_at' | 'updated_at'>

export type Deployment = {
  id: number
  app_id: number
  host_id: number
  host_name: string
  host_ip: string
  host_status: 'online' | 'offline' | 'unknown'
  group_tag: '' | 'blue' | 'green'
  current_artifact_id: number
  previous_artifact_id: number
  port: number
  status: 'pending' | 'running' | 'stopped' | 'failed'
  created_at: string
  updated_at: string
}

export type DeploymentInput = {
  host_id: number
  group_tag?: '' | 'blue' | 'green'
  port?: number
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

// --- 流水线 ---

export type PipelineStage = 'dial' | 'env_check' | 'upload' | 'write_unit' | 'restart' | 'health' | 'nginx_apply'

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
  strategy: string // 'single' | 'rolling' | 'rollback'
  status: 'pending' | 'running' | 'success' | 'failed' | 'cancelled'
  state_snapshot: string // JSON 字符串，前端 JSON.parse 成 RunSnapshot
  triggered_by: string
  started_at?: string
  finished_at?: string
  created_at: string
}
