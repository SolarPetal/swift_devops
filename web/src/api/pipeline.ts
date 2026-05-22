import { api } from './client'
import type { PipelineRun } from '../types'

export async function listPipelines(appId?: number): Promise<PipelineRun[]> {
  const r = await api.get<{ items: PipelineRun[] }>('/pipelines', {
    params: appId ? { app_id: appId } : undefined,
  })
  return r.data.items
}

export async function getPipeline(id: number): Promise<PipelineRun> {
  const r = await api.get<PipelineRun>(`/pipelines/${id}`)
  return r.data
}

// deployApp 触发部署。
//   - strategy='single'（默认）：顺序逐台、fail-fast
//   - strategy='rolling'：分批并行，batchSize 必传 >=1
//   - strategy='blue_green'：后端按 active_group 自动选目标组（active→opposite，空→blue）
//     需要 App 已配 nginx_host_id + nginx_upstream_name；前端不必传 target_group
export async function deployApp(
  appId: number,
  artifactId: number,
  strategy: 'single' | 'rolling' | 'blue_green' = 'single',
  batchSize?: number,
): Promise<PipelineRun> {
  const body: Record<string, unknown> = {
    artifact_id: artifactId,
    strategy,
  }
  if (strategy === 'rolling') {
    body.batch_size = batchSize ?? 1
  }
  const r = await api.post<PipelineRun>(`/apps/${appId}/deploy`, body)
  return r.data
}

// cancelPipeline 请求取消运行中的 pipeline。后端 202 异步生效；最终 status 由 execute 收口。
export async function cancelPipeline(id: number): Promise<void> {
  await api.post(`/pipelines/${id}/cancel`)
}

// rollbackApp 一键回滚：每个 deployment 退到自己的 previous_artifact_id。
// 没有任何 deployment 有 previous → 后端 400。
export async function rollbackApp(appId: number): Promise<PipelineRun> {
  const r = await api.post<PipelineRun>(`/apps/${appId}/rollback`)
  return r.data
}
