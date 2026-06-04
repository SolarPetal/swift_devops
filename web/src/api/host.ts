import { api } from './client'
import type { Host, HostDockerContainer, HostDockerLogs, HostInput, HostMetrics, TestResult } from '../types'

export async function listHosts() {
  const r = await api.get<{ items: Host[] }>('/hosts')
  return r.data.items
}

export async function listHostMetrics() {
  const r = await api.get<{ items: HostMetrics[] }>('/hosts/metrics', {
    timeout: 60_000,
  })
  return r.data.items ?? []
}

export async function getHost(id: number) {
  const r = await api.get<Host>(`/hosts/${id}`)
  return r.data
}

export async function createHost(input: HostInput) {
  const r = await api.post<Host>('/hosts', input)
  return r.data
}

export async function updateHost(id: number, input: HostInput) {
  const r = await api.put<Host>(`/hosts/${id}`, input)
  return r.data
}

export async function deleteHost(id: number) {
  await api.delete(`/hosts/${id}`)
}

export async function testHost(id: number) {
  const r = await api.post<TestResult>(`/hosts/${id}/test`)
  return r.data
}

export async function listHostDockerContainers(id: number): Promise<HostDockerContainer[]> {
  const r = await api.get<{ items: HostDockerContainer[] }>(`/hosts/${id}/docker/containers`, {
    timeout: 45_000,
  })
  return r.data.items ?? []
}

export async function getHostDockerLogs(
  id: number,
  container: string,
  lines = 300,
  timestamps = false,
): Promise<HostDockerLogs> {
  const r = await api.get<HostDockerLogs>(`/hosts/${id}/docker/logs`, {
    params: {
      container,
      lines,
      timestamps: timestamps ? 1 : 0,
    },
    timeout: 45_000,
  })
  return r.data
}
