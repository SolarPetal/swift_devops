import { api } from './client'
import type { BuilderEnv, BuilderEnvInput } from '../types'

export async function getBuilderEnv(): Promise<BuilderEnv> {
  const r = await api.get<BuilderEnv>('/builder-env')
  return r.data
}

export async function updateBuilderEnv(input: BuilderEnvInput): Promise<BuilderEnv> {
  const r = await api.put<BuilderEnv>('/builder-env', input)
  return r.data
}

export async function detectBuilderEnv(): Promise<BuilderEnv> {
  const r = await api.post<BuilderEnv>('/builder-env/detect')
  return r.data
}
