import { api } from './client'
import type { Deployment, DeploymentInput } from '../types'

export async function listDeployments(appId: number) {
  const r = await api.get<{ items: Deployment[] }>(`/apps/${appId}/hosts`)
  return r.data.items
}

export async function bindHost(appId: number, input: DeploymentInput) {
  const r = await api.post<Deployment>(`/apps/${appId}/hosts`, input)
  return r.data
}

export async function unbindDeployment(id: number) {
  await api.delete(`/deployments/${id}`)
}

export async function updateDeploymentGroup(id: number, groupTag: string) {
  await api.patch(`/deployments/${id}`, { group_tag: groupTag })
}
