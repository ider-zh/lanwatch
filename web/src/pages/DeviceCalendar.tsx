import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, CalendarDay, CalendarSegment } from '../api'
import RefreshCountdown from '../components/RefreshCountdown'

const colors = { online: '#34d399', offline: '#1e293b', merged: '#a7f3d0', unknown: 'url(#calendar-unknown)' }
const labels = { online: '在线', offline: '离线', merged: '合并短断线', unknown: '采集未知' }
const fieldClass = 'bg-slate-800 border border-slate-700 rounded px-3 py-2 text-sm'
const dateLabel = (timestamp: number) => new Date((timestamp + 8 * 3600) * 1000).toISOString().slice(0, 10)
const clock = (timestamp: number) => new Date(timestamp * 1000).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
function duration(seconds: number) {
  if (seconds === 0) return '0 分钟'
  if (seconds < 60) return `${seconds} 秒`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds % 3600 / 60)} 分`
}
function daySummary(day: CalendarDay) {
  const periods = day.segments.filter(segment => segment.kind === 'online').map(segment => `${clock(segment.start)}–${segment.end === day.end ? '24:00:00' : clock(segment.end)}`).join('、')
  return `${day.date}｜记录在线 ${duration(day.online_seconds)}｜平滑后 ${duration(day.smoothed_seconds)}｜最长连续 ${duration(day.longest_seconds)}｜未知 ${duration(day.unknown_seconds)}｜合并 ${day.merged_gaps} 段｜在线区间 ${periods || '无'}`
}

export default function DeviceCalendar() {
  const [params, setParams] = useSearchParams()
  const [days, setDays] = useState(7)
  const [smooth, setSmooth] = useState(300)
  const [endDate, setEndDate] = useState('')
  const [mode, setMode] = useState<'periods' | 'duration'>('periods')
  const [selectedDate, setSelectedDate] = useState('')
  const devicesQuery = useQuery({ queryKey: ['devices', 'watched'], queryFn: () => api.devices({ watched: 'true' }), refetchInterval: 30000, refetchOnWindowFocus: true })
  const devices = devicesQuery.data?.devices ?? []
  const requestedMAC = params.get('mac')
  const mac = devices.find(device => device.mac === requestedMAC)?.mac ?? devices[0]?.mac ?? ''
  const query = useQuery({
    queryKey: ['calendar', mac, days, smooth, endDate],
    queryFn: () => api.calendar(mac, days, smooth, endDate),
    enabled: !!mac,
  })
  const data = query.data
  const selected = data?.days.find(day => day.date === selectedDate) ?? data?.days[data.days.length - 1]
  const today = dateLabel(Date.now() / 1000)
  const totals = data?.days.reduce((sum, day) => ({ online: sum.online + day.online_seconds, smoothed: sum.smoothed + day.smoothed_seconds, unknown: sum.unknown + day.unknown_seconds }), { online: 0, smoothed: 0, unknown: 0 })

  return <div className="max-w-7xl mx-auto space-y-5">
    <div className="flex flex-wrap gap-3 items-center justify-between">
      <div><h2 className="text-xl font-semibold">设备日历</h2><p className="text-sm text-slate-400 mt-1">只看你关注的设备 · 按北京时间观察每天的在线规律</p></div>
      {mac && <RefreshCountdown query={query} />}
    </div>
    {devicesQuery.isLoading && <p>正在加载关注设备…</p>}
    {devicesQuery.isError && <p role="alert" className="text-rose-400">设备加载失败：{devicesQuery.error.message} <button onClick={() => { void devicesQuery.refetch() }}>重试</button></p>}
    {!devicesQuery.isLoading && !devicesQuery.isError && devices.length === 0 && <div className="rounded-xl border border-slate-700 bg-slate-900 p-10 text-center">
      <h3 className="text-lg mb-2">先关注你想长期观察的设备</h3>
      <p className="text-slate-400 mb-5">临时设备和随机 MAC 不会自动加入。关注名单独立于固定服务器标记。</p>
      <Link to="/devices" className="inline-block rounded bg-sky-600 px-4 py-2">前往设备列表，命名并关注</Link>
    </div>}
    {mac && <>
      <div className="flex flex-wrap gap-4 items-end p-4 rounded-xl bg-slate-900 border border-slate-800">
        <label className="grid gap-1 text-xs text-slate-400">关注设备<select aria-label="关注设备" className={fieldClass} value={mac} onChange={event => { setParams({ mac: event.target.value }); setSelectedDate('') }}>
          {devices.map(device => <option key={device.mac} value={device.mac}>{device.name || device.hostname || device.mac} · {device.current_ip || device.mac}</option>)}
        </select></label>
        <label className="grid gap-1 text-xs text-slate-400">日期范围<select aria-label="日期范围" className={fieldClass} value={days} onChange={event => setDays(Number(event.target.value))}><option value={7}>最近 7 天</option><option value={30}>最近 30 天</option></select></label>
        <label className="grid gap-1 text-xs text-slate-400">截至日期<input aria-label="截至日期" type="date" max={today} value={endDate || today} className={fieldClass} onChange={event => setEndDate(event.target.value)} /></label>
        {endDate && <button className="text-xs text-sky-400 pb-2" onClick={() => setEndDate('')}>回到今天</button>}
        <label className="grid gap-1 text-xs text-slate-400">合并短暂断线<select aria-label="合并短暂断线" className={fieldClass} value={smooth} onChange={event => setSmooth(Number(event.target.value))}><option value={0}>关闭（记录区间）</option><option value={300}>≤ 5 分钟</option><option value={600}>≤ 10 分钟</option></select></label>
        <Link to="/devices" className="text-sm text-sky-400 pb-2">管理关注</Link>
      </div>
      {query.isLoading && <p>正在加载每日在线记录…</p>}
      {query.isError && <p role="alert" className="text-rose-400">日历加载失败：{query.error.message}</p>}
      {data && totals && <>
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          {[['记录在线时长', duration(totals.online)], [smooth ? '平滑后在线时长' : '未启用平滑', duration(totals.smoothed)], ['采集未知时长', duration(totals.unknown)]].map(([label, value]) => <div key={label} className="rounded-lg border border-slate-800 bg-slate-900 p-4"><div className="text-xs text-slate-400">{label}</div><div className="text-xl mt-1 font-semibold">{value}</div></div>)}
        </div>
        <div className="bg-slate-900 border border-slate-800 rounded-xl p-4">
          <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
            <h3 className="font-semibold">{data.device.name || data.device.hostname || data.device.mac}</h3>
            <div className="flex gap-1">{(['periods', 'duration'] as const).map(value => <button key={value} aria-pressed={mode === value} onClick={() => setMode(value)} className={`rounded px-3 py-1.5 text-sm ${mode === value ? 'bg-sky-600 text-white' : 'bg-slate-800 text-slate-300'}`}>{value === 'periods' ? '在线时段' : '每日总时长'}</button>)}</div>
          </div>
          <div className="flex flex-wrap gap-4 text-xs text-slate-400 mb-3"><span className="text-emerald-400">■ 在线</span><span className="text-emerald-200">▧ 平滑补齐</span><span>□ 离线</span><span>▨ 未知</span><span>虚线边界：今日截至当前</span></div>
          <CalendarChart days={data.days} now={data.now} mode={mode} selected={selected?.date ?? ''} onSelect={setSelectedDate} />
          <p className="text-xs text-slate-400 mt-3">{mode === 'periods' ? '横轴：日期；纵轴：一天中的时刻（00:00–24:00）。点击某一天查看明细。' : '横轴：日期；纵轴：每日在线小时数。浅绿色为平滑补齐的时长。'} 未知时段不计入在线统计，也不会被平滑连接。</p>
        </div>
        {selected && <DayDetails day={selected} now={data.now} />}
        <div className="text-xs text-slate-400 space-y-1">
          <p>平滑仅影响此页面，不修改原始记录、实时状态或通知策略。尚未恢复的离线不会提前合并；短暂上线仍会保留。</p>
          <p>独立区间记录始于 {dateLabel(data.raw_since)} {clock(data.raw_since)}。此前历史可能已合并短断线，无法还原。这里的“记录在线”基于采集判定，并非精确断网时刻。</p>
          <p>采集源异常、服务停机或没有覆盖记录时保守显示未知；当前使用所有采集源均成功的轮次确认覆盖。跨日区间按零点切分，不代表零点发生了上下线。</p>
        </div>
      </>}
    </>}
  </div>
}

function CalendarChart({ days, now, mode, selected, onSelect }: { days: CalendarDay[]; now: number; mode: 'periods' | 'duration'; selected: string; onSelect: (date: string) => void }) {
  const [hovered, setHovered] = useState<string | null>(null)
  const width = Math.max(680, days.length * 38 + 70)
  const left = 52, top = 25, height = 300, step = (width - left - 20) / days.length
  const y = (seconds: number) => top + height - seconds / 86400 * height
  const hoveredDay = days.find(day => day.date === hovered)
  return <>
    <div className="overflow-x-auto">
      <svg role="img" aria-label={mode === 'periods' ? '每日在线时段图' : '每日在线总时长图'} viewBox={`0 0 ${width} 375`} style={{ minWidth: width }} className="w-full">
        <defs><pattern id="calendar-unknown" width="6" height="6" patternUnits="userSpaceOnUse"><rect width="6" height="6" fill="#334155"/><path d="M0 6L6 0" stroke="#64748b" strokeWidth="1"/></pattern></defs>
        {[0, 4, 8, 12, 16, 20, 24].map(hour => <g key={hour}><line x1={left} x2={width-15} y1={y(hour*3600)} y2={y(hour*3600)} stroke="#334155"/><text x={left-8} y={y(hour*3600)+4} textAnchor="end" fill="#94a3b8" fontSize={11}>{mode === 'periods' ? `${String(hour).padStart(2,'0')}:00` : `${hour}h`}</text></g>)}
        {days.map((day, index) => {
          const center = left + step * (index + .5), bar = Math.min(28, step*.65)
          const elapsed = Math.max(0, Math.min(86400, now-day.start))
          return <g key={day.date} role="button" tabIndex={0} aria-label={daySummary(day)} aria-pressed={selected === day.date}
            onClick={() => onSelect(day.date)} onKeyDown={event => { if(event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect(day.date) } }}
            onMouseEnter={() => setHovered(day.date)} onMouseLeave={() => setHovered(null)} onFocus={() => setHovered(day.date)} onBlur={() => setHovered(null)} className="cursor-pointer outline-none">
            <title>{daySummary(day)}</title>
            <rect x={center-step/2+1} y={top} width={step-2} height={height} fill={day.date === selected ? '#0c4a6e' : 'transparent'} opacity={.4}/>
            {mode === 'periods' ? <>
              <rect x={center-bar/2} y={top} width={bar} height={height} fill="#0f172a"/>
              {day.segments.map(segment => <rect key={`${segment.start}-${segment.kind}`} x={center-bar/2} y={y(segment.end-day.start)} width={bar} height={Math.max(.7,(segment.end-segment.start)/86400*height)} fill={colors[segment.kind]} fillOpacity={segment.kind === 'merged' ? .65 : 1}/>) }
            </> : <>
              {day.unknown_seconds >= elapsed && <rect x={center-bar/2} y={y(elapsed)} width={bar} height={elapsed/86400*height} fill="url(#calendar-unknown)" opacity={.5}/>}
              <rect x={center-bar/2} y={y(day.online_seconds)} width={bar} height={day.online_seconds/86400*height} fill={colors.online}/>
              <rect x={center-bar/2} y={y(day.smoothed_seconds)} width={bar} height={(day.smoothed_seconds-day.online_seconds)/86400*height} fill={colors.merged} opacity={.65}/>
            </>}
            {now >= day.start && now < day.end && mode === 'periods' && <line x1={center-bar/2-3} x2={center+bar/2+3} y1={y(elapsed)} y2={y(elapsed)} stroke="#fbbf24" strokeDasharray="3 2"/>}
            <text x={center} y={top+height+20} textAnchor="middle" fill={day.date === selected ? '#7dd3fc' : '#94a3b8'} fontSize={10}>{day.date.slice(5)}</text>
          </g>
        })}
      </svg>
    </div>
    <div className="text-xs text-slate-300 min-h-8" aria-live="polite">{hoveredDay ? daySummary(hoveredDay) : '悬停、触摸或用键盘选择某一天，查看每日统计。'}</div>
  </>
}

function DayDetails({ day, now }: { day: CalendarDay; now: number }) {
  const timeLabel = (at: number) => at === day.end ? '24:00:00' : clock(at)
  const segmentLabel = (segment: CalendarSegment) => {
    if (segment.end === now && segment.kind === 'offline') return '离线 · 等待后续采集确认是否短断线'
    if (segment.end === now && segment.kind === 'online') return '在线 · 截至当前'
    return labels[segment.kind]
  }
  return <section className="rounded-xl border border-slate-800 bg-slate-900 p-4">
    <h3 className="font-semibold mb-2">{day.date} · 每日明细</h3>
    <p className="text-sm text-slate-400 mb-4">记录在线 {duration(day.online_seconds)} · 平滑后 {duration(day.smoothed_seconds)} · 最长连续（平滑后）{duration(day.longest_seconds)} · 合并 {day.merged_gaps} 段</p>
    <div className="overflow-x-auto max-h-80"><table className="w-full min-w-[600px] whitespace-nowrap text-sm text-left [&_th]:pr-4 [&_td]:pr-4"><thead className="text-slate-400"><tr><th className="py-2">状态</th><th>开始（北京时间）</th><th>结束</th><th>时长</th></tr></thead><tbody>
      {day.segments.map(segment => <tr key={segment.start} className="border-t border-slate-800"><td className={`py-2 ${segment.kind === 'online' ? 'text-emerald-400' : segment.kind === 'merged' ? 'text-emerald-200' : 'text-slate-400'}`}>{segmentLabel(segment)}</td><td>{timeLabel(segment.start)}{segment.start === day.start ? '（当日边界）' : ''}</td><td>{timeLabel(segment.end)}{segment.end === now ? '（截至当前）' : ''}</td><td>{duration(segment.end-segment.start)}</td></tr>)}
    </tbody></table></div>
  </section>
}
