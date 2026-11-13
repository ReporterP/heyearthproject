import { useCallback, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { fetchHub } from '../../api/admin.js'
import { Button, Card, CardBody, CardHeader, Skeleton } from '../ui/index.js'
import { loadView } from './serversModel.js'
import LoadCharts from './LoadCharts.jsx'
import OwnerForm from './OwnerForm.jsx'
import HubKeyBox from '../entries/HubKeyBox.jsx'

function Metric({ label, v, hint }) {
  return (
    <div className="rounded border border-default p-3 min-w-0">
      <p className="text-xs text-tertiary">{label}</p>
      <p className={`font-mono tabular-nums text-lg mt-1 ${v?.warn ? 'text-warning' : 'text-primary'}`}>{v?.text ?? '—'}</p>
      {hint && <p className="text-[11px] text-tertiary mt-0.5">{hint}</p>}
    </div>
  )
}

/**
 * HubPanel — the «Hub» tab of «Серверы»: the latest sample (the API measures the Hub itself every
 * minute), this month's traffic against the hoster's quota, the owner's numbers and the charts.
 */
export default function HubPanel() {
  const { can } = useAuth()
  const [hub, setHub] = useState(null)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    try {
      setHub(await fetchHub())
      setError('')
    } catch (e) {
      setError(e?.message || 'ошибка')
    }
  }, [])
  useEffect(() => { load() }, [load])

  if (!hub && !error) return <div className="space-y-3"><Skeleton className="h-24" /><Skeleton className="h-48" /></div>
  if (!hub) {
    return (
      <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
        <span className="text-sm text-danger">Не удалось загрузить Hub: {error}</span>
        <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
      </div>
    )
  }
  const v = loadView(hub.load, { portMbps: hub.owner?.port_mbps ?? 1000, role: 'hub' })
  const quota = hub.owner?.traffic_quota_tb
  const month = { text: `${(hub.month_tb ?? 0).toFixed(2)} ТБ`, warn: quota > 0 && hub.month_tb > quota * 0.7 }
  return (
    <div className="space-y-4">
      {v.empty ? (
        <p className="text-sm text-warning">{v.why}</p>
      ) : (
        <>
          {v.why && <p className="text-xs text-warning">{v.why}</p>}
          <div className="grid grid-cols-2 lg:grid-cols-5 gap-3">
            <Metric label="CPU" v={v.cpu} hint={v.cores ? `${v.cores} ядер` : ''} />
            <Metric label="RAM" v={v.ram} />
            <Metric label="Канал, Мбит/с" v={v.net} hint="↓ вход · ↑ выход" />
            <Metric label="Подключения TCP" v={v.conns} />
            <Metric label="Трафик за месяц" v={month} hint={quota > 0 ? `лимит ${quota} ТБ` : 'лимит не указан'} />
          </div>
        </>
      )}
      <Card>
        <CardHeader><h3 className="text-sm font-semibold text-primary">Нагрузка</h3></CardHeader>
        <CardBody><LoadCharts id="hub" /></CardBody>
      </Card>
      <HubKeyBox />
      <Card>
        <CardHeader><h3 className="text-sm font-semibold text-primary">Настройки Hub</h3></CardHeader>
        <CardBody>
          <OwnerForm id="hub" owner={hub.owner} detectedCores={hub.load?.cores} canEdit={can('servers.write')} onSaved={load} />
        </CardBody>
      </Card>
    </div>
  )
}
