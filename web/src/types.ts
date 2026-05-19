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
