import { create } from 'zustand'
import { getMe } from '../api/auth'

interface AuthState {
  user: string | null
  loading: boolean // 首次 refresh 进行中
  setUser: (u: string | null) => void
  logout: () => void
  refresh: () => Promise<void>
}

export const useAuth = create<AuthState>((set) => ({
  user: null,
  loading: true,
  setUser: (u) => set({ user: u, loading: false }),
  logout: () => {
    localStorage.removeItem('token')
    set({ user: null })
  },
  refresh: async () => {
    const token = localStorage.getItem('token')
    if (!token) {
      set({ user: null, loading: false })
      return
    }
    try {
      const me = await getMe()
      set({ user: me.user, loading: false })
    } catch {
      localStorage.removeItem('token')
      set({ user: null, loading: false })
    }
  },
}))
