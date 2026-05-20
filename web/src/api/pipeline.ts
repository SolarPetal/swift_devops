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

export async function deployApp(appId: number, artifactId: number, strategy = 'single'): Promise<PipelineRun> {
  const r = await api.post<PipelineRun>(`/apps/${appId}/deploy`, {
    artifact_id: artifactId,
    strategy,
  })
  return r.data
}
