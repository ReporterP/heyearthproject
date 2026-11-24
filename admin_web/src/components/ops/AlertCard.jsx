import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ChevronDown, ChevronRight, TriangleAlert, OctagonAlert } from 'lucide-react'
import { Badge, Button, Card, Dialog, Spinner } from '../ui/index.js'
import { useToast } from '../Toast.jsx'
import { createOpsSilence, fetchOpsSnapshot } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { actionLabel, levelLabel, impactLabel } from './opsModel.js'
import { actionKind, actionLink, targetFromKey } from './actionsModel.js'

function since(iso) {
  const sec = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000))
  if (!iso || Number.isNaN(sec)) return ''
  if (sec < 3600) return `${Math.max(1, Math.floor(sec / 60))} мин`
  if (sec < 86400) return `${Math.floor(sec / 3600)} ч`
  return `${Math.floor(sec / 86400)} д`
}

/**
 * AlertCard — an open incident with its explanation: what happened, what it means for users,
 * what usually causes it, what can be done (a button for a console action, a link to another
 * page, else text), «Тишина 1 ч», the A16 snapshot of a critical one, and — when opened — what
 * changed in the hour before it.
 * Props: alert, open, onToggle(), before {loading, error, items} | null, actions (the registry),
 * onAction(id, target), canSilence.
 */
export default function AlertCard({ alert, open, onToggle, before, actions = [], onAction = () => {}, canSilence = false }) {
  const toast = useToast()
  const [silencing, setSilencing] = useState(false)
  const [snap, setSnap] = useState(null) // { loading, error, data }
  async function silence() {
    setSilencing(true)
    try {
      await createOpsSilence(alert.key, 60, 'с карточки тревоги')
      toast.success('Тишина на час: тревога не будет отправляться')
    } catch (e) { toast.error(e.message) } finally { setSilencing(false) }
  }
  async function showSnapshot() {
    setSnap({ loading: true })
    try { setSnap({ data: await fetchOpsSnapshot(alert.key) }) } catch (e) { setSnap({ error: e.message }) }
  }
  const critical = alert.level === 'critical'
  const muted = !!alert.inhibited_by
  const Icon = critical ? OctagonAlert : TriangleAlert
  const panelId = `alert-${alert.key}-more`
  return (
    <Card className={muted ? 'opacity-70' : ''}>
      <div className="px-4 py-3 space-y-2">
        <div className="flex items-start gap-2">
          <Icon aria-hidden="true" className={`w-4 h-4 mt-0.5 shrink-0 ${critical ? 'text-danger' : 'text-warning'}`} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <h3 className="text-sm font-semibold text-primary break-words">{alert.title}</h3>
              <Badge variant={critical ? 'danger' : 'warning'}>{levelLabel(alert.level)}</Badge>
              {alert.flapping && <Badge variant="info">мигает</Badge>}
              {muted && <Badge>следствие</Badge>}
              <span className="text-xs font-mono tabular-nums text-tertiary" title={formatDate(alert.since)}>{since(alert.since)}</span>
            </div>
            {alert.last_details && <p className="mt-1 text-sm text-secondary break-words whitespace-pre-line">{alert.last_details}</p>}
          </div>
        </div>
        {alert.meaning && (
          <dl className="grid grid-cols-1 sm:grid-cols-[7.5rem_1fr] gap-x-3 gap-y-1 text-sm">
            <dt className="text-tertiary">Что это значит</dt>
            <dd className="text-primary">{alert.meaning}</dd>
            {alert.usual && alert.usual !== '—' && (<>
              <dt className="text-tertiary">Обычно причина</dt>
              <dd className="text-secondary">{alert.usual}</dd>
            </>)}
            {muted && (<>
              <dt className="text-tertiary">Следствие</dt>
              <dd className="text-secondary">объясняется тревогой <span className="font-mono text-xs">{alert.inhibited_by}</span> — отдельное сообщение не отправлялось</dd>
            </>)}
            {alert.actions?.length > 0 && (<>
              <dt className="text-tertiary">Что можно сделать</dt>
              <dd className="text-secondary">
                <ul className="flex flex-wrap gap-2">
                  {alert.actions.map(a => {
                    const kind = actionKind(a, actions)
                    if (kind === 'button') {
                      const reg = actions.find(x => x.id === a)
                      const target = reg?.target ? targetFromKey(alert.key) : ''
                      return (
                        <li key={a}>
                          <Button size="sm" variant="secondary" disabled={!reg.allowed || (reg.target && !target)}
                            title={reg.allowed ? '' : `Не хватает права ${reg.perm}`} onClick={() => onAction(a, target)}>
                            {actionLabel(a)}
                          </Button>
                        </li>
                      )
                    }
                    if (kind === 'link') return <li key={a}><Link to={actionLink(a)} className="text-accent hover:underline">{actionLabel(a)}</Link></li>
                    return <li key={a}>{actionLabel(a)}</li>
                  })}
                </ul>
              </dd>
            </>)}
          </dl>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
          <button type="button" onClick={onToggle} aria-expanded={open} aria-controls={panelId}
            className="inline-flex items-center gap-1 h-7 -ml-1 px-1 rounded text-xs text-secondary hover:text-primary
              focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">
            {open ? <ChevronDown aria-hidden="true" className="w-3.5 h-3.5" /> : <ChevronRight aria-hidden="true" className="w-3.5 h-3.5" />}
            что менялось за час до
          </button>
          <div className="flex flex-wrap items-center gap-2">
            {critical && <Button size="sm" variant="ghost" onClick={showSnapshot}>Снимок</Button>}
            {canSilence && <Button size="sm" variant="ghost" loading={silencing} onClick={silence}>Тишина 1 ч</Button>}
            <span className="text-[11px] font-mono text-tertiary break-all">{alert.key}</span>
          </div>
        </div>
        <Dialog open={!!snap} onClose={() => setSnap(null)} title="Снимок по тревоге" maxWidth="max-w-2xl">
          {snap?.loading && <div className="flex justify-center py-6"><Spinner size="sm" /></div>}
          {snap?.error && <p className="text-sm text-secondary">{snap.error === 'снимка нет' ? 'Снимка ещё нет: автоматика A16 снимает его в течение минуты после критичной тревоги (если правило не выключено).' : snap.error}</p>}
          {snap?.data && (
            <div className="flex flex-col gap-3 text-xs">
              <p className="text-secondary">снят {formatDate(snap.data.at)} · синтетика: {snap.data.data?.synthetic?.state || snap.data.data?.synthetic?.error || '—'}</p>
              {Object.entries(snap.data.data?.logs || {}).map(([unit, lines]) => (
                <div key={unit}>
                  <p className="text-tertiary mb-1">{unit}: ошибок {lines?.length || 0}</p>
                  {lines?.length > 0 && <pre className="font-mono text-secondary whitespace-pre-wrap break-words border border-default rounded p-2 max-h-40 overflow-auto">{lines.join('\n')}</pre>}
                </div>
              ))}
            </div>
          )}
        </Dialog>
        {open && (
          <div id={panelId} className="border-t border-default pt-2">
            {before?.loading && <div className="flex justify-center py-3"><Spinner size="sm" /></div>}
            {before?.error && <p role="alert" className="text-xs text-danger">{before.error}</p>}
            {before && !before.loading && !before.error && before.items.length === 0 && (
              <p className="text-xs text-tertiary">За час до тревоги ничего не менялось: ни выкаток, ни настроек, ни перезапусков.</p>
            )}
            {before?.items?.length > 0 && (
              <ul className="space-y-1">
                {before.items.map((it, i) => (
                  <li key={i} className="flex flex-wrap items-baseline gap-x-2 text-xs">
                    <span className="font-mono tabular-nums text-tertiary shrink-0">{formatDate(it.at).slice(-5)}</span>
                    <span className="text-secondary break-words min-w-0">{it.summary}{it.target ? <span className="text-tertiary"> · {it.target}</span> : null}</span>
                    {it.impact && it.impact !== 'K0' && <span className="text-warning shrink-0">{impactLabel(it.impact)}</span>}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    </Card>
  )
}
