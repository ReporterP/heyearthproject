import { useEffect, useState } from 'react'
import { useToast } from '../Toast.jsx'
import { fetchOpsActions, fetchOpsAutomation, setOpsActionMode, setOpsAutomationMode } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Card, CardBody, CardHeader, EmptyState, Select, Skeleton } from '../ui/index.js'
import { actionModeRows, ruleRows } from './automationModel.js'

/**
 * AutomationTab — «Операции → Автоматика»: the 16 rules (what fires them, what they do, their
 * limits, mode or switch, how many times they fired and the last case) and the K2/K3 console
 * actions, which ship in dry-run until the owner arms them.
 * Props: canWrite (settings.write: switch the engine rules), canArm (ops.disrupt: arm actions).
 */
export default function AutomationTab({ canWrite, canArm }) {
  const toast = useToast()
  const [rules, setRules] = useState(null)
  const [actions, setActions] = useState([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  async function load() {
    try {
      const [a, b] = await Promise.all([fetchOpsAutomation(), fetchOpsActions().catch(() => ({ actions: [] }))])
      setRules(a.rules || [])
      setActions(b.actions || [])
      setError('')
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  async function setRuleMode(id, mode) {
    setBusy(id)
    try {
      await setOpsAutomationMode(id, mode)
      toast.success(`Правило ${id}: режим изменён`)
      await load()
    } catch (e) { toast.error(e.message) } finally { setBusy('') }
  }

  async function arm(a) {
    setBusy(a.id)
    try {
      await setOpsActionMode(a.id, a.next)
      toast.success(a.next === 'on' ? `«${a.title}» в боевом режиме` : `«${a.title}» в пробном режиме`)
      await load()
    } catch (e) { toast.error(e.message) } finally { setBusy('') }
  }

  if (error && !rules) return <EmptyState title="«Автоматика» не загрузилась" description={error} action={<Button size="sm" variant="secondary" onClick={load}>Повторить</Button>} />
  if (!rules) return <Skeleton className="h-64 w-full" />

  const rows = ruleRows(rules)
  const acts = actionModeRows(actions)

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
          <span className="text-sm font-medium text-primary">Правила</span>
          <span className="text-xs text-tertiary sm:ml-auto">новое правило выходит в пробном режиме: пишет в ленту «сделал бы» и ничего не меняет</span>
        </CardHeader>
        <CardBody className="p-0">
          <ul className="divide-y divide-default">
            {rows.map(r => (
              <li key={r.id} className="px-4 py-3 flex flex-col gap-1">
                <div className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
                  <span className="font-mono text-xs text-tertiary w-8 shrink-0">{r.id}</span>
                  <span className="text-sm text-primary">{r.title}</span>
                  <div className="sm:ml-auto flex items-center gap-2">
                    {r.switchable ? (
                      <>
                        <Badge variant={r.mode.tone}>{r.mode.label}</Badge>
                        {canWrite && (
                          <Select aria-label={`Режим правила ${r.id}`} value={r.modeValue}
                            disabled={busy === r.id} onChange={e => setRuleMode(r.id, e.target.value)} className="w-auto">
                            <option value="off">выключено</option>
                            <option value="dry-run">пробный режим</option>
                            <option value="on">включено</option>
                          </Select>
                        )}
                      </>
                    ) : (
                      <span className="text-xs text-tertiary">{r.builtin ? 'встроено' : 'витрина'}</span>
                    )}
                  </div>
                </div>
                <p className="text-xs text-secondary sm:pl-11">Когда: {r.when}. Делает: {r.does}.{r.limits ? ` Ограничители: ${r.limits}.` : ''}</p>
                {r.switchText && <p className="text-xs text-tertiary sm:pl-11">{r.switchText}</p>}
                {r.switchable && r.fired > 0 && (
                  <p className="text-xs text-tertiary sm:pl-11">
                    срабатываний: <span className="font-mono tabular-nums">{r.fired}</span>
                    {r.last_fired_at && <> · последнее {formatDate(r.last_fired_at)}: {r.last_summary}</>}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </CardBody>
      </Card>

      <Card>
        <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
          <span className="text-sm font-medium text-primary">Действия К2 и К3</span>
          <span className="text-xs text-tertiary sm:ml-auto">в боевой режим переводит владелец (право ops.disrupt)</span>
        </CardHeader>
        <CardBody className="p-0">
          {acts.length === 0 ? (
            <EmptyState className="py-6" title="Действий К2/К3 нет" description="Список действий не загрузился или права на чтение нет." />
          ) : (
            <ul className="divide-y divide-default">
              {acts.map(a => (
                <li key={a.id} className="px-4 py-2 flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 text-sm">
                  <span className="text-primary">{a.title}</span>
                  <span className="font-mono text-xs text-tertiary">{a.id}</span>
                  <Badge variant={a.impact === 'K3' ? 'danger' : 'warning'}>{a.impact}</Badge>
                  <Badge variant={a.modeView.tone}>{a.modeView.label}</Badge>
                  {canArm && (
                    <Button size="sm" variant="ghost" className="sm:ml-auto" loading={busy === a.id} onClick={() => arm(a)}>
                      {a.next === 'on' ? 'Включить по-настоящему' : 'Вернуть в пробный'}
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
