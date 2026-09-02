import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, NotifyLog, EVENT_CN } from '../api'

function fmt(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

export default function Notifications() {
  const qc = useQueryClient()
  const [offset, setOffset] = useState(0)
  const [errorsOnly, setErrorsOnly] = useState(false)
  const limit = 50

  const { data, isLoading } = useQuery({
    queryKey: ['notifications', offset, errorsOnly],
    queryFn: () => api.notifications({ limit, offset, errors: errorsOnly }),
  })
  const retryMut = useMutation({
    mutationFn: (id: number) => api.retryNotification(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  })

  const logs: NotifyLog[] = data?.logs ?? []

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">通知日志</h2>
        <label className="text-sm text-slate-300 flex items-center gap-1">
          <input type="checkbox" checked={errorsOnly} onChange={(e) => { setErrorsOnly(e.target.checked); setOffset(0) }} />
          仅失败
        </label>
      </div>

      {isLoading && <div className="text-slate-400">加载中…</div>}

      <div className="overflow-x-auto rounded-lg border border-slate-800">
        <table className="w-full text-sm">
          <thead className="bg-slate-900 text-slate-400">
            <tr>
              <th className="text-left px-3 py-2">时间</th>
              <th className="text-left px-3 py-2">事件</th>
              <th className="text-left px-3 py-2">MAC</th>
              <th className="text-left px-3 py-2">渠道</th>
              <th className="text-left px-3 py-2">状态</th>
              <th className="text-left px-3 py-2">重试</th>
              <th className="text-left px-3 py-2">错误</th>
              <th className="text-left px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            {logs.map((l) => (
              <tr key={l.id} className="border-t border-slate-800 hover:bg-slate-900/50">
                <td className="px-3 py-2 text-slate-400 whitespace-nowrap">{fmt(l.created_at)}</td>
                <td className="px-3 py-2 text-slate-200">{EVENT_CN[l.event] || l.event}</td>
                <td className="px-3 py-2 font-mono text-slate-400">{l.mac || '-'}</td>
                <td className="px-3 py-2 text-slate-300">{l.channel_name || `#${l.channel_id}`}</td>
                <td className="px-3 py-2">
                  <span className={`text-xs px-2 py-0.5 rounded ${l.status === 'sent' ? 'bg-emerald-900 text-emerald-300' : l.status === 'failed' ? 'bg-rose-900 text-rose-300' : 'bg-slate-700 text-slate-300'}`}>
                    {l.status}
                  </span>
                </td>
                <td className="px-3 py-2 text-slate-400">{l.attempts}</td>
                <td className="px-3 py-2 text-rose-300 max-w-xs truncate" title={l.error}>{l.error || '-'}</td>
                <td className="px-3 py-2">
                  {l.status === 'failed' && (
                    <button
                      className="text-xs px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 disabled:opacity-50"
                      disabled={retryMut.isPending}
                      onClick={() => retryMut.mutate(l.id)}
                    >
                      重试
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="flex items-center gap-3 mt-3 text-sm">
        <button className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 disabled:opacity-50" disabled={offset === 0} onClick={() => setOffset((o) => Math.max(0, o - limit))}>
          上一页
        </button>
        <span className="text-slate-400">第 {offset / limit + 1} 页</span>
        <button className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 disabled:opacity-50" disabled={logs.length < limit} onClick={() => setOffset((o) => o + limit)}>
          下一页
        </button>
      </div>
    </div>
  )
}
