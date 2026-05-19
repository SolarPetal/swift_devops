import axios, { AxiosError } from 'axios'
import type { ApiError } from '../types'

export const api = axios.create({
  baseURL: '/api/v1',
  timeout: 30_000,
})

// 注入 token
api.interceptors.request.use((cfg) => {
  const tk = localStorage.getItem('token')
  if (tk) cfg.headers.Authorization = `Bearer ${tk}`
  return cfg
})

// 401 自动退出
api.interceptors.response.use(
  (r) => r,
  (err: AxiosError) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('token')
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login'
      }
    }
    return Promise.reject(err)
  },
)

// formatError 把 axios 错误转成人话
export function formatError(err: unknown): string {
  if (err instanceof AxiosError) {
    const body = err.response?.data as ApiError | undefined
    return body?.error?.message ?? err.message
  }
  return String(err)
}
