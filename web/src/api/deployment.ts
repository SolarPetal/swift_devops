import { api } from './client'
import type { Deployment, DeploymentInput, DeploymentRuntimeAction, DeploymentRuntimeLogs } from '../types'

export async function listDeployments(appId: number) {
  const r = await api.get<{ items: Deployment[] }>(`/apps/${appId}/hosts`)
  return r.data.items
}

export async function checkDeploymentRuntime(appId: number) {
  const r = await api.post<{ items: Deployment[] }>(`/apps/${appId}/hosts/runtime-check`, undefined, {
    timeout: 60_000,
  })
  return r.data.items
}

export async function bindHost(appId: number, input: DeploymentInput) {
  const r = await api.post<Deployment>(`/apps/${appId}/hosts`, input)
  return r.data
}

export async function unbindDeployment(id: number) {
  await api.delete(`/deployments/${id}`)
}

export async function getDeploymentRuntimeLogs(
  id: number,
  lines = 300,
  timestamps = false,
): Promise<DeploymentRuntimeLogs> {
  const r = await api.get<DeploymentRuntimeLogs>(`/deployments/${id}/runtime-logs`, {
    params: {
      lines,
      timestamps: timestamps ? 1 : 0,
    },
    timeout: 45_000,
  })
  return r.data
}

export async function stopDeploymentRuntime(id: number): Promise<DeploymentRuntimeAction> {
  const r = await api.post<DeploymentRuntimeAction>(`/deployments/${id}/runtime/stop`, undefined, {
    timeout: 45_000,
  })
  return r.data
}

export async function restartDeploymentRuntime(id: number): Promise<DeploymentRuntimeAction> {
  const r = await api.post<DeploymentRuntimeAction>(`/deployments/${id}/runtime/restart`, undefined, {
    timeout: 45_000,
  })
  return r.data
}
