import { api } from './client'

export async function login(username: string, password: string) {
  const r = await api.post('/auth/login', { username, password })
  return r.data as { token: string; username: string; expires: number }
}

export async function getMe() {
  const r = await api.get('/me')
  return r.data as { user: string }
}
