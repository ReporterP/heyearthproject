import { useEffect, useState } from 'react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { runHubAddressStep, hubAddressAction, fetchHubAddressPhone } from '../../api/admin.js'
import { Badge, Button, ConfirmDanger, Stepper, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { HUB_STEPS, stepView, countdown, transitionBlockers, changeBanner, dnsAutoRecheck } from './hubAddressModel.js'

const STATUS = {
  done: { v: 'success', t: 'готово' }, current: { v: 'accent', t: 'сейчас' }, running: { v: 'info', t: 'выполняется' },
  failed: { v: 'danger', t: 'не прошло' }, pending: { v: 'default', t: 'дальше' },
}
const VERDICT = { ok: { v: 'success', t: 'ок' }, fail: { v: 'danger', t: 'не доходит' }, not_indicative: { v: 'default', t: 'не показатель' } }

function OkBadge({ ok, okText = 'ок', badText = 'нет' }) {
  return <Badge variant={ok ? 'success' : 'danger'}>{ok ? okText : badText}</Badge>
}

function Probes({ title, items }) {
  if (!items?.length) return null
  return (
    <div className="space-y-1">
      <p className="text-xs text-tertiary">{title}</p>
      {items.map((p, i) => (
        <div key={i} className="flex items-start gap-2 text-sm min-w-0">
          <OkBadge ok={p.ok} />
          <span className="text-secondary min-w-0 break-words">{p.name}{p.error ? <span className="text-tertiary"> — {p.error}</span> : null}</span>
        </div>
      ))}
    </div>
  )
}

function Blockers({ items }) {
  if (!items?.length) return null
  return (
    <ul className="rounded border border-warning/30 bg-warning/10 px-3 py-2 space-y-1 text-sm text-warning list-disc list-inside">
      {items.map((b, i) => <li key={i}>{b}</li>)}
    </ul>
  )
}

function Log({ text }) {
  if (!text) return null
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-tertiary">Журнал шага</summary>
      <pre className="mt-2 max-h-64 overflow-auto rounded border border-default bg-canvas p-2 font-mono text-[11px] text-secondary whitespace-pre-wrap">{text}</pre>
    </details>
  )
}

function CheckPanel({ change, checks, canAct, onPhone, busy }) {
  const phone = change.phone
  return (
    <div className="space-y-3">
      {checks?.outbound?.length > 0 && (
        <Table>
          <Thead><Tr><Th>Наружу с хаба</Th><Th align="right">Текущий</Th><Th align="right">Новый</Th><Th>Вывод</Th></Tr></Thead>
          <Tbody>
            {checks.outbound.map((t) => (
              <Tr key={t.target}>
                <Td mono>{t.target}</Td>
                <Td mono align="right">{t.cur}/{t.tries}</Td>
                <Td mono align="right">{t.new}/{t.tries}</Td>
                <Td><Badge variant={VERDICT[t.verdict]?.v}>{VERDICT[t.verdict]?.t || t.verdict}</Badge></Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}
      {checks && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Probes title="Из-за рубежа (через выход)" items={checks.abroad} />
          <Probes title="Из РФ (с входов, 256 КБ)" items={checks.russia} />
          <div className="text-sm space-y-1">
            <p className="text-xs text-tertiary">Почта и ЮKassa с нового адреса</p>
            <p className="text-secondary">Почта: <span className="font-mono">{checks.smtp === 'skipped' ? 'не настроена' : checks.smtp}</span></p>
            <p className="text-secondary">ЮKassa: <span className="font-mono">{checks.yookassa === 'skipped' ? 'не настроена' : checks.yookassa}</span></p>
          </div>
          <p className="text-sm text-secondary">Бамбук: {checks.bamboo}</p>
        </div>
      )}
      <div className="rounded border border-default p-3 space-y-2">
        <p className="text-sm text-primary">Проверка с телефона</p>
        <p className="text-xs text-tertiary">Выключите Wi-Fi и откройте ссылку на телефоне: страница скачает 2 МБ с нового адреса. Сравнение с текущим адресом покажет, режет ли оператор подсеть целиком.</p>
        {phone?.url && (
          <div className="flex flex-col sm:flex-row gap-3 sm:items-start">
            {phone.qr && <img src={phone.qr} alt="QR-код ссылки проверки" width={140} height={140} className="rounded border border-default" />}
            <div className="min-w-0 space-y-1">
              <p className="text-xs text-tertiary">{phone.target === 'current' ? 'Текущий адрес' : 'Новый адрес'}</p>
              <p className="font-mono text-xs text-secondary break-all">{phone.url}</p>
              {(phone.results || []).map((r, i) => (
                <p key={i} className="text-sm text-secondary"><OkBadge ok={r.ok} okText="прошло" badText="не прошло" /> {r.operator || r.ip} · <span className="font-mono tabular-nums">{Math.round((r.delivered_bytes || 0) / 1024)} КБ</span></p>
              ))}
            </div>
          </div>
        )}
        {change.phone_skipped && <p className="text-xs text-warning">Проверка с телефона пропущена владельцем.</p>}
        {canAct && (
          <div className="flex flex-col sm:flex-row flex-wrap gap-2">
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => onPhone('new')}>Ссылка для нового адреса</Button>
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => onPhone('current')}>Сравнить с текущим</Button>
            {phone?.url && <Button variant="ghost" size="sm" disabled={busy} onClick={() => onPhone('read')}>Обновить результат</Button>}
            {!change.phone_skipped && <Button variant="ghost" size="sm" disabled={busy} onClick={() => onPhone('skip')}>Пропустить</Button>}
          </div>
        )}
      </div>
      <Blockers items={checks?.blockers} />
    </div>
  )
}

function NodesPanel({ checks, canAct, onExclude, busy }) {
  if (!checks?.nodes?.length) return <p className="text-sm text-tertiary">Выходы и входы переустанавливаются по одному: разрешения получают новый адрес, а SSH и агент проверяются с него.</p>
  return (
    <Table>
      <Thead><Tr><Th>Узел</Th><Th>Адрес</Th><Th>Состояние</Th><Th>Задача</Th><Th /></Tr></Thead>
      <Tbody>
        {checks.nodes.map((n) => (
          <Tr key={n.server_id}>
            <Td>{n.name} <span className="text-tertiary text-xs">{n.role === 'exit' ? 'выход' : 'вход'}</span></Td>
            <Td mono>{n.ip}</Td>
            <Td><Badge variant={n.status === 'ok' ? 'success' : n.status === 'excluded' ? 'default' : 'danger'}>{{ ok: 'готов', failed: 'ошибка', not_ready: 'не в сети', excluded: 'исключён' }[n.status] || n.status}</Badge>
              {n.error && <span className="block text-xs text-tertiary mt-1">{n.error}</span>}</Td>
            <Td mono>{n.job_id ? <a className="text-accent hover:underline" href={`/servers?node=${n.server_id}`}>#{n.job_id}</a> : '—'}</Td>
            <Td align="right">{canAct && n.status === 'not_ready' && <Button variant="ghost" size="sm" disabled={busy} onClick={() => onExclude(n)}>Исключить</Button>}</Td>
          </Tr>
        ))}
      </Tbody>
    </Table>
  )
}

function DNSPanel({ change, checks }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-secondary">В Cloudflare поменяйте A-записи, которые указывают на <span className="font-mono">{change.old_ip}</span>, на <span className="font-mono">{change.new_ip}</span> (оранжевое облако не включать). Мастер проверяет резолв каждые 30 секунд.</p>
      {checks?.rows?.length > 0 && (
        <Table>
          <Thead><Tr><Th>Имя</Th><Th>Было</Th><Th>1.1.1.1</Th><Th>8.8.8.8</Th><Th>77.88.8.8</Th><Th>Что делать</Th></Tr></Thead>
          <Tbody>
            {checks.rows.map((r) => (
              <Tr key={r.name}>
                <Td mono>{r.name}</Td>
                <Td mono>{r.before || '—'}</Td>
                {['1.1.1.1', '8.8.8.8', '77.88.8.8'].map((s) => <Td key={s} mono>{(r.by_server?.[s] || []).join(', ') || '—'}</Td>)}
                <Td>{r.points_at_old ? <Badge variant={r.converged ? 'success' : 'warning'}>{r.converged ? 'сошёлся' : 'поменяйте'}</Badge> : <span className="text-xs text-tertiary">{r.note}</span>}</Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}
      <Probes title="Сертификат на новом адресе" items={checks?.certs} />
    </div>
  )
}

function SwitchPanel({ change, checks, now }) {
  return (
    <div className="space-y-3">
      <p className="rounded border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-warning">
        Хаб начнёт отвечать с нового адреса, подписки и входы переедут на него. Бамбук прервётся на несколько минут, пока перенастраиваются входы. До подтверждения работает таймер отката: если не подтвердить, всё вернётся само.
      </p>
      {change.state === 'switching' && change.confirm_deadline && (
        <p className="text-sm text-primary">Откат через <span className="font-mono tabular-nums text-lg">{countdown(change.confirm_deadline, now)}</span>, если не подтвердить.</p>
      )}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Probes title="Проверки после переключения" items={checks?.probes} />
        <Probes title="Входы" items={(checks?.relays || []).map((r) => ({ name: `${r.name} (${r.ip})`, ok: r.status === 'ok', error: r.error }))} />
      </div>
    </div>
  )
}

function TransitionPanel({ change }) {
  const t = change.transition
  if (!t) return null
  const pct = t.m > 0 ? Math.round((t.n / t.m) * 100) : 100
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div className="rounded border border-default p-3"><p className="text-xs text-tertiary">Прошло</p><p className="font-mono tabular-nums text-lg text-primary">{Math.floor(t.hours)} ч из 72</p></div>
        <div className="rounded border border-default p-3"><p className="text-xs text-tertiary">Обновили подписку</p><p className="font-mono tabular-nums text-lg text-primary">{t.n} из {t.m} · {pct}%</p></div>
        <div className="rounded border border-default p-3"><p className="text-xs text-tertiary">На старый адрес, в минуту</p><p className="font-mono tabular-nums text-lg text-primary">{t.measured ? t.old_per_min.toFixed(1) : '—'}</p></div>
      </div>
      <Blockers items={transitionBlockers(t)} />
    </div>
  )
}

function FinishPanel({ change }) {
  const notes = change.steps?.finish?.checks?.notes || []
  if (!notes.length) return <p className="text-sm text-tertiary">Старый адрес снимается с интерфейса и из netplan, если через него не идёт шлюз.</p>
  return <ul className="space-y-1 text-sm text-secondary list-disc list-inside">{notes.map((n, i) => <li key={i}>{n}</li>)}</ul>
}

/**
 * HubAddressWizard — the steps of one change: a stepper, one open step with its checks and its one
 * primary button, the revert countdown in step 5, the transition counters in step 6.
 */
export default function HubAddressWizard({ change, onChanged }) {
  const { can } = useAuth()
  const toast = useToast()
  const canAct = can('hub.address')
  const [busy, setBusy] = useState(false)
  const [phone, setPhone] = useState(null)
  const [now, setNow] = useState(() => new Date())
  const [confirm, setConfirm] = useState(null) // 'force' | 'override' | 'cancel' | 'revert' | {exclude: node}
  useEffect(() => {
    if (change.state !== 'switching') return undefined
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [change.state])

  const recheck = canAct && dnsAutoRecheck(change)
  useEffect(() => {
    if (!recheck) return undefined
    const id = setInterval(() => { runHubAddressStep(change.id, 'dns').then(() => onChanged?.()).catch(() => {}) }, 30000)
    return () => clearInterval(id)
  }, [recheck, change.id, onChanged])

  const view = stepView(change)
  const idx = HUB_STEPS.findIndex((s) => s.key === (view.find((v) => v.status === 'current' || v.status === 'running' || v.status === 'failed')?.key))
  const current = idx < 0 ? HUB_STEPS.length : idx
  const banner = changeBanner(change)
  const shown = { ...change, phone: phone || change.phone }

  const act = async (fn, ok) => {
    setBusy(true)
    try {
      const r = await fn()
      if (ok) toast.success(ok)
      await onChanged?.()
      return r
    } catch (e) {
      toast.error(e?.message || 'Не получилось')
      throw e
    } finally {
      setBusy(false)
    }
  }
  const run = (action) => {
    if (action.startsWith('step:')) return act(() => runHubAddressStep(change.id, action.slice(5)), 'Шаг запущен').catch(() => {})
    if (action === 'confirm') return act(() => hubAddressAction(change.id, 'confirm', {}), 'Подтверждено: таймер отката снят').catch(() => {})
    if (action === 'finish') return act(() => hubAddressAction(change.id, 'finish', {}), 'Старый адрес выведен').catch(() => {})
    if (action === 'revert') return setConfirm('revert')
    if (action === 'dns-trust') return act(() => hubAddressAction(change.id, 'dns-trust'), 'Принято: DNS поменян, дальше — переключение').catch(() => {})
    return undefined
  }
  const onPhone = async (which) => {
    try {
      if (which === 'skip') return await act(() => hubAddressAction(change.id, 'phone-skip'), 'Проверка с телефона пропущена')
      if (which === 'read') return setPhone(await fetchHubAddressPhone(change.id))
      const r = await act(() => hubAddressAction(change.id, 'phone-check', { which }))
      setPhone({ ...r, target: which, results: [] })
    } catch { /* toast shown */ }
    return undefined
  }

  const panel = (key) => {
    const checks = change.steps?.[key]?.checks
    switch (key) {
      case 'add': return checks ? <p className="text-sm text-secondary">Новый адрес в <span className="font-mono">{checks.subnet}</span>, текущий в <span className="font-mono">{checks.cur_subnet}</span>. Netplan: <span className="font-mono">{checks.netplan_path}</span>.</p> : null
      case 'check': return <CheckPanel change={shown} checks={checks} canAct={canAct} onPhone={onPhone} busy={busy} />
      case 'nodes': return <NodesPanel checks={checks} canAct={canAct} busy={busy} onExclude={(n) => setConfirm({ exclude: n })} />
      case 'dns': return <DNSPanel change={change} checks={checks} />
      case 'switch': return <SwitchPanel change={change} checks={checks} now={now} />
      case 'transition': return <TransitionPanel change={change} />
      case 'finish': return <FinishPanel change={change} />
    }
    return null
  }

  return (
    <div className="space-y-4">
      {banner && <p role="status" className={`rounded border px-3 py-2 text-sm ${banner.tone === 'danger' ? 'border-danger/30 bg-danger/10 text-danger' : banner.tone === 'warning' ? 'border-warning/30 bg-warning/10 text-warning' : banner.tone === 'success' ? 'border-success/30 bg-success/10 text-success' : 'border-default text-secondary'}`}>{banner.text}</p>}
      <p className="text-sm text-secondary">Смена <span className="font-mono">{change.old_ip}</span> → <span className="font-mono">{change.new_ip}</span></p>
      <Stepper steps={HUB_STEPS.map((s) => s.title)} current={current} />
      <ol className="space-y-2">
        {view.map((s) => {
          const open = ['current', 'running', 'failed'].includes(s.status) || (s.key === 'finish' && change.state === 'finished')
          const rec = change.steps?.[s.key]
          return (
            <li key={s.key} className={`rounded border ${open ? 'border-strong bg-elevated' : 'border-default'} p-3`}>
              <div className="flex items-center gap-2 justify-between min-w-0">
                <p className={`text-sm min-w-0 truncate ${open ? 'text-primary font-medium' : 'text-secondary'}`}>{s.title}</p>
                <Badge variant={STATUS[s.status]?.v}>{STATUS[s.status]?.t}</Badge>
              </div>
              {open && (
                <div className="mt-3 space-y-3">
                  {panel(s.key)}
                  <Log text={rec?.log} />
                  {canAct && (s.primary || s.secondary) && (
                    <div className="flex flex-col sm:flex-row flex-wrap gap-2">
                      {s.primary && <Button variant="primary" size="sm" loading={busy} disabled={busy || s.primary.disabled} onClick={() => run(s.primary.action)}>{s.primary.label}</Button>}
                      {s.primary?.disabled && s.primary.action === 'confirm' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConfirm('force')}>Подтвердить несмотря на…</Button>}
                      {s.primary?.disabled && s.primary.action === 'finish' && <Button variant="secondary" size="sm" disabled={busy} onClick={() => setConfirm('override')}>Вывести несмотря на…</Button>}
                      {s.secondary && <Button variant={s.secondary.action === 'revert' ? 'danger' : 'secondary'} size="sm" disabled={busy} onClick={() => run(s.secondary.action)}>{s.secondary.label}</Button>}
                    </div>
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ol>
      {canAct && ['draft', 'added', 'checked', 'nodes_ready', 'dns_ready'].includes(change.state) && !change.running && (
        <Button variant="ghost" size="sm" onClick={() => setConfirm('cancel')}>Отменить смену</Button>
      )}

      <ConfirmDanger open={confirm === 'revert'} onClose={() => setConfirm(null)} title="Вернуть хаб на старый адрес?"
        consequences={[`Хаб снова отвечает с ${change.old_ip}, подписки и входы возвращаются на него.`, 'Бамбук через входы прервётся на несколько минут.']}
        confirmText={change.old_ip} actionLabel="Вернуть как было"
        onConfirm={() => act(() => hubAddressAction(change.id, 'revert', {}), 'Откат запущен').then(() => setConfirm(null))} />
      <ConfirmDanger open={confirm === 'force'} onClose={() => setConfirm(null)} title="Подтвердить, хотя проверки не зелёные?"
        consequences={['Таймер отката снимется: хаб останется на новом адресе.', 'Красные проверки выше останутся красными — разберитесь с ними.']}
        confirmText={change.new_ip} actionLabel="Подтвердить"
        onConfirm={() => act(() => hubAddressAction(change.id, 'confirm', { force: true }), 'Подтверждено').then(() => setConfirm(null))} />
      <ConfirmDanger open={confirm === 'override'} onClose={() => setConfirm(null)} title="Вывести старый адрес раньше времени?"
        consequences={[...transitionBlockers(change.transition), 'Клиенты со старыми строками «· напрямую» потеряют их до обновления подписки.']}
        confirmText={change.old_ip} actionLabel="Вывести"
        onConfirm={() => act(() => hubAddressAction(change.id, 'finish', { override: true }), 'Старый адрес выведен').then(() => setConfirm(null))} />
      <ConfirmDanger open={confirm === 'cancel'} onClose={() => setConfirm(null)} title="Отменить смену адреса?"
        consequences={[`${change.new_ip} снимается с интерфейса, если его добавил мастер, netplan возвращается.`, 'Если меняли DNS-записи — верните их сами.']}
        confirmText={change.new_ip} actionLabel="Отменить смену"
        onConfirm={() => act(() => hubAddressAction(change.id, 'cancel'), 'Смена отменена').then(() => setConfirm(null))} />
      <ConfirmDanger open={Boolean(confirm?.exclude)} onClose={() => setConfirm(null)} title={`Исключить ${confirm?.exclude?.name || ''}?`}
        consequences={['Узел не получит новый адрес: после переключения хаб потеряет с ним связь, пока его не переустановят.']}
        confirmText={confirm?.exclude?.name || ''} actionLabel="Исключить"
        onConfirm={() => act(() => hubAddressAction(change.id, 'exclude-node', { server_id: confirm.exclude.server_id }), 'Узел исключён').then(() => setConfirm(null))} />
    </div>
  )
}
