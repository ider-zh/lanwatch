import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import RefreshCountdown from '../components/RefreshCountdown'

export default function Overview() {
  const query = useQuery({ queryKey: ['stats'], queryFn: () => api.stats() })
  const { data, isLoading } = query

  if (isLoading) return <div className="text-slate-400">加载中…</div>

  const s = data
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <h2 className="text-lg font-semibold">总览</h2>
        <RefreshCountdown query={query} />
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-6">
        <Card label="当前在线" value={s?.online_now ?? '-'} accent="text-emerald-400" />
        <Card label="今日新设备" value={s?.new_today ?? '-'} accent="text-sky-400" />
        <Card
          label="采集源健康"
          value={s?.all_healthy ? '正常' : '异常'}
          accent={s?.all_healthy ? 'text-emerald-400' : 'text-rose-400'}
        />
        <Card label="源数量" value={s?.sources?.length ?? '-'} accent="text-slate-200" />
      </div>

      <h3 className="text-sm font-semibold text-slate-300 mb-2">采集源</h3>
      <div className="space-y-2">
        {(s?.sources ?? []).map((src) => (
          <div
            key={src.ID}
            className="flex items-center justify-between px-3 py-2 rounded bg-slate-900 border border-slate-800"
          >
            <div>
              <div className="text-slate-100">{src.Label || src.ID}</div>
              <div className="text-xs text-slate-500 font-mono">{src.Kind}</div>
            </div>
            <div className="text-right">
              <span
                className={`inline-block w-2.5 h-2.5 rounded-full ${
                  src.Healthy ? 'bg-emerald-400' : 'bg-rose-400'
                }`}
              />
              <div className="text-xs text-slate-500 mt-1">
                {src.Healthy ? '健康' : `连续失败 ${src.ConsecutiveFail}`}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

function Card({ label, value, accent }: { label: string; value: string | number; accent: string }) {
  return (
    <div className="p-4 rounded-lg bg-slate-900 border border-slate-800">
      <div className="text-xs text-slate-500 mb-1">{label}</div>
      <div className={`text-2xl font-semibold ${accent}`}>{value}</div>
    </div>
  )
}
