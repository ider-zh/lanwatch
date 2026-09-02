export class ApiError extends Error {
  status: number
  constructor(status: number, msg: string) {
    super(msg)
    this.status = status
  }
}

async function req<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  if (res.status === 401) {
    throw new ApiError(401, 'unauthorized')
  }
  if (res.status === 204) {
    return null as T
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new ApiError(res.status, text || res.statusText)
  }
  return (await res.json()) as T
}

export interface User {
  username: string
  role: string
}

export interface Device {
  mac: string
  name: string
  hostname: string
  vendor: string
  current_ip: string
  link_type: string
  ssid: string
  status: string
  first_seen: number
  last_seen: number
  notes: string
  online: boolean
  online_ratio_24h: number
  channels: number[]
}

export interface Session {
  start: number
  end: number | null
  reason?: string
}

export interface TimelineDevice {
  mac: string
  name: string
  hostname: string
  ip: string
  online: boolean
  sessions: Session[]
}

export interface SourceState {
  ID: string
  Kind: string
  Healthy: boolean
  LastOKAt: number
  ConsecutiveFail: number
  LastError: string
  LockedUntil: number
  HostKey: string
  Label: string
}

export interface Stats {
  online_now: number
  new_today: number
  all_healthy: boolean
  sources: SourceState[]
}

// ---- 通知 ----
export type ChannelType = 'email' | 'dingtalk'
export interface Channel {
  id: number
  type: ChannelType
  name: string
  enabled: boolean
  config: Record<string, any>
  created_at: number
}
export interface Rule {
  id: number
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
  created_at: number
}
export interface NotifyLog {
  id: number
  rule_id: number | null
  channel_id: number
  channel_name: string
  channel_type: string
  mac: string | null
  event: string
  status: string
  attempts: number
  error: string
  created_at: number
  sent_at: number | null
  device_name?: string | null
  device_hostname?: string | null
}
export interface Subscription {
  channel_id: number
  events: string[]
}

export const EVENT_TYPES = [
  'online',
  'offline',
  'new_device',
  'ip_changed',
  'hostname_changed',
  'flapping',
] as const
export const EVENT_CN: Record<string, string> = {
  online: '上线',
  offline: '离线',
  new_device: '新设备',
  ip_changed: 'IP 变更',
  hostname_changed: '主机名变更',
  flapping: '抖动',
}

export const api = {
  login: (username: string, password: string) =>
    req<{ user: User }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  me: () => req<{ user: User }>('/api/auth/me'),
  logout: () => req<{}>('/api/auth/logout', { method: 'POST' }),
  devices: (q: Record<string, string> = {}) => {
    const sp = new URLSearchParams()
    Object.entries(q).forEach(([k, v]) => {
      if (v) sp.set(k, v)
    })
    const s = sp.toString()
    return req<{ devices: Device[] }>(`/api/devices${s ? `?${s}` : ''}`)
  },
  timeline: (from: number, to: number) =>
    req<{ from: number; to: number; devices: TimelineDevice[] }>(
      `/api/timeline?from=${from}&to=${to}`,
    ),
  sources: () => req<{ sources: SourceState[] }>('/api/sources'),
  stats: () => req<Stats>('/api/stats/overview'),

  // 渠道
  channels: () => req<{ channels: Channel[] }>('/api/channels'),
  createChannel: (body: Partial<Channel> & { config: Record<string, any> }) =>
    req<{ id: number }>('/api/channels', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateChannel: (id: number, body: Partial<Channel> & { config?: Record<string, any> }) =>
    req<{ ok: boolean }>(`/api/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteChannel: (id: number) =>
    req<{ ok: boolean }>(`/api/channels/${id}`, { method: 'DELETE' }),
  testChannel: (id: number, event: string, mac: string) =>
    req<{ ok: boolean; elapsed_ms?: number; error?: string }>(
      `/api/channels/${id}/test`,
      { method: 'POST', body: JSON.stringify({ event, mac }) },
    ),

  // 策略
  rules: () => req<{ rules: Rule[] }>('/api/rules'),
  createRule: (body: Partial<Rule> & { channel_ids: number[]; events: string[] }) =>
    req<{ id: number }>('/api/rules', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateRule: (id: number, body: Partial<Rule> & { channel_ids: number[]; events: string[] }) =>
    req<{ ok: boolean }>(`/api/rules/${id}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteRule: (id: number) =>
    req<{ ok: boolean }>(`/api/rules/${id}`, { method: 'DELETE' }),

  // 发送日志
  notifications: (opts: { limit?: number; offset?: number; errors?: boolean } = {}) => {
    const sp = new URLSearchParams()
    if (opts.limit) sp.set('limit', String(opts.limit))
    if (opts.offset) sp.set('offset', String(opts.offset))
    if (opts.errors) sp.set('errors', 'true')
    const s = sp.toString()
    return req<{ logs: NotifyLog[] }>(`/api/notifications${s ? `?${s}` : ''}`)
  },
  retryNotification: (id: number) =>
    req<{ ok: boolean; error?: string }>(`/api/notifications/${id}/retry`, {
      method: 'POST',
    }),

  // 设备订阅
  subscriptions: (mac: string) =>
    req<{ mac: string; subscriptions: Subscription[] }>(
      `/api/devices/${encodeURIComponent(mac)}/subscriptions`,
    ),
  putSubscriptions: (mac: string, channels: number[], events: string[]) =>
    req<{ ok: boolean }>(`/api/devices/${encodeURIComponent(mac)}/subscriptions`, {
      method: 'PUT',
      body: JSON.stringify({ channels, events }),
    }),
}

