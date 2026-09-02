import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, Device, Channel, EVENT_TYPES, EVENT_CN } from '../api'

function fmt(ts: number): string {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

function pct(r: number): string {
  return `${Math.round(r * 100)}%`
}

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
}

export default function Devices() {
  const [q, setQ] = useState('')
  const [onlineOnly, setOnlineOnly] = useState(false)
  const [subMac, setSubMac] = useState<string | null>(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['devices', q, onlineOnly],
    queryFn: () =>
      api.devices({
        q,
        online: onlineOnly ? 'true' : '',
      }),
  })
  const { data: cd } = useQuery({ queryKey: ['channels'], queryFn: api.channels })

  const devices: Device[] = data?.devices ?? []
  const channels: Channel[] = cd?.channels ?? []

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h2 className="text-lg font-semibold">设备</h2>
        <input
          placeholder="搜索名称/MAC/IP…"
          className="px-3 py-1.5 rounded bg-slate-800 text-white outline-none focus:ring-2 focus:ring-sky-500"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <label className="text-sm text-slate-300 flex items-center gap-1">
          <input
            type="checkbox"
            checked={onlineOnly}
            onChange={(e) => setOnlineOnly(e.target.checked)}
          />
          仅在线
        </label>
        <span className="text-sm text-slate-500">共 {devices.length} 台</span>
      </div>

      {isLoading && <div className="text-slate-400">加载中…</div>}
      {error && <div className="text-rose-400">加载失败：{(error as Error).message}</div>}

      <div className="overflow-x-auto rounded-lg border border-slate-800">
        <table className="w-full text-sm">
          <thead className="bg-slate-900 text-slate-400">
            <tr>
              <th className="text-left px-3 py-2">状态</th>
              <th className="text-left px-3 py-2">名称</th>
              <th className="text-left px-3 py-2">MAC</th>
              <th className="text-left px-3 py-2">IP</th>
              <th className="text-left px-3 py-2">厂商</th>
              <th className="text-left px-3 py-2">最近上线</th>
              <th className="text-left px-3 py-2">24h 在线率</th>
              <th className="text-left px-3 py-2">标记</th>
              <th className="text-left px-3 py-2">订阅</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => (
              <tr key={d.mac} className="border-t border-slate-800 hover:bg-slate-900/50">
                <td className="px-3 py-2">
                  <span
                    className={`inline-block w-2.5 h-2.5 rounded-full ${
                      d.online ? 'bg-emerald-400' : 'bg-slate-600'
                    }`}
                    title={d.online ? '在线' : '离线'}
                  />
                </td>
                <td className="px-3 py-2 text-slate-100">{d.name || d.hostname || '-'}</td>
                <td className="px-3 py-2 font-mono text-slate-400">{d.mac}</td>
                <td className="px-3 py-2 font-mono text-slate-300">{d.current_ip || '-'}</td>
                <td className="px-3 py-2 text-slate-400">{d.vendor || '-'}</td>
                <td className="px-3 py-2 text-slate-400">{fmt(d.last_seen)}</td>
                <td className="px-3 py-2 text-slate-300">{pct(d.online_ratio_24h)}</td>
                <td className="px-3 py-2 text-slate-500">{d.status}</td>
                <td className="px-3 py-2">
                  <button
                    className="text-xs px-2 py-1 rounded bg-slate-800 hover:bg-slate-700"
                    onClick={() => setSubMac(d.mac)}
                  >
                    {d.channels?.length ? `已订 ${d.channels.length}` : '订阅'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {subMac && (
        <SubscriptionModal
          mac={subMac}
          channels={channels}
          onClose={() => setSubMac(null)}
        />
      )}
    </div>
  )
}

function SubscriptionModal({
  mac,
  channels,
  onClose,
}: {
  mac: string
  channels: Channel[]
  onClose: () => void
}) {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['subscriptions', mac],
    queryFn: () => api.subscriptions(mac),
  })
  const [selChannels, setSelChannels] = useState<number[]>([])
  const [selEvents, setSelEvents] = useState<string[]>(['online', 'offline'])
  const [ready, setReady] = useState(false)

  // 用首次加载的订阅初始化选择
  if (data && !ready) {
    const initCh = data.subscriptions.map((s) => s.channel_id)
    const initEv = data.subscriptions[0]?.events ?? ['online', 'offline']
    setSelChannels(initCh)
    setSelEvents(initEv)
    setReady(true)
  }

  const saveMut = useMutation({
    mutationFn: () => api.putSubscriptions(mac, selChannels, selEvents),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['devices'] })
      qc.invalidateQueries({ queryKey: ['subscriptions', mac] })
      onClose()
    },
  })

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center p-4 z-20">
      <div className="bg-slate-900 rounded-lg border border-slate-700 w-full max-w-lg p-5">
        <h3 className="font-semibold mb-1">设备订阅</h3>
        <p className="text-xs text-slate-500 font-mono mb-3">{mac}</p>
        {isLoading && <div className="text-slate-400">加载中…</div>}
        {channels.length === 0 && <div className="text-slate-500 text-sm">请先创建通知渠道。</div>}
        <div className="space-y-3">
          <div>
            <label className="text-xs text-slate-400">渠道</label>
            <div className="mt-1 flex flex-wrap gap-2">
              {channels.map((c) => (
                <label key={c.id} className="flex items-center gap-1 text-sm text-slate-300">
                  <input
                    type="checkbox"
                    checked={selChannels.includes(c.id)}
                    onChange={() =>
                      setSelChannels((p) => (p.includes(c.id) ? p.filter((x) => x !== c.id) : [...p, c.id]))
                    }
                  />
                  {c.name}
                </label>
              ))}
            </div>
          </div>
          <div>
            <label className="text-xs text-slate-400">事件</label>
            <div className="mt-1 flex flex-wrap gap-2">
              {EVENT_TYPES.map((e) => (
                <label key={e} className="flex items-center gap-1 text-sm text-slate-300">
                  <input
                    type="checkbox"
                    checked={selEvents.includes(e)}
                    onChange={() => setSelEvents((p) => toggle(p, e))}
                  />
                  {EVENT_CN[e]}
                </label>
              ))}
            </div>
          </div>
        </div>
        <div className="flex gap-2 justify-end mt-4">
          <button className="px-3 py-1.5 rounded bg-slate-800 hover:bg-slate-700 text-sm" onClick={onClose}>
            取消
          </button>
          <button
            className="px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-sm disabled:opacity-50"
            disabled={saveMut.isPending}
            onClick={() => saveMut.mutate()}
          >
            {saveMut.isPending ? '保存中…' : '保存'}
          </button>
        </div>
        {saveMut.isError && (
          <div className="text-rose-400 text-sm mt-2">{String((saveMut.error as Error).message)}</div>
        )}
      </div>
    </div>
  )
}

