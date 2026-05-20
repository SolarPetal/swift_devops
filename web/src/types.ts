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

export type PipelineStage = 'dial' | 'upload' | 'write_unit' | 'restart' | 'health'

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
  strategy: string
  status: 'pending' | 'running' | 'success' | 'failed' | 'interrupted'
  state_snapshot: string // JSON 字符串，前端 JSON.parse 成 RunSnapshot
  triggered_by: string
  started_at?: string
  finished_at?: string
  created_at: string
}
