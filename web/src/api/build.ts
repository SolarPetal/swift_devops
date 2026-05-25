import { api } from './client'
import type { BuildRun, BuildTriggerInput } from '../types'

export async function listBuilds(appId?: number): Promise<BuildRun[]> {
  const r = await api.get<{ items: BuildRun[] }>('/builds', {
    params: appId ? { app_id: appId } : undefined,
  })
  return r.data.items
}

export async function getBuild(id: number): Promise<BuildRun> {
  const r = await api.get<BuildRun>(`/builds/${id}`)
  return r.data
}

export async function triggerBuild(appId: number, input: BuildTriggerInput): Promise<BuildRun> {
  const r = await api.post<BuildRun>(`/apps/${appId}/build`, input)
  return r.data
}

export async function getBuildLog(id: number): Promise<string> {
  const r = await api.get<string>(`/builds/${id}/log`, {
    responseType: 'text',
    transformResponse: (x) => x, // 避免 axios 把 text 当 JSON 解析
  })
  return r.data
}
