import { useCallback, useEffect, useId, useReducer, useRef, useState } from 'react'
import { ListChecks, ShieldCheck } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import {
  verifyNode, provisionNode, deprovisionNode, deleteNode, updateNodeRouting, fetchNodeJobs, fetchPlans, fetchServerLogs,
  probeNode, confirmHostKey, reinstallMTProxy, runServerAction,
} from '../../api/admin.js'
import { formatDate, formatRelativeRu } from '../../utils/format.js'
import {
  Drawer, Tabs, TabPanel, Badge, Button, Input, Select, Switch, CopyButton, ConfirmDanger, TableSkeleton, EmptyState, useFocusTrap,
} from '../ui/index.js'
import { nodeState, keyTypeLabel, hostKeyFile, PRIORITY_HINT, JOB_KIND } from './nodeModel.js'
import { normalizePlans, matchPasted } from './wizardModel.js'
import {
  PRIORITIES, PLANS_HINT, LOG_SERVICES, drawerTabs, initialTab, initialJobId, nodeActions, clampPriority, sortedPlanIds,
  routingBody, fallbackBody, routingDirty, fallbackOffRisky, pathRow, mtprotoView, jobListStatus, keyRows, initialKeyCheck,
  keyCheckReducer, canConfirmProbeKey, keyPasteVerdict, storedKeyNotice, keyReplaceWarning, reprovisionConsequences,
  deprovisionConsequences, deleteConsequences, fallbackOffConsequences, plansNote, plansFailure, LAST_EXIT_PHRASE,
} from './drawerModel.js'
import JobProgress from './JobProgress.jsx'
import MobileCheckPanel from '../entries/MobileCheckPanel.jsx'
import TunnelCheckDialog from '../servers/TunnelCheckDialog.jsx'
import LoadCharts from '../servers/LoadCharts.jsx'
import OwnerForm from '../servers/OwnerForm.jsx'
import { loadView } from '../servers/serversModel.js'

const ROLE_LABEL = { exit: 'Выход за рубежом', relay: 'Резервный вход в РФ' }
const SETUP_TEXT = {
  reprovision: 'Приводит работающий выход к эталонной настройке: каждый шаг сначала проверяется, меняется только то, что отличается.',
  provision: 'Настройка с первого шага (выполненные шаги только проверятся) и проверка обоих путей туннеля; в маршрутизацию выход попадёт только после неё. Тарифы и приоритет — во вкладке «Тарифы и приоритет».',
}
const NOTE_TONE = { warning: 'text-warning', danger: 'text-danger' }

function Section({ title, children }) {
  return (
    <section>
      <h3 className="text-xs font-medium text-secondary mb-1">{title}</h3>
      {children}
    </section>
  )
}

function Row({ label, children }) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start gap-1 sm:gap-3 py-2 border-b border-default last:border-b-0">
      <div className="sm:w-44 shrink-0 text-xs text-tertiary sm:pt-0.5">{label}</div>
      <div className="flex-1 min-w-0">{children}</div>
    </div>
  )
}

function PathValue({ row }) {
  const { view } = row
  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Badge variant={view.variant}>{view.label}</Badge>
        {row.rttMs > 0 && <span className="font-mono tabular-nums text-xs text-secondary">{row.rttMs} мс</span>}
        {row.checkedAt && (
          <span className="font-mono tabular-nums text-xs text-tertiary" title={`Проверено ${formatDate(row.checkedAt)}`}>
            {formatRelativeRu(row.checkedAt)}
          </span>
        )}
      </div>
      {view.hint && <p className={`text-xs mt-1 ${NOTE_TONE[view.variant] || 'text-tertiary'}`}>{view.hint}</p>}
      {row.error && <p className="text-xs font-mono text-tertiary mt-1 break-all">{row.error}</p>}
    </div>
  )
}

// ActionItem: one action of the card with what it does and, when unavailable, why.
function ActionItem({ label, description, reason, onClick, loading, disabled }) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start gap-1.5 sm:gap-3 py-2">
      <Button variant="secondary" onClick={onClick} loading={loading} disabled={disabled || !!reason} className="w-full sm:w-60 shrink-0">
        {label}
      </Button>
      <div className="min-w-0 text-xs sm:pt-1.5">
        {description && <p className="text-tertiary">{description}</p>}
        {reason && <p className="text-warning mt-0.5">{reason}</p>}
      </div>
    </div>
  )
}

// EscapeScope: a plain ui/Dialog opened from the card owns Escape — it closes itself, not the
// drawer underneath too (useFocusTrap stops the key in the capture phase), and keeps focus inside.
function EscapeScope({ onEscape, children }) {
  const ref = useRef(null)
  useFocusTrap(ref, true, { onEscape })
  return <div ref={ref}>{children}</div>
}

// HostKeySection: the stored key with the ssh-keygen command of its own type and, for the owner,
// the check: every key the server presents now, an optional pasted console line (selects only)
// and the explicit confirmation (POST /servers/{id}/host-key).
function HostKeySection({ node, manage, onChanged }) {
  const toast = useToast()
  const hk = node.host_key || {}
  const [open, setOpen] = useState(!hk.confirmed)
  const [s, dispatch] = useReducer(keyCheckReducer, initialKeyCheck)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const busyRef = useRef(false)
  const uid = useId()
  const file = hostKeyFile(hk.key_type)
  const cmd = file ? `ssh-keygen -lf ${file}` : ''

  async function guard(key, fn) {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(key)
    setError('')
    try {
      await fn()
    } catch (e) {
      setError(e?.message || 'Не удалось выполнить запрос')
    } finally {
      busyRef.current = false
      setBusy('')
    }
  }
  const probe = () => guard('probe', async () => dispatch({ type: 'probed', result: await probeNode({ server_id: node.id }) }))
  const confirm = () => guard('confirm', async () => {
    if (!canConfirmProbeKey(s)) return
    const r = await confirmHostKey(node.id, s.selected)
    toast.success(`Ключ ${keyTypeLabel(r?.key_type)} подтверждён — Hub подключается к серверу только с ним`)
    dispatch({ type: 'reset' })
    setOpen(false)
    await onChanged?.()
  })
  function cancel() {
    dispatch({ type: 'reset' })
    setError('')
    setOpen(false)
  }

  const rows = keyRows(s.keys, hk.fingerprint)
  const chosen = rows.find((k) => k.fingerprint === s.selected)
  const verdict = keyPasteVerdict(s)
  const pastedKey = verdict === 'mismatch' ? rows.find((k) => k.fingerprint === matchPasted(s.pasted, s.keys)?.fingerprint) : null
  const notice = storedKeyNotice(s.probe)
  const replace = keyReplaceWarning(hk, s.selected)
  const noop = !!hk.confirmed && s.selected === hk.fingerprint
  const confirmable = canConfirmProbeKey({ ...s, ownerConfirmed: true }) && !noop

  return (
    <Section title="Ключ SSH сервера">
      <Row label={hk.key_type ? `Отпечаток (${keyTypeLabel(hk.key_type)})` : 'Отпечаток'}>
        <div className="flex flex-wrap items-center gap-2">
          <code className="font-mono text-xs text-primary break-all">{hk.fingerprint || '—'}</code>
          {hk.fingerprint && <CopyButton text={hk.fingerprint} label="Скопировать отпечаток" />}
          <Badge variant={hk.confirmed ? 'success' : 'warning'}>{hk.confirmed ? 'подтверждён' : 'не подтверждён'}</Badge>
        </div>
        {cmd && (
          <div className="flex flex-wrap items-center gap-1 mt-1">
            <span className="text-xs text-tertiary">В консоли хостера:</span>
            <code className="font-mono text-xs text-tertiary break-all">{cmd}</code>
            <CopyButton text={cmd} label="Скопировать команду" />
          </div>
        )}
      </Row>
      {!hk.confirmed && (
        <p className="text-xs text-warning mt-2">
          Пока отпечаток не сверен с консолью хостера, Hub не подключается к серверу: настройка, проверка, журналы и Telegram-прокси недоступны. Трафик пользователей это не останавливает.
        </p>
      )}
      {!manage && !hk.confirmed && <p className="text-xs text-tertiary mt-1">Подтвердить может администратор с правом настройки нод.</p>}
      {manage && !open && (
        <Button variant="ghost" size="sm" className="mt-2" onClick={() => setOpen(true)}>
          {hk.confirmed ? 'Сверить ключ заново' : 'Подтвердить ключ'}
        </Button>
      )}
      {manage && open && (
        <div className="mt-3 p-3 rounded border border-default">
          <div className="flex items-center gap-2 text-sm text-primary mb-1">
            <ShieldCheck className="w-4 h-4 text-accent" aria-hidden="true" />
            Сверка ключа с консолью хостера
          </div>
          <p className="text-xs text-tertiary mb-3">
            Hub запросит у сервера все его ключи (только обмен ключами, пароль не отправляется). Откройте консоль сервера в панели хостера (веб-консоль или VNC, не SSH), выполните команду для любого из ключей и сравните отпечаток до символа.
          </p>
          {!s.probe ? (
            <div className="flex flex-col sm:flex-row gap-2">
              <Button onClick={probe} loading={busy === 'probe'} className="w-full sm:w-auto">Получить ключи сервера</Button>
              {hk.confirmed && <Button variant="ghost" onClick={cancel} disabled={!!busy} className="w-full sm:w-auto">Отмена</Button>}
            </div>
          ) : (
            <>
              {notice && <p role="alert" className="mb-3 text-xs text-danger">{notice}</p>}
              {rows.length === 0 ? (
                <p role="alert" className="mb-3 text-xs text-danger">Сервер не предъявил ни одного поддерживаемого ключа.</p>
              ) : (
                <div role="radiogroup" aria-label="Ключ для сверки" className="space-y-2">
                  {rows.map((k) => {
                    const selected = k.fingerprint === s.selected
                    return (
                      <div key={k.fingerprint} className={`rounded border px-3 py-2 ${selected ? 'border-accent bg-accent/10' : 'border-default'}`}>
                        <div className="flex items-start gap-1">
                          <label className="flex items-start gap-2 min-w-0 flex-1 cursor-pointer">
                            <input type="radio" name={`${uid}-key`} checked={selected} disabled={!!busy} className="mt-1 accent-accent"
                              onChange={() => dispatch({ type: 'select', fingerprint: k.fingerprint })} />
                            <span className="min-w-0 flex-1">
                              <span className="flex flex-wrap items-center gap-2 text-xs text-secondary">
                                {k.label}
                                {k.stored && <Badge>{hk.confirmed ? 'подтверждён сейчас' : 'сохранён'}</Badge>}
                              </span>
                              <code className="block font-mono text-xs sm:text-sm text-primary break-all">{k.fingerprint}</code>
                            </span>
                          </label>
                          <CopyButton text={k.fingerprint} label={`Скопировать отпечаток ${k.label}`} />
                        </div>
                        {k.cmd && (
                          <div className="flex items-center gap-1 mt-1 pl-6">
                            <code className="font-mono text-xs text-tertiary break-all">{k.cmd}</code>
                            <CopyButton text={k.cmd} label="Скопировать команду" />
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
              <div className="mt-3">
                <label htmlFor={`${uid}-paste`} className="block text-xs text-secondary mb-1">Вывод команды из консоли хостера (необязательно)</label>
                <Input id={`${uid}-paste`} value={s.pasted} disabled={!!busy} onChange={(e) => dispatch({ type: 'paste', text: e.target.value })}
                  placeholder="256 SHA256:… root@server (ECDSA)" autoComplete="off" spellCheck={false} className="font-mono text-xs" />
                <p className="text-xs text-tertiary mt-1">Вставьте строку целиком — выберется ключ с этим отпечатком; подтверждаете вы сами, переключателем ниже.</p>
              </div>
              {verdict === 'match' && <p className="mt-2 text-xs text-success">Совпадает с ключом {chosen?.label} — ключ выбран.</p>}
              {verdict === 'mismatch' && (
                <p role="alert" className="mt-2 text-xs text-danger">
                  {pastedKey
                    ? `Вставлен отпечаток ключа ${pastedKey.label}, а выбран ${chosen?.label || 'другой'} — выберите ключ, который вы сверяли.`
                    : 'Не совпадает ни с одним ключом, который предъявил сервер. Не подтверждайте: проверьте сервер в панели хостера — или подключение кто-то подменяет.'}
                </p>
              )}
              <Switch className="mt-3" checked={s.ownerConfirmed} disabled={!!busy || !confirmable}
                onChange={(v) => dispatch({ type: 'confirm', value: v })}
                label={`Отпечаток ${chosen?.label || 'ключа'} совпадает с консолью хостера`}
                description="Подтверждая, вы разрешаете Hub подключаться к серверу только с этим ключом." />
              {noop && <p className="mt-2 text-xs text-tertiary">Этот ключ уже подтверждён — менять нечего.</p>}
              {replace && <p className="mt-2 text-xs text-warning">{replace}</p>}
              <div className="flex flex-col sm:flex-row gap-2 mt-3">
                <Button onClick={confirm} loading={busy === 'confirm'} disabled={!canConfirmProbeKey(s) || noop || busy === 'probe'}
                  className="w-full sm:w-auto">Подтвердить ключ</Button>
                <Button variant="secondary" onClick={probe} loading={busy === 'probe'} disabled={busy === 'confirm'}
                  className="w-full sm:w-auto">Запросить ключи заново</Button>
                {hk.confirmed && <Button variant="ghost" onClick={cancel} disabled={!!busy} className="w-full sm:w-auto">Отмена</Button>}
              </div>
            </>
          )}
          {error && <p role="alert" className="mt-2 text-xs text-danger break-words">{error}</p>}
        </div>
      )}
    </Section>
  )
}

function LoadRow({ node }) {
  const v = loadView(node.load, { portMbps: node.owner?.port_mbps, role: node.role })
  if (v.empty) return <Row label="Нагрузка"><span className="text-xs text-tertiary">{v.why}</span></Row>
  const cell = (label, c) => (
    <span className="whitespace-nowrap"><span className="text-tertiary">{label} </span><span className={c.warn ? 'text-warning' : 'text-primary'}>{c.text}</span></span>
  )
  return (
    <Row label="Нагрузка">
      <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono tabular-nums text-sm">
        {cell('CPU', v.cpu)}{cell('RAM', v.ram)}{cell('канал', v.net)}
        {node.role === 'relay' ? cell('conntrack', v.conntrack) : cell('подкл.', v.conns)}
      </div>
      {v.why && <p className="text-xs text-warning mt-1">{v.why}</p>}
    </Row>
  )
}

function OverviewTab({ node, acts, manage, busy, fallbackOn, onFallback, onTunnelCheck, onVerify, onSetup, onMtproxy, onRestartAgent, canRestart, onChanged }) {
  const st = nodeState(node)
  const exit = node.role !== 'relay'
  const mt = mtprotoView(node.mtproto_status)
  return (
    <div className="space-y-6">
      <Section title="Состояние">
        <Row label="Сервер">
          <Badge variant={st.variant}>{st.label}</Badge>
          {st.hint && <p className="text-xs text-tertiary mt-1">{st.hint}</p>}
        </Row>
        {exit && node.lifecycle_state === 'online' && (
          <Row label="node-agent">
            <Badge variant={node.status === 'online' ? 'success' : 'danger'}>{node.status === 'online' ? 'отвечает' : 'не отвечает'}</Badge>
            {node.status !== 'online' && <p className="text-xs text-tertiary mt-1">Hub не получает ответ агента три проверки подряд (раз в 30 с).</p>}
          </Row>
        )}
        <LoadRow node={node} />
        {exit && <Row label="Основной туннель (UDP)"><PathValue row={pathRow(node, 'hy')} /></Row>}
        {exit && <Row label="Запасной TCP-путь"><PathValue row={pathRow(node, 'tcp')} /></Row>}
        {exit && (
          <Row label="Telegram-прокси">
            <Badge variant={mt.variant}>{mt.label}</Badge>
            {mt.hint && <p className="text-xs text-tertiary mt-1">{mt.hint}</p>}
          </Row>
        )}
        {exit && (
          <Row label="Приоритет">
            <span className="font-mono tabular-nums text-sm text-primary" title={PRIORITY_HINT}>{node.load_weight}</span>
            <span className="text-xs text-tertiary"> · тарифов: <span className="font-mono tabular-nums">{node.plan_ids?.length ?? 0}</span></span>
          </Row>
        )}
      </Section>

      {acts.fallback.show && (
        <Switch checked={fallbackOn} disabled={!!busy} onChange={onFallback} label="Запасной TCP-путь"
          description="Если основной туннель (UDP) не отвечает — например, его душат на пути «Россия → заграница», — трафик этого выхода идёт по TCP. Без него выход с молчащим основным туннелем перестаёт принимать трафик." />
      )}

      <HostKeySection node={node} manage={manage} onChanged={onChanged} />

      {!exit && node.host_key?.confirmed && (
        <Section title="Мобильные сети">
          <MobileCheckPanel serverId={node.id} canStart={manage} />
        </Section>
      )}

      {(acts.tunnelCheck.show || acts.verify.show) && (
        <Section title="Проверки">
          {acts.tunnelCheck.show && (
            <ActionItem label="Проверить туннель" onClick={onTunnelCheck}
              description="Запрос через каждый путь до выхода прямо сейчас. Только чтение: статусы и настройки не меняются." />
          )}
          {acts.verify.show && (
            <ActionItem label="Проверить настройку" onClick={onVerify} reason={acts.verify.reason} loading={busy === 'verify'} disabled={!!busy}
              description="Задача сверяет каждый шаг настройки с эталоном и ничего не меняет на сервере; расхождения исправит «Переустановить»." />
          )}
        </Section>
      )}

      {(acts.setup.show || acts.mtproxy.show || canRestart) && (
        <Section title="Обслуживание">
          {acts.setup.show && (
            <ActionItem label={acts.setup.label} onClick={onSetup} reason={acts.setup.reason} loading={busy === 'setup'} disabled={!!busy}
              description={SETUP_TEXT[acts.setup.kind]} />
          )}
          {acts.mtproxy.show && (
            <ActionItem label="Переустановить Telegram-прокси" onClick={onMtproxy} reason={acts.mtproxy.reason} loading={busy === 'mtproxy'} disabled={!!busy}
              description="Задача сначала проверяет Telegram-прокси и ставит его заново, только если проверка не прошла; пока идёт установка, прокси этой ноды может быть недоступен. Туннель и трафик выхода это не затрагивает." />
          )}
          {canRestart && (
            <ActionItem label="Перезапустить агент" onClick={onRestartAgent} loading={busy === 'restart'} disabled={!!busy}
              reason={node.host_key?.confirmed ? '' : 'Сначала подтвердите отпечаток ключа SSH'}
              description="Перезапускает node-agent на сервере по SSH. Трафик пользователей это не затрагивает." />
          )}
        </Section>
      )}
    </div>
  )
}

function RoutingTab({ node, can, busy, plans, planIds, weight, error, onTogglePlan, onWeight, onSave, onReset }) {
  const uid = useId()
  const plansLoaded = plans.state === 'ready'
  const dirty = routingDirty(node, { planIds, weight, plansLoaded })
  const note = plansNote(plans, node)
  return (
    <div className="space-y-5">
      <fieldset>
        <legend className="text-xs text-secondary mb-1">Тарифы, которые идут через эту ноду</legend>
        {plans.state === 'loading' && <TableSkeleton rows={2} cols={2} />}
        {note && <p className={`text-xs ${plans.state === 'failed' ? 'text-warning' : 'text-secondary'}`}>{note}</p>}
        {plansLoaded && plans.items.length === 0 && <p className="text-xs text-tertiary">Тарифов пока нет — нода получает общий пул.</p>}
        {plansLoaded && plans.items.length > 0 && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-3">
            {plans.items.map((p) => (
              <label key={p.id} className={`flex items-center gap-2 min-h-9 text-sm text-primary ${can ? 'cursor-pointer' : 'cursor-not-allowed'}`}>
                <input type="checkbox" className="accent-accent" disabled={!can || !!busy} checked={planIds.includes(p.id)}
                  onChange={(e) => onTogglePlan(p.id, e.target.checked)} />
                <span className="min-w-0 truncate">{p.name}</span>
                <span className="font-mono tabular-nums text-xs text-tertiary whitespace-nowrap">{p.price_rub} ₽</span>
                {!p.is_active && <Badge>не продаётся</Badge>}
              </label>
            ))}
          </div>
        )}
        <p className="text-xs text-tertiary mt-1">{PLANS_HINT}.</p>
      </fieldset>

      <div>
        <label htmlFor={`${uid}-priority`} className="block text-xs text-secondary mb-1">Приоритет (1–10)</label>
        <Select id={`${uid}-priority`} value={weight} disabled={!can || !!busy} onChange={(e) => onWeight(e.target.value)}
          className="font-mono tabular-nums w-24">
          {PRIORITIES.map((n) => <option key={n} value={String(n)}>{n}</option>)}
        </Select>
        <p className="text-xs text-tertiary mt-1">{PRIORITY_HINT}.</p>
      </div>

      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      {can ? (
        <div className="flex flex-col sm:flex-row sm:items-center gap-2">
          <Button onClick={onSave} loading={busy === 'routing'} disabled={!!busy || !dirty} className="w-full sm:w-auto">Сохранить</Button>
          {dirty && <Button variant="ghost" onClick={onReset} disabled={!!busy} className="w-full sm:w-auto">Отменить изменения</Button>}
          <span className="text-xs text-tertiary">{dirty ? 'Есть несохранённые изменения.' : 'Маршрутизация обновляется через несколько секунд после сохранения.'}</span>
        </div>
      ) : (
        <p className="text-xs text-tertiary">Изменить может администратор с правом настройки нод.</p>
      )}
    </div>
  )
}

function JobsTab({ node, jobId, jobs, manage, onSelect, onFinished, onRetry }) {
  return (
    <div className="space-y-4">
      {jobId != null && (
        <JobProgress jobId={jobId} canProvision={manage} tcpEnabled={node.tunnel_fallback_enabled !== false} onFinished={onFinished} />
      )}
      <section>
        <h3 className="text-xs font-medium text-secondary mb-1">Последние задачи</h3>
        {jobs.items === null ? (
          jobs.error ? (
            <div className="flex flex-col sm:flex-row sm:items-center gap-2">
              <p role="alert" className="text-sm text-danger break-words">Не удалось загрузить задачи: {jobs.error}</p>
              <Button variant="secondary" size="sm" onClick={onRetry}>Повторить</Button>
            </div>
          ) : <TableSkeleton rows={3} cols={3} />
        ) : jobs.items.length === 0 ? (
          <EmptyState className="py-8" icon={<ListChecks className="w-6 h-6" />} title="Задач ещё не было"
            description="Здесь появятся настройка, проверки, переустановки и отключение этой ноды." />
        ) : (
          <ul className="border border-default rounded">
            {jobs.items.map((j) => {
              const v = jobListStatus(j)
              const selected = j.id === jobId
              return (
                <li key={j.id} className="border-b border-default last:border-b-0">
                  <button type="button" onClick={() => onSelect(j.id)} aria-current={selected ? 'true' : undefined}
                    className={`w-full flex items-center gap-3 min-h-9 px-3 py-1.5 text-left hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent ${selected ? 'bg-accent/10' : ''}`}>
                    <span className="w-12 shrink-0 font-mono tabular-nums text-xs text-tertiary">#{j.id}</span>
                    <span className="flex-1 min-w-0 truncate text-sm text-primary">{JOB_KIND[j.kind] || j.kind}</span>
                    <span className="font-mono tabular-nums text-xs text-tertiary whitespace-nowrap" title={formatDate(j.created_at)}>
                      {formatRelativeRu(j.created_at)}
                    </span>
                    <Badge variant={v.variant} className="shrink-0">{v.label}</Badge>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
        {jobs.items !== null && jobs.error && <p role="alert" className="text-xs text-danger mt-2 break-words">Не удалось обновить список: {jobs.error}</p>}
      </section>
    </div>
  )
}

function LogsTab({ node, reason }) {
  const [service, setService] = useState('node-agent')
  const [state, setState] = useState({ loading: false, text: null, error: '' })
  const seq = useRef(0)
  const uid = useId()

  async function load() {
    const n = ++seq.current
    setState((s) => ({ ...s, loading: true, error: '' }))
    try {
      const text = await fetchServerLogs(node.id, { service, tail: 200 })
      if (n === seq.current) setState({ loading: false, text: text || '', error: '' })
    } catch (e) {
      if (n === seq.current) setState({ loading: false, text: null, error: e?.message || 'Не удалось загрузить журнал' })
    }
  }
  function pick(v) {
    seq.current += 1 // an answer for the previous service is dropped
    setService(v)
    setState({ loading: false, text: null, error: '' })
  }

  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:items-end gap-2 mb-3">
        <div className="w-full sm:w-64">
          <label htmlFor={`${uid}-service`} className="block text-xs text-secondary mb-1">Сервис</label>
          <Select id={`${uid}-service`} value={service} onChange={(e) => pick(e.target.value)} className="font-mono">
            {LOG_SERVICES.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
          </Select>
        </div>
        <Button variant="secondary" onClick={load} loading={state.loading} disabled={!!reason} className="w-full sm:w-auto">
          Показать последние 200 строк
        </Button>
      </div>
      {reason && <p className="text-xs text-warning mb-2">{reason}</p>}
      {state.error && <p role="alert" className="text-sm text-danger mb-2 break-words">{state.error}</p>}
      <pre className="p-3 rounded border border-default bg-canvas max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-secondary">
        {state.text === null ? 'Выберите сервис и нажмите «Показать».' : state.text || 'Журнал пуст.'}
      </pre>
      <p className="text-xs text-tertiary mt-2">Только чтение: журнал читается с сервера по SSH с подтверждённым ключом.</p>
    </div>
  )
}

function DangerBlock({ title, text, reason, children }) {
  return (
    <div className="p-3 rounded border border-danger/30">
      <div className="text-sm text-primary">{title}</div>
      <p className="text-xs text-tertiary mt-1">{text}</p>
      {reason && <p className="text-xs text-warning mt-1">{reason}</p>}
      <div className="mt-2">{children}</div>
    </div>
  )
}

/**
 * NodeDrawer — the node card on «Ноды» (?node=<id>): «Обзор» (state, both tunnel paths with their
 * check time, the TCP fallback switch, Telegram proxy, the host key and its confirmation, «Проверить
 * туннель» — read-only, servers.read; «Проверить настройку», «Настроить»/«Переустановить»,
 * «Переустановить Telegram-прокси»), «Тарифы и приоритет» (PUT /servers/{id}/routing), «Задачи»
 * (GET /provision-jobs?server_id= + JobProgress), «Журналы» (servers.logs, read-only), «Опасная зона»
 * (ConfirmDanger: «Отключить» → deprovision, «Удалить запись» — pending|offline only).
 * Viewing needs servers.read (the page); every change nodes.provision. Rules: drawerModel.js.
 * Props: node (an item of GET /nodes, refreshed by the page), onClose, onChanged (reloads the list).
 */
export default function NodeDrawer(props) {
  // A fresh card per node: nothing of another node's edits, key check or job is shown as this one's.
  return <NodeCard key={props.node.id} {...props} />
}

function NodeCard({ node, lastExit = false, onClose, onChanged }) {
  const { can } = useAuth()
  const toast = useToast()
  const perms = { manage: can('nodes.provision'), logs: can('servers.logs') }
  const canPlans = can('settings.read') // GET /plans
  const acts = nodeActions(node, perms)
  const tabs = drawerTabs(node, perms)
  const exit = node.role !== 'relay'
  const tabsId = `node-card-${node.id}`
  const jobsTabId = `${tabsId}-tab-jobs` // the «Задачи» tab: ui/Tabs gives tab buttons the id <id>-tab-<key>

  const [tabChoice, setTab] = useState(() => initialTab(node))
  const tab = tabs.some((t) => t.key === tabChoice) ? tabChoice : 'overview'
  const [jobId, setJobId] = useState(() => initialJobId(node))
  const [jobs, setJobs] = useState({ items: null, error: '' })
  const [danger, setDanger] = useState(null) // reprovision | deprovision | delete | fallbackOff
  const [tunnelCheck, setTunnelCheck] = useState(false)
  const [busy, setBusy] = useState('')
  const [fallbackPending, setFallbackPending] = useState(null)
  const busyRef = useRef(false)
  const focusJobs = useRef(false) // a job just started: focus goes to the «Задачи» tab

  // «Тарифы и приоритет» edits live here, so switching tabs keeps them.
  const [plans, setPlans] = useState(() => ({ state: canPlans ? 'loading' : 'forbidden', items: [], error: '' }))
  const [planIds, setPlanIds] = useState(() => sortedPlanIds(node.plan_ids))
  const [weight, setWeight] = useState(() => String(clampPriority(node.load_weight)))
  const [routingError, setRoutingError] = useState('')

  useEffect(() => {
    if (!exit || !canPlans) return undefined // without settings.read: plansNote says so, nothing is requested
    let alive = true
    fetchPlans()
      .then((p) => { if (alive) setPlans({ state: 'ready', items: normalizePlans(p), error: '' }) })
      .catch((e) => { if (alive) setPlans(plansFailure(e)) })
    return () => { alive = false }
  }, [exit, canPlans])

  const loadJobs = useCallback(async () => {
    try {
      const r = await fetchNodeJobs(node.id)
      setJobs({ items: r?.items || [], error: '' })
    } catch (e) {
      setJobs((j) => ({ items: j.items, error: e?.message || 'ошибка' }))
    }
  }, [node.id])
  // The list follows the node's last job (a job started elsewhere, one that finished).
  const lastJobKey = `${node.last_job?.id ?? ''}:${node.last_job?.status ?? ''}`
  useEffect(() => { if (tab === 'jobs') loadJobs() }, [tab, loadJobs, lastJobKey])

  // The button that started the job unmounts with its tab (and ConfirmDanger's focus return finds it
  // gone): focus goes to the «Задачи» tab once no dialog is open — passive cleanups (the dialog's
  // focus return) run before this effect in the same commit.
  useEffect(() => {
    if (!focusJobs.current || danger !== null || tab !== 'jobs') return
    focusJobs.current = false
    document.getElementById(jobsTabId)?.focus()
  }, [danger, tab, jobsTabId])

  const closeDanger = useCallback(() => setDanger(null), [])
  const closeTunnelCheck = useCallback(() => setTunnelCheck(false), [])

  // act: one request of the card at a time; an error becomes a toast (undefined is returned).
  async function act(key, fn) {
    if (busyRef.current) return undefined
    busyRef.current = true
    setBusy(key)
    try {
      return await fn()
    } catch (e) {
      toast.error(e?.message || 'Не удалось выполнить действие')
      return undefined
    } finally {
      busyRef.current = false
      setBusy('')
    }
  }
  function showJob(r) {
    if (r?.job_id != null) {
      setJobId(r.job_id)
      setTab('jobs')
      focusJobs.current = true
    }
    onChanged?.()
    loadJobs()
  }
  const startJob = async (key, fn) => { const r = await act(key, fn); if (r) showJob(r) }
  // Behind ConfirmDanger: a throw keeps the dialog open with the error inside it.
  const confirmJob = async (fn) => showJob(await fn())

  async function saveFallback(enabled) {
    setFallbackPending(enabled)
    await act('fallback', async () => {
      await updateNodeRouting(node.id, fallbackBody(node, enabled))
      toast.success(`Запасной TCP-путь ${enabled ? 'включён' : 'выключен'} — маршрутизация обновится через несколько секунд`)
      await onChanged?.()
    })
    setFallbackPending(null)
  }
  function onFallback(enabled) {
    if (busyRef.current) return
    if (!enabled && fallbackOffRisky(node)) { setDanger('fallbackOff'); return }
    saveFallback(enabled)
  }

  async function saveRouting() {
    const { body, error } = routingBody(node, { planIds, weight, plansLoaded: plans.state === 'ready' })
    setRoutingError(error || '')
    if (error) return
    await act('routing', async () => {
      const r = await updateNodeRouting(node.id, body)
      toast.success(r?.message || 'Сохранено — маршрутизация обновится через несколько секунд')
      await onChanged?.()
    })
  }
  function resetRouting() {
    setPlanIds(sortedPlanIds(node.plan_ids))
    setWeight(String(clampPriority(node.load_weight)))
    setRoutingError('')
  }

  function onSetup() {
    if (acts.setup.confirm) setDanger('reprovision')
    else startJob('setup', () => provisionNode(node.id, {}))
  }

  async function removeNode() {
    await deleteNode(node.id, node.name)
    toast.success('Запись ноды удалена')
    onClose()
    onChanged?.()
  }

  const offline = node.lifecycle_state === 'offline'
  return (
    <Drawer open onClose={onClose} title={node.name || `Нода ${node.id}`} width="2xl"
      subtitle={`${ROLE_LABEL[node.role] || node.role || 'Нода'} · ${node.country_code || '—'} · ${node.ip_address}`}>
      <div className="p-4">
        <Tabs id={tabsId} label="Разделы карточки ноды" tabs={tabs} value={tab} onChange={setTab} className="mb-4" />
        <TabPanel tabsId={tabsId} tabKey={tab}>
          {tab === 'overview' && (
            <OverviewTab node={node} acts={acts} manage={perms.manage} busy={busy} onChanged={onChanged}
              fallbackOn={fallbackPending ?? node.tunnel_fallback_enabled !== false} onFallback={onFallback}
              onTunnelCheck={() => setTunnelCheck(true)}
              onVerify={() => startJob('verify', () => verifyNode(node.id))}
              onSetup={onSetup}
              onMtproxy={() => startJob('mtproxy', () => reinstallMTProxy(node.id))}
              canRestart={exit && node.lifecycle_state === 'online' && can('server_ops.execute')}
              onRestartAgent={() => act('restart', async () => {
                await runServerAction(node.id, 'restart_agent')
                toast.success('node-agent перезапущен')
              })} />
          )}
          {tab === 'load' && <LoadCharts id={node.id} relay={!exit} />}
          {tab === 'settings' && (
            <div className="space-y-3">
              <p className="text-xs text-tertiary">Эти числа знает только владелец: «Мощность» считает по ним лимит трафика, запас канала и стоимость на пользователя.</p>
              <OwnerForm id={node.id} owner={node.owner} detectedCores={node.load?.cores} canEdit={can('servers.write')} onSaved={onChanged} />
            </div>
          )}
          {tab === 'routing' && (
            <RoutingTab node={node} can={acts.routing.show} busy={busy} plans={plans} planIds={planIds} weight={weight} error={routingError}
              onTogglePlan={(id, on) => setPlanIds((ids) => sortedPlanIds(on ? [...ids, id] : ids.filter((x) => x !== id)))}
              onWeight={setWeight} onSave={saveRouting} onReset={resetRouting} />
          )}
          {tab === 'jobs' && (
            <JobsTab node={node} jobId={jobId} jobs={jobs} manage={perms.manage} onSelect={setJobId} onRetry={loadJobs}
              onFinished={() => { onChanged?.(); loadJobs() }} />
          )}
          {tab === 'logs' && <LogsTab node={node} reason={acts.logs.reason} />}
          {tab === 'danger' && (
            <div className="space-y-3">
              <DangerBlock title={offline ? 'Отключить ноду повторно' : 'Отключить ноду'} reason={acts.deprovision.reason}
                text={offline
                  ? 'Нода уже отключена. Повтор уберёт с сервера то, что не удалось убрать в прошлый раз, если сервер тогда был недоступен.'
                  : 'Нода выходит из маршрутизации, сервисы на сервере останавливаются, пароль туннеля меняется. Запись остаётся в состоянии «Отключена».'}>
                <Button variant="danger" disabled={!!acts.deprovision.reason || !!busy} onClick={() => setDanger('deprovision')}
                  className="w-full sm:w-auto">{offline ? 'Отключить повторно' : 'Отключить'}</Button>
              </DangerBlock>
              <DangerBlock title="Удалить запись" reason={acts.remove.reason}
                text="Только для отключённой или так и не настроенной ноды. История задач удалится вместе с записью; сервер у хостера не трогается.">
                <Button variant="danger" disabled={!!acts.remove.reason || !!busy} onClick={() => setDanger('delete')}
                  className="w-full sm:w-auto">Удалить</Button>
              </DangerBlock>
            </div>
          )}
        </TabPanel>
      </div>

      {tunnelCheck && (
        <EscapeScope onEscape={closeTunnelCheck}>
          <TunnelCheckDialog server={{ id: node.id, name: node.name, ip_address: node.ip_address }} onClose={closeTunnelCheck} />
        </EscapeScope>
      )}
      <ConfirmDanger open={danger === 'reprovision'} onClose={closeDanger} title="Переустановить ноду" confirmText={node.name}
        actionLabel="Переустановить" consequences={reprovisionConsequences(node)}
        onConfirm={() => confirmJob(() => provisionNode(node.id, {}))} />
      <ConfirmDanger open={danger === 'deprovision'} onClose={closeDanger} title={offline ? 'Отключить ноду повторно' : lastExit ? 'Отключить последний выход' : 'Отключить ноду'}
        confirmText={lastExit ? LAST_EXIT_PHRASE : node.name} actionLabel="Отключить" consequences={deprovisionConsequences(node, { lastExit })}
        onConfirm={() => confirmJob(() => deprovisionNode(node.id, node.name, lastExit ? LAST_EXIT_PHRASE : ''))} />
      <ConfirmDanger open={danger === 'delete'} onClose={closeDanger} title="Удалить запись ноды" confirmText={node.name}
        actionLabel="Удалить" consequences={deleteConsequences(node)} onConfirm={removeNode} />
      <ConfirmDanger open={danger === 'fallbackOff'} onClose={closeDanger} title="Выключить запасной TCP-путь" confirmText={node.name}
        actionLabel="Выключить" consequences={fallbackOffConsequences(node)}
        onConfirm={async () => {
          await updateNodeRouting(node.id, fallbackBody(node, false))
          toast.success('Запасной TCP-путь выключен — маршрутизация обновится через несколько секунд')
          await onChanged?.()
        }} />
    </Drawer>
  )
}
