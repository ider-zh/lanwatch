import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { ApiError } from '../api'

export default function Login() {
  const { login } = useAuth()
  const nav = useNavigate()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      await login(username, password)
      nav('/')
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) {
        setErr('尝试过于频繁，请稍后再试')
      } else {
        setErr('用户名或密码错误')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-950">
      <form onSubmit={submit} className="w-80 p-6 rounded-xl bg-slate-900 border border-slate-800 shadow-lg">
        <h1 className="text-xl font-semibold text-sky-400 mb-4">lanwatch 登录</h1>
        <label className="block text-sm text-slate-400 mb-1">用户名</label>
        <input
          className="w-full mb-3 px-3 py-2 rounded bg-slate-800 text-white outline-none focus:ring-2 focus:ring-sky-500"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <label className="block text-sm text-slate-400 mb-1">密码</label>
        <input
          type="password"
          className="w-full mb-4 px-3 py-2 rounded bg-slate-800 text-white outline-none focus:ring-2 focus:ring-sky-500"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {err && <div className="mb-3 text-sm text-rose-400">{err}</div>}
        <button
          disabled={busy}
          className="w-full py-2 rounded bg-sky-600 hover:bg-sky-500 disabled:opacity-50 text-white font-medium"
        >
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
