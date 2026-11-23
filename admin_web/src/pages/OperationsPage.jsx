import { useEffect, useMemo, useRef, useState } from 'react'
import { Play, RefreshCw } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import { fetchOpsOverview, fetchOpsProbes, runOpsProbes } from '../api/admin.js'
import { startPolling } from '../utils/polling.js'
import { formatDate } from '../utils/format.js'
import { Button, Card, CardBody, EmptyState, PageHeader, Skeleton, Tabs, TabPanel } from '../components/ui/index.js'
import PathsStrip from '../components/ops/PathsStrip.jsx'
import AlertCard from '../components/ops/AlertCard.jsx'
import OpsFeed from '../components/ops/OpsFeed.jsx'
import ProbesMatrix from '../components/ops/ProbesMatrix.jsx'
import WatchdogSettings from '../components/ops/WatchdogSettings.jsx'
import FlagsPanel from '../components/ops/FlagsPanel.jsx'
import { buildLine, needsBeforeFetch, probeSummary, ruViewLine, sortAlerts } from '../components/ops/opsModel.js'
import AlertsPage from './AlertsPage.jsx'

const POLL_MS = 15000
const TABS_ID = 'ops-tabs'

function NowTab({ data, view, setView }) {
  const [before, setBefore] = useState({})
  const alerts = useMemo(() => sortAlerts(data.alerts), [data.alerts])
  const openKey = view.alert

  // «что менялось за час до»: asked when an alert's card is opened — once if it answered,
  // again at the next opening if it failed
  useEffect(() => {
    if (!openKey || !needsBeforeFetch(before[openKey])) return
    let cancelled = false
    setBefore(b => ({ ...b, [openKey]: { loading: true, items: [] } }))
    fetchOpsOverview({ before: openKey })
      .then(d => { if (!cancelled) setBefore(b => ({ ...b, [openKey]: { loading: false, items: d.feed || [] } })) })
      .catch(e => { if (!cancelled) setBefore(b => ({ ...b, [openKey]: { loading: false, items: [], error: e.message } })) })
    return () => { cancelled = true }
  }, [openKey])

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-4 items-start">
      <section aria-label="Активные тревоги" className="space-y-3 min-w-0">
        {alerts.length === 0 ? (
          <Card>
            <EmptyState className="py-10" title="Активных тревог нет"
              description="Когда что-то сломается, здесь появится карточка: что случилось, что это значит и что можно сделать." />
          </Card>
        ) : alerts.map(a => (
          <AlertCard key={a.key} alert={a} open={openKey === a.key} before={before[a.key] || null}
            onToggle={() => setView({ alert: openKey === a.key ? '' : a.key })} />
        ))}
      </section>
      <div className="min-w-0">
        <OpsFeed items={data.feed || []} error={data.feed_error || ''} />
      </div>
    </div>
  )
}

function ProbesTab({ canRun, ruAlerts }) {
  const toast = useToast()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)
  const loadRef = useRef(() => {})

  async function load() {
    try {
      setData(await fetchOpsProbes(24))
      setError('')
    } catch (e) { setError(e.message) }
  }
  loadRef.current = load
  useEffect(() => {
    load()
    return startPolling(loadRef, POLL_MS, () => !document.hidden)
  }, [])

  async function run() {
    setStarting(true)
    try {
      await runOpsProbes()
      toast.success('Проверка запущена — результат появится примерно через минуту')
      await load()
    } catch (e) { toast.error(e.message) } finally { setStarting(false) }
  }

  if (!data && !error) return <Skeleton className="h-64 w-full" />
  const runs = data?.runs || []
  const sum = probeSummary(runs, 'outside')
  const last = runs.find(r => r.vantage === 'outside')
  return (
    <div className="space-y-4">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
        <p className="text-sm text-secondary">
          За 24 часа: <span className="font-mono tabular-nums text-primary">{sum.pass}</span> из{' '}
          <span className="font-mono tabular-nums text-primary">{sum.total}</span> прогонов прошли
          {sum.fail > 0 && <>, не прошли <span className="font-mono tabular-nums text-danger">{sum.fail}</span></>}
          {sum.blind > 0 && <>, слепых <span className="font-mono tabular-nums text-warning">{sum.blind}</span></>}.
          {last && <> Последний — {formatDate(last.at)}: {last.summary || last.state}.</>}
        </p>
        {canRun && (
          <Button variant="secondary" icon={<Play className="w-3.5 h-3.5" />} onClick={run}
            loading={starting || !!data?.running} disabled={!!data?.running}>
            {data?.running ? 'Идёт проверка' : 'Запустить сейчас'}
          </Button>
        )}
      </div>
      {error && <p role="alert" className="text-xs text-danger">Не удалось загрузить прогоны: {error}</p>}
      <ProbesMatrix runs={runs} vantage="outside" />
      <Card>
        <CardBody className="space-y-1">
          <h3 className="text-sm font-semibold text-primary">Откуда смотрит проверка</h3>
          <p className="text-sm text-secondary">
            Настоящее ядро запускается на хабе с подпиской тестового пользователя и выходит наружу через выход: путь проверяется из-за рубежа.
            Мобильные сети операторов так не видны — зонда с SIM-картой пока нет.
          </p>
          <p className="text-sm text-secondary">
            Взгляд из домашних сетей РФ на сайт, домен подписки, хаб и входы в ротации (раз в 15 минут): {ruViewLine(ruAlerts)}
          </p>
        </CardBody>
      </Card>
    </div>
  )
}

export default function OperationsPage() {
  const { can } = useAuth()
  const [view, setView] = useUrlState({ tab: 'now', alert: '' })
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const loadRef = useRef(() => {})

  async function load() {
    try {
      setData(await fetchOpsOverview({ feedHours: 24 }))
      setError('')
    } catch (e) { setError(e.message) }
  }
  loadRef.current = load
  useEffect(() => {
    load()
    // no requests while the browser tab is hidden
    return startPolling(loadRef, POLL_MS, () => !document.hidden)
  }, [])

  const canSettings = can('settings.read')
  const tabs = [
    { key: 'now', label: 'Сейчас', count: data?.alerts?.length || undefined },
    { key: 'subscription', label: 'Подписка' },
    { key: 'probes', label: 'Проверки' },
    ...(canSettings ? [{ key: 'settings', label: 'Настройки' }] : []),
  ]
  const tab = tabs.some(t => t.key === view.tab) ? view.tab : 'now'
  const ruAlerts = (data?.alerts || []).filter(a => a.key.endsWith('-unreachable-ru'))
  const watchdog = data?.paths?.find(p => p.id === 'watchdog')
  const subtitle = [
    buildLine(data?.build, formatDate),
    watchdog ? `сторож ${watchdog.state === 'ok' ? '✓' : watchdog.state === 'off' ? 'не настроен' : '!'}` : '',
  ].filter(Boolean).join(' · ')

  return (
    <div className="max-w-7xl space-y-4">
      <PageHeader title="Операции" subtitle={subtitle || 'Состояние путей, активные тревоги и лента изменений.'}
        actions={<Button variant="ghost" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Обновить</Button>} />

      {error && (
        <div role="alert" className="rounded border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
          Не удалось загрузить состояние: {error}
        </div>
      )}
      {!data && !error ? <Skeleton className="h-12 w-full" /> : data && <PathsStrip paths={data.paths || []} />}

      <Tabs id={TABS_ID} label="Раздел пульта" tabs={tabs} value={tab} onChange={(key) => setView({ tab: key, alert: '' })} />
      <TabPanel tabsId={TABS_ID} tabKey={tab}>
        {tab === 'now' && (data ? <NowTab data={data} view={view} setView={setView} /> : <Skeleton className="h-64 w-full" />)}
        {tab === 'subscription' && <FlagsPanel canWrite={can('settings.write')} />}
        {tab === 'probes' && <ProbesTab canRun={can('settings.write')} ruAlerts={ruAlerts} />}
        {tab === 'settings' && canSettings && (
          <div className="space-y-6">
            <WatchdogSettings canWrite={can('settings.write')} />
            <AlertsPage embedded />
          </div>
        )}
      </TabPanel>
    </div>
  )
}
