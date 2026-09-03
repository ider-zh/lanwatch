import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, TimelineDevice, Session } from '../api'

type RangeKey = '6h' | '24h' | '7d'
const RANGES: Record<RangeKey, number> = {
  '6h': 6 * 3600,
  '24h': 24 * 3600,
  '7d': 7 * 24 * 3600,
}

type SortKey = 'name' | 'recent' | 'duration'
type FilterKey = 'all' | 'online'

function label(d: TimelineDevice): string {
  return d.name || d.hostname || d.mac
}

interface Tick {
  pct: number
  label: string
}

// 根据时间范围挑选"好看"的刻度步长，并生成刻度标签（与绘图区按百分比对齐）。
function buildTicks(from: number, to: number, rangeKey: RangeKey): { ticks: Tick[]; stepPct: number } {
  let step: number
  if (rangeKey === '6h') step = 3600
  else if (rangeKey === '24h') step = 3 * 3600
  else step = 24 * 3600
  const start = Math.ceil(from / step) * step
  const ticks: Tick[] = []
  for (let t = start; t <= to; t += step) {
    const d = new Date(t * 1000)
    const label =
      rangeKey === '7d'
        ? `${d.getMonth() + 1}/${d.getDate()}`
        : `${String(d.getHours()).padStart(2, '0')}:00`
    ticks.push({ pct: ((t - from) / (to - from)) * 100, label })
  }
  const stepPct = (step / (to - from)) * 100
  return { ticks, stepPct }
}

function fmtDur(sec: number): string {
  if (sec < 60) return `${sec}秒`
  const m = Math.floor(sec / 60)
  if (m < 60) return `${m}分`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem ? `${h}时${rem}分` : `${h}时`
}

function fmtTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString()
}

// 单个会话条带（按百分比绝对定位到绘图区）
function Bar({
  s,
  from,
  to,
  now,
  onEnter,
  onLeave,
}: {
  s: Session
  from: number
  to: number
  now: number
  onEnter: (s: Session, left: number, width: number) => void
  onLeave: () => void
}) {
  const range = to - from
  if (range <= 0) return null
  const start = Math.max(s.start, from)
  const end = s.end == null ? to : Math.min(s.end, to)
  const left = ((start - from) / range) * 100
  const width = Math.max(((end - start) / range) * 100, 0.4)
  const ongoing = s.end == null
  const dur = (s.end == null ? now : s.end) - s.start
  return (
    <div
      className={`absolute top-1/2 -translate-y-1/2 h-3.5 rounded cursor-pointer ${
        ongoing ? 'bg-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.7)]' : 'bg-sky-500/90'
      } ${ongoing ? 'animate-pulse' : ''}`}
      style={{ left: `${left}%`, width: `${width}%` }}
      onMouseEnter={() => onEnter(s, left, width)}
      onMouseLeave={onLeave}
    >
      {/* 细微的时长文字（条带够宽才显示） */}
      {width > 8 && (
        <span className="pointer-events-none absolute inset-0 flex items-center justify-center text-[10px] text-white/90 font-medium">
          {fmtDur(dur)}
        </span>
      )}
    </div>
  )
}

export default function Timeline() {
  const [rangeKey, setRangeKey] = useState<RangeKey>('24h')
  const [sortKey, setSortKey] = useState<SortKey>('recent')
  const [filterKey, setFilterKey] = useState<FilterKey>('all')
  const [search, setSearch] = useState('')
  const [hover, setHover] = useState<{ s: Session; left: number; width: number; mac: string } | null>(null)

  const now = Math.floor(Date.now() / 1000)
  const to = now
  const from = now - RANGES[rangeKey]

  const { data, isLoading, error } = useQuery({
    queryKey: ['timeline', rangeKey],
    queryFn: () => api.timeline(from, to),
    refetchInterval: rangeKey === '7d' ? 60000 : 30000,
  })

  const { ticks, stepPct } = useMemo(() => buildTicks(from, to, rangeKey), [from, to, rangeKey])

  // 每个设备的派生指标 + 过滤 + 排序
  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    const list = (data?.devices ?? [])
      .map((d) => {
        let total = 0
        let lastEnd = 0
        for (const s of d.sessions) {
          const start = Math.max(s.start, from)
          const end = s.end == null ? to : Math.min(s.end, to)
          total += Math.max(0, end - start)
          lastEnd = Math.max(lastEnd, s.end == null ? now : s.end)
        }
        return { d, total, lastEnd }
      })
      .filter(({ d }) => {
        if (filterKey === 'online' && !d.online) return false
        if (q) {
          const hay = `${d.name} ${d.hostname} ${d.mac} ${d.ip}`.toLowerCase()
          if (!hay.includes(q)) return false
        }
        return true
      })
    list.sort((a, b) => {
      if (sortKey === 'name') return label(a.d).localeCompare(label(b.d))
      if (sortKey === 'duration') return b.total - a.total
      // recent: 在线优先，再按最近上线时间
      if (a.d.online !== b.d.online) return a.d.online ? -1 : 1
      return b.lastEnd - a.lastEnd
    })
    return list
  }, [data, from, to, now, sortKey, filterKey, search])

  const gridBg = `repeating-linear-gradient(to right, rgba(148,163,184,0.10) 0 1px, transparent 1px ${stepPct}%)`

  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <h2 className="text-lg font-semibold">在线区间时间轴</h2>
        <div className="flex gap-1">
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

        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索设备 / MAC / IP"
          className="ml-auto px-3 py-1 rounded bg-slate-800 text-sm text-slate-100 placeholder:text-slate-500 outline-none focus:ring-1 focus:ring-sky-500 w-52"
        />
        <select
          value={filterKey}
          onChange={(e) => setFilterKey(e.target.value as FilterKey)}
          className="px-2 py-1 rounded bg-slate-800 text-sm text-slate-200"
        >
          <option value="all">全部设备</option>
          <option value="online">仅在线</option>
        </select>
        <select
          value={sortKey}
          onChange={(e) => setSortKey(e.target.value as SortKey)}
          className="px-2 py-1 rounded bg-slate-800 text-sm text-slate-200"
        >
          <option value="recent">按最近上线</option>
          <option value="duration">按在线时长</option>
          <option value="name">按名称</option>
        </select>
      </div>

      {isLoading && <div className="text-slate-400">加载中…</div>}
      {error && <div className="text-rose-400">加载失败：{(error as Error).message}</div>}

      {!isLoading && !error && (
        <div className="rounded-lg border border-slate-800 overflow-hidden">
          {/* 时间轴刻度行 */}
          <div className="grid grid-cols-[240px_1fr] border-b border-slate-800 bg-slate-900/60">
            <div className="px-3 py-1 text-xs text-slate-500">设备（{rows.length}）</div>
            <div className="relative h-6">
              {ticks.map((t, i) => (
                <div
                  key={i}
                  className="absolute top-0 -translate-x-1/2 text-[11px] text-slate-500"
                  style={{ left: `${t.pct}%` }}
                >
                  {t.label}
                </div>
              ))}
              <div className="absolute top-0 right-0 text-[11px] text-slate-400 pr-1">现在</div>
            </div>
          </div>

          {/* 设备行 */}
          <div className="max-h-[68vh] overflow-y-auto">
            {rows.map(({ d, total }) => (
              <div
                key={d.mac}
                className="grid grid-cols-[240px_1fr] border-b border-slate-800 last:border-0 hover:bg-slate-900/40"
              >
                {/* 左侧设备信息 */}
                <div className="px-3 py-2 min-w-0">
                  <div className="flex items-center gap-2">
                    <span
                      className={`inline-block w-2 h-2 rounded-full shrink-0 ${
                        d.online ? 'bg-emerald-400' : 'bg-slate-600'
                      }`}
                    />
                    <span className="text-sm text-slate-100 truncate">{label(d)}</span>
                  </div>
                  <div className="text-xs text-slate-500 font-mono truncate">{d.mac}</div>
                  <div className="text-xs text-slate-400 font-mono truncate">
                    {d.ip || '—'} · 区间在线 {fmtDur(total)}
                  </div>
                </div>

                {/* 右侧绘图区 */}
                <div className="relative h-12" style={{ backgroundImage: gridBg }}>
                  {/* “现在”参考线 */}
                  <div className="absolute top-0 bottom-0 right-0 w-px bg-slate-500/50" />
                  {(d.sessions ?? []).map((s, i) => (
                    <Bar
                      key={i}
                      s={s}
                      from={from}
                      to={to}
                      now={now}
                      onEnter={(ss, l, w) => setHover({ s: ss, left: l, width: w, mac: d.mac })}
                      onLeave={() => setHover(null)}
                    />
                  ))}

                  {/* 悬浮提示 */}
                  {hover?.mac === d.mac && (
                    <div
                      className="absolute z-10 -translate-x-full bottom-1 mb-1 px-2 py-1 rounded bg-slate-800 border border-slate-600 text-[11px] text-slate-200 shadow-lg whitespace-nowrap"
                      style={{ left: `min(${hover.left + hover.width}%, 96%)` }}
                    >
                      {fmtTime(hover.s.start)} 起
                      <br />
                      {hover.s.end == null ? '至今（在线中）' : `${fmtTime(hover.s.end)} 结束`}
                      <br />
                      时长 {fmtDur((hover.s.end == null ? now : hover.s.end) - hover.s.start)}
                    </div>
                  )}
                </div>
              </div>
            ))}

            {rows.length === 0 && (
              <div className="p-6 text-center text-slate-500">没有匹配的设备</div>
            )}
          </div>
        </div>
      )}

      {/* 图例 */}
      <div className="flex items-center gap-4 mt-3 text-xs text-slate-400">
        <span className="flex items-center gap-1">
          <span className="inline-block w-3 h-3 rounded bg-sky-500/90" /> 历史在线区间
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block w-3 h-3 rounded bg-emerald-500" /> 进行中（当前在线）
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block w-3 h-3 rounded bg-slate-600" /> 离线
        </span>
        <span className="ml-auto">区间在线时长按所选时间范围统计</span>
      </div>
    </div>
  )
}
