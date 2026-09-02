import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, Channel, ChannelType, EVENT_TYPES, EVENT_CN } from '../api'

function cfgSummary(c: Channel): string {
  if (c.type === 'email') {
    const to = Array.isArray(c.config.to) ? c.config.to.join(', ') : '-'
    return `${c.config.host || ''}:${c.config.port || ''} → ${to}`
  }
  if (c.type === 'dingtalk') {
    return c.config.webhook ? String(c.config.webhook).slice(0, 48) + '…' : '-'
  }
  return ''
}

interface FormState {
  id?: number
  type: ChannelType
  name: string
  enabled: boolean
  config: Record<string, any>
}

const emptyForm = (type: ChannelType): FormState => ({
  type,
  name: '',
  enabled: true,
  config:
    type === 'email'
      ? { host: '', port: '465', username: '', password: '', from: '', to: [], cc: [], tls: 'ssl', subject: '设备告警' }
      : { webhook: '', secret: '', msgtype: 'markdown', at_all: false, at_mobiles: [] },
})

export default function Channels() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ['channels'], queryFn: api.channels })
  const [form, setForm] = useState<FormState | null>(null)
  const [test, setTest] = useState<{ id: number; msg: string; ok?: boolean } | null>(null)

  const saveMut = useMutation({
    mutationFn: async (f: FormState) => {
      const body = { type: f.type, name: f.name, enabled: f.enabled, config: f.config }
      if (f.id) return api.updateChannel(f.id, body)
      return api.createChannel(body as any)
    },
    onSuccess: () => {
      setForm(null)
      qc.invalidateQueries({ queryKey: ['channels'] })
    },
  })
  const delMut = useMutation({
    mutationFn: (id: number) => api.deleteChannel(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['channels'] }),
  })
  const testMut = useMutation({
    mutationFn: async ({ id, event, mac }: { id: number; event: string; mac: string }) =>
      api.testChannel(id, event, mac),
    onSuccess: (r) =>
      setTest((t) => (t ? { ...t, msg: r.ok ? `发送成功（${r.elapsed_ms}ms）` : `失败：${r.error}`, ok: r.ok } : t)),
  })

  const edit = (c: Channel) => {
    setTest(null)
    setForm({
      id: c.id,
      type: c.type,
      name: c.name,
      enabled: c.enabled,
      // 已脱敏字段回填为 ***，未改动则服务端保留原值
      config: { ...c.config, password: c.config.password ?? '***', secret: c.config.secret ?? '***' },
    })
  }

  const channels = data?.channels ?? []

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">通知渠道</h2>
        <button
          className="px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-sm"
          onClick={() => {
            setTest(null)
            setForm(emptyForm('email'))
          }}
        >
          + 新建渠道
        </button>
      </div>

      {isLoading && <div className="text-slate-400">加载中…</div>}

      <div className="grid gap-3">
        {channels.map((c) => (
          <div key={c.id} className="rounded-lg border border-slate-800 p-3 flex items-center justify-between">
            <div>
              <div className="flex items-center gap-2">
                <span className={`text-xs px-2 py-0.5 rounded ${c.enabled ? 'bg-emerald-900 text-emerald-300' : 'bg-slate-700 text-slate-400'}`}>
                  {c.enabled ? '启用' : '停用'}
                </span>
                <span className="font-medium text-slate-100">{c.name}</span>
                <span className="text-xs text-slate-500">{c.type}</span>
              </div>
              <div className="text-xs text-slate-500 mt-1 font-mono">{cfgSummary(c)}</div>
            </div>
            <div className="flex items-center gap-2 text-sm">
              <button className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700" onClick={() => { setTest({ id: c.id, msg: '' }); }}>
                测试
              </button>
              <button className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700" onClick={() => edit(c)}>
                编辑
              </button>
              <button
                className="px-2 py-1 rounded bg-rose-900/60 hover:bg-rose-800 text-rose-200"
                onClick={() => delMut.mutate(c.id)}
              >
                删除
              </button>
            </div>
          </div>
        ))}
        {!isLoading && channels.length === 0 && (
          <div className="text-slate-500 text-sm">暂无渠道，点击右上角新建。</div>
        )}
      </div>

      {test && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center p-4 z-20">
          <div className="bg-slate-900 rounded-lg border border-slate-700 w-full max-w-md p-5">
            <h3 className="font-semibold mb-3">测试发送</h3>
            <TestForm
              loading={testMut.isPending}
              onRun={(event, mac) => testMut.mutate({ id: test.id, event, mac })}
              result={test}
            />
            <button className="mt-4 text-sm text-slate-400 hover:text-white" onClick={() => setTest(null)}>
              关闭
            </button>
          </div>
        </div>
      )}

      {form && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center p-4 z-20">
          <div className="bg-slate-900 rounded-lg border border-slate-700 w-full max-w-lg p-5 max-h-[90vh] overflow-y-auto">
            <h3 className="font-semibold mb-3">{form.id ? '编辑渠道' : '新建渠道'}</h3>
            <ChannelForm
              form={form}
              setForm={setForm}
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

function TestForm({
  loading,
  onRun,
  result,
}: {
  loading: boolean
  onRun: (event: string, mac: string) => void
  result: { msg: string; ok?: boolean }
}) {
  const [event, setEvent] = useState('offline')
  const [mac, setMac] = useState('')
  return (
    <div className="space-y-3">
      <div>
        <label className="text-xs text-slate-400">事件</label>
        <select className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={event} onChange={(e) => setEvent(e.target.value)}>
          {EVENT_TYPES.map((e) => (
            <option key={e} value={e}>{EVENT_CN[e]}</option>
          ))}
        </select>
      </div>
      <div>
        <label className="text-xs text-slate-400">MAC（可选，留空用占位）</label>
        <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white font-mono" placeholder="3C:0A:F3:D3:54:43" value={mac} onChange={(e) => setMac(e.target.value)} />
      </div>
      <button className="px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-sm disabled:opacity-50" disabled={loading} onClick={() => onRun(event, mac)}>
        {loading ? '发送中…' : '发送测试'}
      </button>
      {result.msg && (
        <div className={`text-sm ${result.ok ? 'text-emerald-400' : 'text-rose-400'}`}>{result.msg}</div>
      )}
    </div>
  )
}

function ChannelForm({
  form,
  setForm,
  saving,
  error,
  onSubmit,
  onCancel,
}: {
  form: FormState
  setForm: (f: FormState) => void
  saving: boolean
  error: string
  onSubmit: () => void
  onCancel: () => void
}) {
  const set = (patch: Partial<FormState>) => setForm({ ...form, ...patch })
  const setCfg = (k: string, v: any) => setForm({ ...form, config: { ...form.config, [k]: v } })

  return (
    <div className="space-y-3">
      <div className="flex gap-3">
        <div className="flex-1">
          <label className="text-xs text-slate-400">类型</label>
          <select
            className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white"
            value={form.type}
            onChange={(e) => set({ type: e.target.value as ChannelType })}
            disabled={!!form.id}
          >
            <option value="email">邮件</option>
            <option value="dingtalk">钉钉</option>
          </select>
        </div>
        <div className="flex-1">
          <label className="text-xs text-slate-400">名称</label>
          <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.name} onChange={(e) => set({ name: e.target.value })} />
        </div>
      </div>

      {form.type === 'email' && (
        <>
          <div className="flex gap-2">
            <div className="flex-1">
              <label className="text-xs text-slate-400">SMTP 主机</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.host ?? ''} onChange={(e) => setCfg('host', e.target.value)} />
            </div>
            <div className="w-24">
              <label className="text-xs text-slate-400">端口</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.port ?? ''} onChange={(e) => setCfg('port', e.target.value)} />
            </div>
          </div>
          <div className="flex gap-2">
            <div className="flex-1">
              <label className="text-xs text-slate-400">用户名</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.username ?? ''} onChange={(e) => setCfg('username', e.target.value)} />
            </div>
            <div className="flex-1">
              <label className="text-xs text-slate-400">密码</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.password ?? ''} onChange={(e) => setCfg('password', e.target.value)} />
            </div>
          </div>
          <div className="flex gap-2">
            <div className="flex-1">
              <label className="text-xs text-slate-400">发件人</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.from ?? ''} onChange={(e) => setCfg('from', e.target.value)} />
            </div>
            <div className="w-32">
              <label className="text-xs text-slate-400">TLS</label>
              <select className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.tls ?? 'ssl'} onChange={(e) => setCfg('tls', e.target.value)}>
                <option value="ssl">ssl</option>
                <option value="starttls">starttls</option>
                <option value="none">none</option>
              </select>
            </div>
          </div>
          <div>
            <label className="text-xs text-slate-400">收件人（逗号分隔）</label>
            <input
              className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white"
              value={Array.isArray(form.config.to) ? form.config.to.join(',') : ''}
              onChange={(e) => setCfg('to', e.target.value.split(',').map((s) => s.trim()).filter(Boolean))}
            />
          </div>
          <div>
            <label className="text-xs text-slate-400">抄送（逗号分隔，可选）</label>
            <input
              className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white"
              value={Array.isArray(form.config.cc) ? form.config.cc.join(',') : ''}
              onChange={(e) => setCfg('cc', e.target.value.split(',').map((s) => s.trim()).filter(Boolean))}
            />
          </div>
          <div>
            <label className="text-xs text-slate-400">主题模板</label>
            <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.subject ?? ''} onChange={(e) => setCfg('subject', e.target.value)} />
          </div>
        </>
      )}

      {form.type === 'dingtalk' && (
        <>
          <div>
            <label className="text-xs text-slate-400">Webhook 地址</label>
            <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.webhook ?? ''} onChange={(e) => setCfg('webhook', e.target.value)} />
          </div>
          <div className="flex gap-2">
            <div className="flex-1">
              <label className="text-xs text-slate-400">加签 Secret</label>
              <input className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.secret ?? ''} onChange={(e) => setCfg('secret', e.target.value)} />
            </div>
            <div className="w-36">
              <label className="text-xs text-slate-400">消息类型</label>
              <select className="w-full mt-1 px-2 py-1.5 rounded bg-slate-800 text-white" value={form.config.msgtype ?? 'markdown'} onChange={(e) => setCfg('msgtype', e.target.value)}>
                <option value="markdown">markdown</option>
                <option value="text">text</option>
              </select>
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={!!form.config.at_all} onChange={(e) => setCfg('at_all', e.target.checked)} />
            @所有人
          </label>
        </>
      )}

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
      <p className="text-xs text-slate-500">密码/Secret 留为 <code>***</code> 表示沿用原值（仅在编辑且未改动时）。</p>
    </div>
  )
}
