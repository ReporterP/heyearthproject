import { useEffect, useRef, useState } from 'react'
import { useToast } from '../Toast.jsx'
import { fetchOpsFlags, fetchOpsProbes, flagBypass, flagPromote, flagRollback, flagToTest, setOpsUrgent } from '../../api/admin.js'
import { startPolling } from '../../utils/polling.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Card, CardBody, CardHeader, Dialog, EmptyState, Select, Skeleton, Textarea } from '../ui/index.js'
import CanaryCard from './CanaryCard.jsx'
import UrgentCard from './UrgentCard.jsx'
import ClientKindsCard from './ClientKindsCard.jsx'
import { bypassAllowed, canaryView, flagStateLabel } from './flagsModel.js'

const POLL_MS = 20000
const STATE_TONE = { off: 'default', test: 'warning', on: 'success' }

/**
 * FlagsPanel — «Операции → Подписка»: the subscription flags with their state, the running
 * canary, «Срочное обновление», the subscription as each app gets it and the last changes.
 * Props: canWrite (settings.write).
 */
export default function FlagsPanel({ canWrite }) {
  const toast = useToast()
  const [data, setData] = useState(null)
  const [runs, setRuns] = useState([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [bypass, setBypass] = useState(null) // { flag, to, reason }
  const loadRef = useRef(() => {})

  async function load() {
    try {
      const [f, p] = await Promise.all([fetchOpsFlags(), fetchOpsProbes(3).catch(() => ({ runs: [] }))])
      setData(f)
      setRuns(p.runs || [])
      setError('')
    } catch (e) { setError(e.message) }
  }
  loadRef.current = load
  useEffect(() => {
    load()
    return startPolling(loadRef, POLL_MS, () => !document.hidden)
  }, [])

  async function act(name, fn, done) {
    setBusy(name)
    try {
      await fn()
      if (done) toast.success(done)
      await load()
    } catch (e) {
      toast.error(e.message)
    } finally { setBusy('') }
  }

  if (error && !data) {
    return <div role="alert" className="rounded border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">Не удалось загрузить флаги: {error}</div>
  }
  if (!data) return <Skeleton className="h-64 w-full" />

  const active = data.active
  const activeFlag = active && data.flags.find(f => f.key === active.flag)
  const now = Date.now()
  const bypassTargets = bypass ? bypass.flag.states.filter(s => s !== bypass.flag.state) : []

  return (
    <div className="space-y-4">
      {active && (
        <CanaryCard change={active} flag={activeFlag} honesty={data.honesty} nowMs={now} canWrite={canWrite} busy={busy}
          onPromote={() => act('promote', () => flagPromote(active.flag), 'Включено всем, идёт страховочный час')}
          onRollback={() => act('rollback', () => flagRollback(active.flag), 'Откат выполнен')} />
      )}

      <Card>
        <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 justify-between">
          <span className="text-sm font-medium text-primary">Флаги подписки</span>
          <span className="text-xs text-tertiary">
            канарейка: {data.soak_min} мин на тестовом пользователе, затем «Всем» и {data.guard_min} мин страховки
            {data.auto_rollback ? ' с автооткатом' : ' (автооткат выключен)'}
          </span>
        </CardHeader>
        <ul className="divide-y divide-default">
          {data.flags.map(f => {
            const running = active && active.flag === f.key
            return (
              <li key={f.key} className="px-4 py-3 flex flex-col md:flex-row md:items-center gap-2">
                <div className="min-w-0 md:flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm text-primary">{f.label}</span>
                    <Badge variant={STATE_TONE[f.state] || 'default'}>{flagStateLabel(f.state)}</Badge>
                    {!f.canary && <Badge>без канарейки</Badge>}
                    {running && <Badge variant="accent">{canaryView(active, now).phaseLabel}</Badge>}
                  </div>
                  <div className="text-xs text-tertiary mt-0.5">
                    <span className="font-mono">{f.key}</span>
                    {f.changed_by && <> · {f.changed_by}, {formatDate(f.changed_at)}</>}
                    {f.note && <> · {f.note}</>}
                  </div>
                </div>
                {canWrite && f.canary && (
                  <div className="flex flex-wrap gap-2">
                    {f.state !== 'on' && !active && (
                      <Button size="sm" variant="secondary" disabled={!!busy} loading={busy === 'test:' + f.key}
                        onClick={() => act('test:' + f.key, () => flagToTest(f.key, 'on'), 'Включено тестовому пользователю, идёт выдержка')}>На тест</Button>
                    )}
                    {!active && (
                      <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => setBypass({ flag: f, to: f.states.find(s => s !== f.state), reason: '' })}>Без канарейки…</Button>
                    )}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 items-start">
        <UrgentCard until={data.urgent_until} formatTime={formatDate} canWrite={canWrite} busy={busy}
          onSet={(h) => act(h ? 'urgent-on' : 'urgent-off', () => setOpsUrgent(h), h ? `Срочное обновление на ${h} ч` : 'Срочное обновление выключено')} />
        <ClientKindsCard runs={runs} formatTime={formatDate} />
      </div>

      <Card>
        <CardHeader className="text-sm font-medium text-primary">Последние изменения флагов</CardHeader>
        <CardBody className="p-0">
          {data.recent.length === 0 ? <EmptyState className="py-6" title="Изменений ещё не было" description="Здесь появится каждый переход флага: на тест, всем, откат — кто и чем закончилось." /> : (
            <ul className="divide-y divide-default">
              {data.recent.map(c => (
                <li key={c.id} className="px-4 py-2 flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 text-sm">
                  <span className="text-xs text-tertiary font-mono tabular-nums sm:w-32 shrink-0">{formatDate(c.started_at)}</span>
                  <span className="text-primary">{(data.flags.find(f => f.key === c.flag) || {}).label || c.flag}</span>
                  <span className="text-secondary">{flagStateLabel(c.from)} → {flagStateLabel(c.to)}</span>
                  <Badge variant={canaryView(c, now).verdictTone}>{canaryView(c, now).phaseLabel}</Badge>
                  <span className="text-xs text-tertiary sm:ml-auto break-words">{c.by}{c.note ? ` · ${c.note}` : ''}</span>
                </li>
              ))}
            </ul>
          )}
        </CardBody>
      </Card>

      <Dialog open={!!bypass} onClose={() => setBypass(null)} title="Без канарейки" maxWidth="max-w-lg">
        {bypass && (
          <div className="space-y-3 text-sm">
            <p className="text-secondary">«{bypass.flag.label}» перейдёт сразу, минуя выдержку на тестовом пользователе. Страховочный час с автооткатом действует всё равно; причина уйдёт в ленту.</p>
            <label className="block">
              <span className="text-xs text-tertiary">Новое состояние</span>
              <Select className="w-full mt-1" value={bypass.to} onChange={e => setBypass({ ...bypass, to: e.target.value })}>
                {bypassTargets.map(s => <option key={s} value={s}>{flagStateLabel(s)}</option>)}
              </Select>
            </label>
            <label className="block">
              <span className="text-xs text-tertiary">Причина (не короче 10 символов)</span>
              <Textarea className="w-full mt-1" rows={3} value={bypass.reason} onChange={e => setBypass({ ...bypass, reason: e.target.value })} />
            </label>
            <div className="flex flex-col sm:flex-row justify-end gap-2">
              <Button variant="ghost" onClick={() => setBypass(null)}>Отмена</Button>
              <Button variant="danger" disabled={!bypassAllowed(bypass.reason) || !!busy} loading={busy === 'bypass'}
                onClick={() => act('bypass', () => flagBypass(bypass.flag.key, bypass.to, bypass.reason), 'Переключено без канарейки, идёт страховочный час').then(() => setBypass(null))}>
                Переключить без канарейки
              </Button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}
