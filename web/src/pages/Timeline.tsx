import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, TimelineDevice, Session } from '../api'

type RangeKey = '6h' | '24h' | '7d'
const RANGES: Record<RangeKey, number> = {
  '6h': 6 * 3600,
  '24h': 24 * 3600,
  '7d': 7 * 24 * 3600,
}

function label(d: TimelineDevice): string {
  return d.name || d.hostname || d.mac
}

// 把会话区间渲染为绝对定位的条带
function Bar({ s, from, to }: { s: Session; from: number; to: number }) {
  const range = to - from
  if (range <= 0) return null
  const start = Math.max(s.start, from)
  const end = s.end == null ? to : Math.min(s.end, to)
  const left = ((start - from) / range) * 100
  const width = Math.max(((end - start) / range) * 100, 0.3)
  const ongoing = s.end == null
  return (
    <div
      className={`absolute top-1 h-4 rounded ${
        ongoing ? 'bg-emerald-500 animate-pulse' : 'bg-sky-500'
      }`}
      style={{ left: `${left}%`, width: `${width}%` }}
      title={`${new Date(start * 1000).toLocaleString()} → ${
        ongoing ? '现在' : new Date(end * 1000).toLocaleString()
      }`}
    />
  )
}

export default function Timeline() {
  const [rangeKey, setRangeKey] = useState<RangeKey>('24h')
  const now = Math.floor(Date.now() / 1000)
  const to = now
  const from = now - RANGES[rangeKey]

  const { data, isLoading, error } = useQuery({
    queryKey: ['timeline', rangeKey],
    queryFn: () => api.timeline(from, to),
    refetchInterval: rangeKey === '7d' ? 60000 : 30000,
  })

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-lg font-semibold">在线区间时间轴</h2>
        <div className="flex gap-1 ml-2">
          {(['6h', '24h', '7d'] as RangeKey[]).map((k) => (
            <button
              key={k}
              onClick={() => setRangeKey(k)}
              className={`px-3 py-1 rounded text-sm ${
                rangeKey === k ? 'bg-sky-600 text-white' : 'bg-slate-800 text-slate-300'
              }`}
            >
              {k === '6h' ? '近 6 小时' : k === '24h' ? '近 24 小时' : '近 7 天'}
            </button>
          ))}
        </div>
      </div>

      {isLoading && <div className="text-slate-400">加载中…</div>}
      {error && <div className="text-rose-400">加载失败：{(error as Error).message}</div>}

      <div className="rounded-lg border border-slate-800 overflow-hidden">
        {data?.devices.map((d) => (
          <div
            key={d.mac}
            className="flex items-stretch border-b border-slate-800 last:border-0 hover:bg-slate-900/40"
          >
            <div className="w-56 shrink-0 px-3 py-2 text-sm">
              <div className="text-slate-100 truncate">{label(d)}</div>
              <div className="text-xs text-slate-500 font-mono truncate">{d.mac}</div>
              {d.ip && <div className="text-xs text-slate-400 font-mono truncate">{d.ip}</div>}
            </div>
            <div className="relative flex-1 h-9 bg-slate-950/40">
              {(d.sessions ?? []).map((s, i) => (
                <Bar key={i} s={s} from={from} to={to} />
              ))}
            </div>
          </div>
        ))}
        {(data?.devices ?? []).length === 0 && (
          <div className="p-6 text-center text-slate-500">暂无设备</div>
        )}
      </div>
    </div>
  )
}
