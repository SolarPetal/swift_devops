import { api } from './client'
import type { Host, HostInput, TestResult } from '../types'

export async function listHosts() {
  const r = await api.get<{ items: Host[] }>('/hosts')
  return r.data.items
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
