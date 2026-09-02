import { Routes, Route, Navigate, Link, useNavigate } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider, useAuth } from './auth'
import Login from './pages/Login'
import Devices from './pages/Devices'
import Timeline from './pages/Timeline'
import Overview from './pages/Overview'
import Channels from './pages/Channels'
import Rules from './pages/Rules'
import Notifications from './pages/Notifications'

function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) {
    return <div className="p-8 text-slate-400">加载中…</div>
  }
  if (!user) return <Navigate to="/login" replace />
  return children
}

function Layout({ children }: { children: ReactNode }) {
  const { user, logout } = useAuth()
  const nav = useNavigate()
  return (
    <div className="min-h-screen flex flex-col">
      <header className="flex items-center justify-between px-4 py-3 bg-slate-900 border-b border-slate-800">
        <div className="flex items-center gap-4">
          <Link to="/" className="font-semibold text-sky-400">
            lanwatch
          </Link>
          <Link to="/" className="text-sm text-slate-300 hover:text-white">
            总览
          </Link>
          <Link to="/timeline" className="text-sm text-slate-300 hover:text-white">
            时间轴
          </Link>
          <Link to="/devices" className="text-sm text-slate-300 hover:text-white">
            设备
          </Link>
          <Link to="/channels" className="text-sm text-slate-300 hover:text-white">
            渠道
          </Link>
          <Link to="/rules" className="text-sm text-slate-300 hover:text-white">
            策略
          </Link>
          <Link to="/notifications" className="text-sm text-slate-300 hover:text-white">
            通知
          </Link>
        </div>
        <div className="flex items-center gap-3 text-sm">
          <span className="text-slate-400">{user?.username}</span>
          <button
            className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700"
            onClick={async () => {
              await logout()
              nav('/login')
            }}
          >
            退出
          </button>
        </div>
      </header>
      <main className="flex-1 p-4">{children}</main>
    </div>
  )
}

function Guards() {
  const { user, loading } = useAuth()
  if (loading) return <div className="p-8 text-slate-400">加载中…</div>
  return (
    <Routes>
      <Route path="/login" element={user ? <Navigate to="/" replace /> : <Login />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <Layout>
              <Overview />
            </Layout>
          </RequireAuth>
        }
      />
      <Route
        path="/timeline"
        element={
          <RequireAuth>
            <Layout>
              <Timeline />
            </Layout>
          </RequireAuth>
        }
      />
      <Route
        path="/devices"
        element={
          <RequireAuth>
            <Layout>
              <Devices />
            </Layout>
          </RequireAuth>
        }
      />
      <Route
        path="/channels"
        element={
          <RequireAuth>
            <Layout>
              <Channels />
            </Layout>
          </RequireAuth>
        }
      />
      <Route
        path="/rules"
        element={
          <RequireAuth>
            <Layout>
              <Rules />
            </Layout>
          </RequireAuth>
        }
      />
      <Route
        path="/notifications"
        element={
          <RequireAuth>
            <Layout>
              <Notifications />
            </Layout>
          </RequireAuth>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <Guards />
    </AuthProvider>
  )
}
