import { api } from './client'
import type { App, AppInput } from '../types'

export async function listApps() {
  const r = await api.get<{ items: App[] }>('/apps')
  return r.data.items
}

export async function getApp(id: number) {
  const r = await api.get<App>(`/apps/${id}`)
  return r.data
}

export async function createApp(input: AppInput) {
  const r = await api.post<App>('/apps', input)
  return r.data
}

export async function updateApp(id: number, input: AppInput) {
  const r = await api.put<App>(`/apps/${id}`, input)
  return r.data
}

export async function deleteApp(id: number) {
  await api.delete(`/apps/${id}`)
}

export async function listBranches(appId: number, credId?: number) {
  const params = credId ? { cred_id: credId } : {}
  const r = await api.get<{ branches: string[] }>(`/apps/${appId}/branches`, { params })
  return r.data.branches
}
