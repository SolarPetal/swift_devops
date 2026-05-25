import { useEffect } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { Spin } from 'antd'

import { useAuth } from './store/auth'
import Login from './pages/Login'
import MainLayout from './pages/MainLayout'
import HostList from './pages/HostList'
import AppList from './pages/AppList'
import AppDetail from './pages/AppDetail'
import GitCredList from './pages/GitCredList'
import BuilderEnvPage from './pages/BuilderEnvPage'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) {
    return (
      <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>
        <Spin size="large" />
      </div>
    )
  }
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  const refresh = useAuth((s) => s.refresh)
  useEffect(() => { refresh() }, [refresh])

  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<RequireAuth><MainLayout /></RequireAuth>}>
        <Route index element={<Navigate to="/hosts" replace />} />
        <Route path="hosts" element={<HostList />} />
        <Route path="apps" element={<AppList />} />
        <Route path="apps/:id" element={<AppDetail />} />
        <Route path="git-creds" element={<GitCredList />} />
        <Route path="builder-env" element={<BuilderEnvPage />} />
        {/* 未来：artifacts / pipelines / monitor */}
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
