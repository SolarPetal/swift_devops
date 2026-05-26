import { api } from './client'
import type { AppService, AppServiceInput } from '../types'

// Sprint X.1/X.4：AppService（微服务层）CRUD —— 后端路由：
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
