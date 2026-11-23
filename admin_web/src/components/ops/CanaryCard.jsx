import { Badge, Button, Card, CardBody, CardHeader } from '../ui/index.js'
import { canaryView, diffChecks, flagStateLabel } from './flagsModel.js'

const PHONE_CHECKLIST = [
  'Телефон в мобильной сети (Wi‑Fi выключен), Happ на подписке HiEarth тестового пользователя',
  'Обновить подписку в Happ и подключиться',
  'Открыть заблокированный в РФ сайт',
  'Открыть российский сайт без замедления',
]

/**
 * CanaryCard — the running change of a subscription flag: phase, time left, what got worse,
 * the phone checklist (visible, not required) and the honest line about what is not checked.
 * Props: change (the API's active), flag (its registry entry), honesty, nowMs, canWrite, busy,
 * onPromote(), onRollback().
 */
export default function CanaryCard({ change, flag, honesty, nowMs, canWrite, busy, onPromote, onRollback }) {
  const v = canaryView(change, nowMs)
  const d = diffChecks(change.baseline, change.new_failing)
  const why = v.canPromote ? '' : change.phase === 'guard' ? 'уже «всем»: идёт страховочный час' : `«Всем» откроется после PASS: ${v.verdict}`
  return (
    <Card>
      <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-2 justify-between">
        <div className="min-w-0">
          <div className="text-sm font-medium text-primary">{flag?.label || change.flag}</div>
          <div className="text-xs text-tertiary">
            {flagStateLabel(change.from)} → {flagStateLabel(change.to)}
            {change.bypass ? ` · без канарейки: ${change.reason}` : ''} · {change.by}
          </div>
        </div>
        <Badge variant={v.verdictTone}>{v.phaseLabel}{v.minutesLeft ? ` · ${v.minutesLeft} мин` : ''}</Badge>
      </CardHeader>
      <CardBody className="space-y-3 text-sm">
        <p className={v.verdictTone === 'danger' ? 'text-danger' : v.verdictTone === 'success' ? 'text-success' : 'text-secondary'}>{v.verdict}</p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <div className="text-xs text-tertiary mb-1">Стало хуже после изменения</div>
            {d.worse.length === 0 ? <div className="text-xs text-secondary">ничего</div> : (
              <ul className="space-y-0.5">{d.worse.map(w => <li key={w} className="text-xs text-danger break-words">{w}</li>)}</ul>
            )}
          </div>
          <div>
            <div className="text-xs text-tertiary mb-1">Не работало и до изменения (не в счёт)</div>
            {d.unchanged.length === 0 ? <div className="text-xs text-secondary">ничего</div> : (
              <ul className="space-y-0.5">{d.unchanged.map(w => <li key={w} className="text-xs text-secondary break-words">{w}</li>)}</ul>
            )}
          </div>
        </div>
        <div className="text-xs text-tertiary">
          Прогонов без новых поломок: <span className="font-mono tabular-nums text-secondary">{change.good_runs}</span>
          {change.blind_streak > 0 && <> · слепых подряд: <span className="font-mono tabular-nums text-warning">{change.blind_streak}</span></>}
        </div>
        <details className="text-xs">
          <summary className="cursor-pointer text-secondary">Ручная проверка с телефона — по желанию</summary>
          <ol className="list-decimal pl-5 mt-1 space-y-0.5 text-secondary">{PHONE_CHECKLIST.map(s => <li key={s}>{s}</li>)}</ol>
        </details>
        <p className="text-xs text-tertiary">{honesty}</p>
        {canWrite && (
          <div className="flex flex-col sm:flex-row gap-2">
            <Button size="sm" disabled={!v.canPromote || busy} loading={busy === 'promote'} title={why || undefined} onClick={onPromote}>Всем</Button>
            <Button size="sm" variant="secondary" disabled={!!busy} loading={busy === 'rollback'} onClick={onRollback}>Откат</Button>
            {!v.canPromote && why && <span className="text-xs text-tertiary self-center">{why}</span>}
          </div>
        )}
      </CardBody>
    </Card>
  )
}
