import { useEffect, useState } from 'react'

interface Props {
  query: {
    isFetching: boolean
    isPaused: boolean
    isError: boolean
    dataUpdatedAt: number
    errorUpdatedAt: number
    refetch: () => Promise<unknown>
  }
  intervalMs?: number
}

export default function RefreshCountdown({ query, intervalMs = 30000 }: Props) {
  const { isFetching, isPaused, isError, dataUpdatedAt, errorUpdatedAt, refetch } = query
  const [visible, setVisible] = useState(() => !document.hidden)
  const [seconds, setSeconds] = useState(Math.ceil(intervalMs / 1000))

  useEffect(() => {
    const onVisibilityChange = () => {
      setVisible(!document.hidden)
      if (!document.hidden) void refetch()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [refetch])

  useEffect(() => {
    setSeconds(Math.ceil(intervalMs / 1000))
    if (!visible || isFetching || isPaused) return
    const deadline = Date.now() + intervalMs
    const timer = window.setInterval(() => {
      const remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
      setSeconds(remaining)
      if (remaining === 0) {
        window.clearInterval(timer)
        void refetch()
      }
    }, 250)
    return () => window.clearInterval(timer)
  }, [visible, isFetching, isPaused, intervalMs, dataUpdatedAt, errorUpdatedAt, refetch])

  const status = isPaused ? '等待网络恢复' : !visible ? '后台暂停刷新' : isFetching
    ? '正在刷新…' : `${isError ? '刷新失败，' : ''}${seconds} 秒后${isError ? '重试' : '刷新'}`

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400" title="页面刷新从服务器读取数据，不会触发路由器采集">
      <span className={`tabular-nums ${isError ? 'text-amber-400' : ''}`}>{status}</span>
      {dataUpdatedAt > 0 && <span>上次更新 {new Date(dataUpdatedAt).toLocaleTimeString()}</span>}
      <button
        type="button"
        className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 disabled:opacity-50"
        disabled={isFetching || isPaused}
        onClick={() => { void refetch() }}
      >
        立即刷新
      </button>
    </div>
  )
}
