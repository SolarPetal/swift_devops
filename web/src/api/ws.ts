// WebSocket 一次性 ticket 客户端。
//
// 后端 POST /api/v1/ws-tickets {subject:"pipeline:5"} 返回 {ticket, expires_at}。
// 5s 内拿着 ticket 走 GET /api/v1/ws/pipelines/5?ticket=<id> 升级。
import { api } from './client'

type TicketResp = { ticket: string; expires_at: string }

export async function issueWSTicket(subject: string): Promise<TicketResp> {
  const r = await api.post<TicketResp>('/ws-tickets', { subject })
  return r.data
}

// 把 /api/v1/ws/xxx 路径 + ticket 拼成完整 ws(s) URL。
export function buildWSURL(path: string, ticket: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const sep = path.includes('?') ? '&' : '?'
  return `${proto}//${window.location.host}${path}${sep}ticket=${encodeURIComponent(ticket)}`
}

// 标准事件 schema（与 service.PipelineEvent 对齐）
import type { PipelineRun, RunSnapshot, StepResult } from '../types'

export type PipelineWSEvent =
  | { type: 'snapshot'; run_id: number; status: PipelineRun['status']; snapshot: RunSnapshot; ts: string }
  | { type: 'status'; run_id: number; status: PipelineRun['status']; ts: string }
  | { type: 'step'; run_id: number; step: StepResult; ts: string }

// 构建日志 WS 事件（与 service.BuildEvent 对齐，Sprint 5.5）
//   - snapshot：连上首帧，全量日志 + 当前状态
//   - log：增量日志块，前端 append
//   - status：构建终态（success/failed/cancelled）
export type BuildWSEvent =
  | { type: 'snapshot'; build_id: number; log: string; status: string; ts: string }
  | { type: 'log'; build_id: number; chunk: string; ts: string }
  | { type: 'status'; build_id: number; status: string; ts: string }
