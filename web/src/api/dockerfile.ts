import { api } from './client'
import type { DockerfileTemplate, DockerfileTemplateInput } from '../types'

export async function listDockerfileTemplates(appId: number): Promise<DockerfileTemplate[]> {
  const r = await api.get<{ items: DockerfileTemplate[] }>(`/apps/${appId}/dockerfiles`)
  return r.data.items ?? []
}

export async function createDockerfileTemplate(appId: number, input: DockerfileTemplateInput): Promise<DockerfileTemplate> {
  const r = await api.post<DockerfileTemplate>(`/apps/${appId}/dockerfiles`, input)
  return r.data
}

export async function updateDockerfileTemplate(id: number, input: DockerfileTemplateInput): Promise<DockerfileTemplate> {
  const r = await api.put<DockerfileTemplate>(`/dockerfiles/${id}`, input)
  return r.data
}

export async function deleteDockerfileTemplate(id: number): Promise<void> {
  await api.delete(`/dockerfiles/${id}`)
}

export async function ensureDefaultDockerfileTemplate(appId: number): Promise<DockerfileTemplate> {
  const r = await api.post<DockerfileTemplate>(`/apps/${appId}/dockerfiles/default`)
  return r.data
}
