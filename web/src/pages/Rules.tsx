import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, Channel, EVENT_TYPES, EVENT_CN } from '../api'

interface FormState {
  id?: number
  name: string
  enabled: boolean
  scope: string
  scope_ref: string
  events: string[]
  min_offline_sec: number
  quiet_hours: string
  cooldown_sec: number
  digest_sec: number
  channel_ids: number[]
}

const emptyForm = (channelId?: number): FormState => ({
  name: '',
  enabled: true,
  scope: 'all',
  scope_ref: '',
  events: ['online', 'offline'],
  min_offline_sec: 0,
  quiet_hours: '',
  cooldown_sec: 300,
  digest_sec: 0,
  channel_ids: channelId ? [channelId] : [],
})

export default function Rules() {
  const qc = useQueryClient()
  const { data: rd } = useQuery({ queryKey: ['rules'], queryFn: api.rules })
  const { data: cd } = useQuery({ queryKey: ['channels'], queryFn: api.channels })
  const [form, setForm] = useState<FormState | null>(null)

  const saveMut = useMutation({
    mutationFn: async (f: FormState) => {
      const body = {
        name: f.name,
        enabled: f.enabled,
        scope: f.scope,
        scope_ref: f.scope_ref,
        events: f.events,
        min_offline_sec: f.min_offline_sec,
        quiet_hours: f.quiet_hours,
        cooldown_sec: f.cooldown_sec,
        digest_sec: f.digest_sec,
        channel_ids: f.channel_ids,
      }
      if (f.id) return api.updateRule(f.id, body)
      return api.createRule(body as any)
    },
    onSuccess: () => {
      setForm(null)
      qc.invalidateQueries({ queryKey: ['rules'] })
    },
  })
  const delMut = useMutation({
    mutationFn: (id: number) => api.deleteRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  })

  const rules = rd?.rules ?? []
  const channels: Channel[] = cd?.channels ?? []

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">通知策略</h2>
        <button
          className="px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-sm"
          onClick={() => setForm(emptyForm())}
        >
          + 新建策略
        </button>
      </div>

      <div className="grid gap-3">
        {rules.map((r) => (
          <div key={r.id} className="rounded-lg border border-slate-800 p-3 flex items-center justify-between">
            <div>
              <div className="flex items-center gap-2">
                <span className={`text-xs px-2 py-0.5 rounded ${r.enabled ? 'bg-emerald-900 text-emerald-300' : 'bg-slate-700 text-slate-400'}`}>
                  {r.enabled ? '启用' : '停用'}
                </span>
                <span className="font-medium text-slate-100">{r.name}</span>
              </div>
              <div className="text-xs text-slate-500 mt-1">
                范围：{r.scope} · 事件：{r.events.map((e) => EVENT_CN[e] || e).join('/') || '-'} · 冷却 {r.cooldown_sec}s
                {r.digest_sec > 0 && ` · 聚合 ${r.digest_sec}s`}
              </div>
              <div className="text-xs text-slate-500">渠道：{r.channel_ids.length} 个</div>
            </div>
            <div className="flex items-center gap-2 text-sm">
              <button className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700" onClick={() => setForm({ ...r })}>
                编辑
              </button>
              <button className="px-2 py-1 rounded bg-rose-900/60 hover:bg-rose-800 text-rose-200" onClick={() => delMut.mutate(r.id)}>
                删除
              </button>
            </div>
          </div>
        ))}
        {rules.length === 0 && <div className="text-slate-500 text-sm">暂无策略。</div>}
      </div>

      {form && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center p-4 z-20">
          <div className="bg-slate-900 rounded-lg border border-slate-700 w-full max-w-lg p-5 max-h-[90vh] overflow-y-auto">
            <h3 className="font-semibold mb-3">{form.id ? '编辑策略' : '新建策略'}</h3>
            <RuleForm
              form={form}
              setForm={setForm}
              channels={channels}
              saving={saveMut.isPending}
              error={saveMut.isError ? String((saveMut.error as Error).message) : ''}
              onSubmit={() => saveMut.mutate(form)}
              onCancel={() => setForm(null)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v]
}

function RuleForm({
  form,
  setForm,
  channels,
  saving,
  error,
  onSubmit,
  onCancel,
}: {
  form: FormState
  setForm: (f: FormState) => void
  channels: Channel[]
  saving: boolean
  error: string
  onSubmit: () => void
  onCancel: () => void
}) {
  const set = (patch: Partial<FormState>) => setForm({ ...form, ...patch })
  const setCh = (id: number) =>
    setForm({ ...form, channel_ids: form.channel_ids.includes(id) ? form.channel_ids.filter((x) => x !== id) : [...form.channel_ids, id] })

  return (
    <div className="space-y-3">
      <div>
        <label className="text-xs text-slate-400">名称</label>
        <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.name} onChange={(e) => set({ name: e.target.value })} />
      </div>

      <div className="flex gap-2">
        <div className="flex-1">
          <label className="text-xs text-slate-400">作用范围</label>
          <select className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.scope} onChange={(e) => set({ scope: e.target.value })}>
            <option value="all">全部设备</option>
            <option value="new_devices">仅新设备</option>
            <option value="devices">指定设备</option>
          </select>
        </div>
        {form.scope === 'devices' && (
          <div className="flex-1">
            <label className="text-xs text-slate-400">MAC 列表（逗号分隔）</label>
            <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white font-mono" value={form.scope_ref} onChange={(e) => set({ scope_ref: e.target.value })} />
          </div>
        )}
      </div>

      <div>
        <label className="text-xs text-slate-400">触发事件</label>
        <div className="mt-1 flex flex-wrap gap-2">
          {EVENT_TYPES.map((e) => (
            <label key={e} className="flex items-center gap-1 text-sm text-slate-300">
              <input type="checkbox" checked={form.events.includes(e)} onChange={() => setForm({ ...form, events: toggle(form.events, e) })} />
              {EVENT_CN[e]}
            </label>
          ))}
        </div>
      </div>

      <div>
        <label className="text-xs text-slate-400">通知渠道</label>
        <div className="mt-1 flex flex-wrap gap-2">
          {channels.length === 0 && <span className="text-xs text-slate-500">先创建渠道</span>}
          {channels.map((c) => (
            <label key={c.id} className="flex items-center gap-1 text-sm text-slate-300">
              <input type="checkbox" checked={form.channel_ids.includes(c.id)} onChange={() => setCh(c.id)} />
              {c.name}
            </label>
          ))}
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex-1">
          <label className="text-xs text-slate-400">冷却秒</label>
          <input type="number" className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.cooldown_sec} onChange={(e) => set({ cooldown_sec: Number(e.target.value) })} />
        </div>
        <div className="flex-1">
          <label className="text-xs text-slate-400">聚合秒（0=不聚合）</label>
          <input type="number" className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.digest_sec} onChange={(e) => set({ digest_sec: Number(e.target.value) })} />
        </div>
        <div className="flex-1">
          <label className="text-xs text-slate-400">最小离线秒</label>
          <input type="number" className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.min_offline_sec} onChange={(e) => set({ min_offline_sec: Number(e.target.value) })} />
        </div>
      </div>

      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
        启用
      </label>

      {error && <div className="text-rose-400 text-sm">{error}</div>}
      <div className="flex gap-2 justify-end">
        <button className="px-3 py-1.5 rounded bg-slate-800 hover:bg-slate-700 text-sm" onClick={onCancel}>
          取消
        </button>
        <button className="px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-sm disabled:opacity-50" disabled={saving} onClick={onSubmit}>
          {saving ? '保存中…' : '保存'}
        </button>
      </div>
    </div>
  )
}
