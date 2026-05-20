import { api } from './client'
import type { Artifact, ArtifactInput } from '../types'

export async function listArtifacts(appId?: number): Promise<Artifact[]> {
  const r = await api.get<{ items: Artifact[] }>('/artifacts', {
    params: appId ? { app_id: appId } : undefined,
  })
  return r.data.items
}

export async function createArtifact(input: ArtifactInput): Promise<Artifact> {
  const r = await api.post<Artifact>('/artifacts', input)
  return r.data
}

export async function deleteArtifact(id: number): Promise<void> {
  await api.delete(`/artifacts/${id}`)
}
