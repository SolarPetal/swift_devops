import { api } from './client'
import type { GitCredential, GitCredentialInput } from '../types'

export async function listGitCreds(): Promise<GitCredential[]> {
  const r = await api.get<{ items: GitCredential[] }>('/git-creds')
  return r.data.items
}

export async function getGitCred(id: number): Promise<GitCredential> {
  const r = await api.get<GitCredential>(`/git-creds/${id}`)
  return r.data
}

export async function createGitCred(input: GitCredentialInput): Promise<GitCredential> {
  const r = await api.post<GitCredential>('/git-creds', input)
  return r.data
}

export async function updateGitCred(id: number, input: GitCredentialInput): Promise<GitCredential> {
  const r = await api.put<GitCredential>(`/git-creds/${id}`, input)
  return r.data
}

export async function deleteGitCred(id: number): Promise<void> {
  await api.delete(`/git-creds/${id}`)
}
