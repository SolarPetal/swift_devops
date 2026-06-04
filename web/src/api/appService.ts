import { api } from './client'
import type { AppService, AppServiceBatchImportResult, AppServiceInput, AppServiceSuggestion } from '../types'

// Sprint X.1/X.4：AppService（可部署服务层）CRUD —— 后端路由：
//   POST   /apps/:id/services
//   GET    /apps/:id/services
//   GET    /app-services/:id
//   PUT    /app-services/:id
//   DELETE /app-services/:id

export async function listAppServices(appId: number): Promise<AppService[]> {
  const r = await api.get<{ items: AppService[] }>(`/apps/${appId}/services`)
  return r.data.items ?? []
}

export async function createAppService(appId: number, input: AppServiceInput): Promise<AppService> {
  const r = await api.post<AppService>(`/apps/${appId}/services`, input)
  return r.data
}

export async function discoverAppServices(
  appId: number,
  input: { git_ref?: string; cred_id?: number } = {},
): Promise<AppServiceSuggestion[]> {
  const r = await api.post<{ items: AppServiceSuggestion[] }>(`/apps/${appId}/services/discover`, input, {
    timeout: 120_000,
  })
  return r.data.items ?? []
}

export async function batchImportAppServices(
  appId: number,
  items: AppServiceInput[],
  options: { upsert?: boolean; disable_default?: boolean } = {},
): Promise<AppServiceBatchImportResult> {
  const r = await api.post<AppServiceBatchImportResult>(`/apps/${appId}/services/batch`, {
    items,
    upsert: options.upsert ?? true,
    disable_default: options.disable_default ?? true,
  })
  return r.data
}

export async function getAppService(id: number): Promise<AppService> {
  const r = await api.get<AppService>(`/app-services/${id}`)
  return r.data
}

export async function updateAppService(id: number, input: AppServiceInput): Promise<AppService> {
  const r = await api.put<AppService>(`/app-services/${id}`, input)
  return r.data
}

export async function deleteAppService(id: number): Promise<void> {
  await api.delete(`/app-services/${id}`)
}
