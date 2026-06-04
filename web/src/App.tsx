import { lazy, Suspense, useEffect } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { Spin } from 'antd'

import { useAuth } from './store/auth'

const Login = lazy(() => import('./pages/Login'))
const MainLayout = lazy(() => import('./pages/MainLayout'))
const OverviewPage = lazy(() => import('./pages/OverviewPage'))
const HostList = lazy(() => import('./pages/HostList'))
const AppList = lazy(() => import('./pages/AppList'))
const AppDetail = lazy(() => import('./pages/AppDetail'))
const GitCredList = lazy(() => import('./pages/GitCredList'))
const BuilderEnvPage = lazy(() => import('./pages/BuilderEnvPage'))

function PageFallback() {
  return (
    <div className="app-loading">
      <Spin size="large" />
    </div>
  )
}

function LazyPage({ children }: { children: React.ReactNode }) {
  return <Suspense fallback={<PageFallback />}>{children}</Suspense>
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) {
    return <PageFallback />
  }
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  const refresh = useAuth((s) => s.refresh)
  useEffect(() => { refresh() }, [refresh])

  return (
    <Routes>
      <Route path="/login" element={<LazyPage><Login /></LazyPage>} />
      <Route path="/" element={<RequireAuth><LazyPage><MainLayout /></LazyPage></RequireAuth>}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={<LazyPage><OverviewPage /></LazyPage>} />
        <Route path="apps" element={<LazyPage><AppList /></LazyPage>} />
        <Route path="apps/:id" element={<LazyPage><AppDetail /></LazyPage>} />
        <Route path="hosts" element={<LazyPage><HostList /></LazyPage>} />
        <Route path="credentials" element={<LazyPage><GitCredList /></LazyPage>} />
        <Route path="git-creds" element={<Navigate to="/credentials" replace />} />
        <Route path="settings" element={<Navigate to="/settings/build-env" replace />} />
        <Route path="settings/build-env" element={<LazyPage><BuilderEnvPage /></LazyPage>} />
        <Route path="builder-env" element={<Navigate to="/settings/build-env" replace />} />
        {/* 未来：artifacts / pipelines / monitor */}
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
