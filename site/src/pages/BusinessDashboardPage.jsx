import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import { getBusinessMe, initBusinessTopup, createBusinessInvite, removeBusinessMember, changeBusinessMemberRole } from '../api/business.js'
import NarrowHeader from '../components/NarrowHeader.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import Card from '../components/Card.jsx'
import CopyField from '../components/CopyField.jsx'
import Dialog, { DialogActions } from '../components/Dialog.jsx'
import EmptyState from '../components/EmptyState.jsx'
import Field, { Input } from '../components/Field.jsx'
import Flap from '../components/Flap.jsx'
import ListRow from '../components/ListRow.jsx'
import { SkeletonCard } from '../components/Skeleton.jsx'
import { Progress, Select } from '../components/Controls.jsx'
import { shortId } from '../components/Nomerok.jsx'
import { useToast } from '../components/Toast.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { ROLES, ROLE_OPTIONS, initials } from '../lib/b2b.js'
import { date, dateTime, days, group, money, plural } from '../lib/format.js'

// Кабинет компании /business/dashboard (spec 10 §10.4, artboard «Кабинет компании»).
// GET /api/v1/business/me → company, the caller's role, members, operations, invoices. The role decides
// what is shown: admin — invite, roles, remove; admin and billing — top up. No membership → /business.
const AMOUNTS = [1000, 5000, 10000, 25000]
const ROLE_PHRASE = { admin: 'администратор', billing: 'биллинг-менеджер', user: 'сотрудник', viewer: 'доступ только на чтение' }
const STATUS = {
  active: { tone: 'ok', text: 'Аккаунт активен' },
  inactive: { tone: 'warn', text: 'Аккаунт неактивен' },
  suspended: { tone: 'bad', text: 'Аккаунт приостановлен' },
}
const INVITE_ROLES = [
  { value: 'user', label: 'Сотрудник — только доступ' },
  { value: 'viewer', label: 'Только чтение — видит кабинет' },
  { value: 'billing', label: 'Биллинг-менеджер — может пополнять' },
  { value: 'admin', label: 'Админ — полный доступ' },
]
const INVOICE = { paid: { tone: 'ok', text: 'Оплачен' }, pending: { tone: 'warn', text: 'Ожидание' }, cancelled: { tone: undefined, text: 'Отменён' } }

const memberName = (m) => (m.username ? `@${m.username}` : m.email || `ID ${shortId(m.user_id)}`)
const memberInitials = (m) => initials(m.username || (m.email || '').split('@')[0] || '?')

// the API answers in English; the person reads Russian (05 §6)
function say(e, fallback) {
  const m = String(e?.message || '')
  if (/last admin/i.test(m)) return 'В компании должен остаться хотя бы один администратор.'
  if (/admin role required|does not allow/i.test(m)) return 'Это действие доступно только администратору компании.'
  if (/amount/i.test(m)) return 'Минимум 100 ₽'
  return /[а-я]/i.test(m) ? m : fallback
}

// the 1st of next month — when the monthly charge happens
function nextCharge() {
  const d = new Date()
  return new Date(d.getFullYear(), d.getMonth() + 1, 1)
}

export default function BusinessDashboardPage() {
  const { token } = useAuth()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const toast = useToast()
  const narrow = useMedia('(max-width: 1024px)')
  const phone = useMedia('(max-width: 640px)')
  const { data: acc } = useAccount(['profile'], { optional: ['profile'] })
  const myId = acc?.profile?.user_id
  const [data, setData] = useState(null)
  const [loadErr, setLoadErr] = useState(false)
  const [savingRole, setSavingRole] = useState(null)
  const [leaving, setLeaving] = useState(null) // user_db_id whose row is collapsing
  const [topup, setTopup] = useState(false)
  const [invite, setInvite] = useState(false)
  const [remove, setRemove] = useState(null)
  const welcomed = useRef(false)
  const timers = useRef([])
  useEffect(() => () => timers.current.forEach(clearTimeout), [])

  usePageMeta(`${data?.tenant?.name ? `${data.tenant.name} — ` : ''}Кабинет компании — HiEarth`, { noindex: true })

  const load = useCallback(async () => {
    setLoadErr(false)
    try {
      setData(await getBusinessMe(token))
    } catch (e) {
      if (/no B2B membership/i.test(e.message || '')) { navigate('/business', { replace: true }); return }
      setLoadErr(true)
    }
  }, [token, navigate])

  useEffect(() => { load() }, [load])

  // after joining by an invitation (/claim → ?welcome=1): one toast, then the parameter goes away
  useEffect(() => {
    if (!data || welcomed.current || params.get('welcome') !== '1') return
    welcomed.current = true
    toast.ok(`Вы в компании ${data.tenant.name}`)
    params.delete('welcome')
    setParams(params, { replace: true })
  }, [data]) // eslint-disable-line react-hooks/exhaustive-deps

  async function changeRole(m, role) {
    if (role === m.role || savingRole) return
    const prev = m.role
    setSavingRole(m.user_db_id)
    setData((d) => ({ ...d, members: d.members.map((x) => (x.user_db_id === m.user_db_id ? { ...x, role } : x)) }))
    try {
      await changeBusinessMemberRole(token, m.user_db_id, role)
      toast.ok('Роль изменена')
    } catch (e) {
      setData((d) => ({ ...d, members: d.members.map((x) => (x.user_db_id === m.user_db_id ? { ...x, role: prev } : x)) }))
      toast.bad(say(e, 'Не удалось изменить роль — попробуйте ещё раз.'))
    } finally {
      setSavingRole(null)
    }
  }

  // the row collapses (04: `collapse`, 320 ms) after the dialog closes, then the list reloads
  function removed(m) {
    setRemove(null)
    toast.ok('Сотрудник удалён')
    timers.current.push(setTimeout(() => setLeaving(m.user_db_id), 170))
    timers.current.push(setTimeout(() => {
      setData((d) => ({ ...d, members: d.members.filter((x) => x.user_db_id !== m.user_db_id) }))
      setLeaving(null)
      document.getElementById('staff')?.focus({ preventScroll: true }) // the row with the opener is gone
      load()
    }, 520))
  }

  const header = <NarrowHeader back={{ to: '/dashboard', label: 'Личный кабинет' }} logoText={phone ? undefined : 'HiEarth для бизнеса'} />

  if (loadErr) {
    return (
      <>
        {header}
        <main id="main" className="container" style={{ paddingTop: 56, paddingBottom: 80 }}>
          <EmptyState mood="worried" title="Не удалось загрузить кабинет компании" actions={<Button variant="secondary" onClick={load} icon="refresh">Повторить</Button>}>
            Сервис временно недоступен. Попробуйте ещё раз через минуту.
          </EmptyState>
        </main>
      </>
    )
  }

  if (!data) {
    return (
      <>
        {header}
        <main id="main" className="container" style={{ paddingTop: 40, paddingBottom: 80 }} aria-busy="true">
          <span className="sr" role="status">Загружаем кабинет компании…</span>
          <SkeletonCard lines={1} style={{ marginBottom: 16 }} />
          <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0,4fr) minmax(0,8fr)', gap: 16 }}>
            <SkeletonCard big />
            <SkeletonCard lines={5} />
          </div>
        </main>
      </>
    )
  }

  const t = data.tenant
  const role = data.role
  const isAdmin = role === 'admin'
  const canBill = role === 'admin' || role === 'billing'
  const members = data.members || []
  const max = Number(t.max_users) || 0
  const full = max > 0 && members.length >= max
  const status = STATUS[t.status] || { tone: undefined, text: t.status }
  const invoices = data.invoices || []
  const operations = data.transactions || []
  const balance = Math.round((Number(t.balance) || 0) * 100) / 100
  const span = (n) => (narrow ? undefined : { gridColumn: `span ${n}` })
  const seats = max > 0
    ? <span className="mono">{members.length} из {max} {plural(max, ['места', 'мест', 'мест'])}</span>
    : <span className="mono">{members.length} {plural(members.length, ['место', 'места', 'мест'])}</span>

  return (
    <>
      {header}
      <main id="main" className="container" style={{ paddingTop: phone ? 28 : 40, paddingBottom: 80 }}>
        <div className="page-head rise">
          <div style={{ minWidth: 0 }}>
            <h1 className="t-h1">{t.name}</h1>
            <p className="muted mono t-sm" style={{ marginTop: 6 }}>{t.inn ? `ИНН ${t.inn} · ` : ''}вы — {ROLE_PHRASE[role] || role}</p>
          </div>
          <Badge tone={status.tone} style={{ height: 30, padding: '0 12px', fontSize: 13 }}>{status.text}</Badge>
        </div>

        {t.status === 'inactive' && <Alert tone="warn" rise className="mb-4"><b>Аккаунт неактивен.</b> Пополните кошелёк — после первого платежа аккаунт активируется автоматически.</Alert>}
        {t.status === 'suspended' && <Alert tone="bad" rise className="mb-4"><b>Аккаунт приостановлен.</b> Баланс закончился. Пополните — доступ восстановится после ежемесячного списания.</Alert>}

        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'repeat(12, minmax(0,1fr))', gap: 16 }}>
          <Card title="Баланс компании" className="rise" style={{ ...span(4), '--d': '60ms' }}>
            <Flap value={group(balance)} suffix="₽" size={phone ? 36 : 44} label={money(balance)} />
            <p className="t-sm subtle" style={{ marginTop: 10 }}>
              Следующее списание — {date(nextCharge())}: {max > 0 ? `${max} ${plural(max, ['место', 'места', 'мест'])}` : 'места'} × тариф «{t.plan}».
            </p>
            {canBill
              ? <Button variant="primary" block onClick={() => setTopup(true)} style={{ marginTop: 16 }}>Пополнить кошелёк</Button>
              : <p className="t-xs subtle" style={{ marginTop: 12 }}>Пополнение доступно администратору и биллинг-менеджеру</p>}
          </Card>

          <Card
            title={<>Сотрудники · {seats}</>}
            action={isAdmin && members.length > 1 ? <Button size="sm" onClick={() => setInvite(true)} disabled={full}>+ Пригласить</Button> : null}
            className="rise"
            style={{ ...span(8), '--d': '120ms' }}
            id="staff"
            tabIndex={-1}
          >
            {max > 0 && <div style={{ marginBottom: 8 }}><Progress value={(members.length / max) * 100} warn={full} label={`Занято ${members.length} из ${max} мест`} /></div>}
            {full && <p className="t-xs subtle" style={{ marginBottom: 8 }}>Мест не осталось — новые сотрудники не смогут присоединиться.</p>}
            {members.length <= 1 && (
              <EmptyState small title="Сотрудники не привязаны" actions={isAdmin ? <Button size="sm" onClick={() => setInvite(true)}>Пригласить первого</Button> : null}>
                {isAdmin
                  ? 'Создайте ссылку-приглашение — сотрудник получит доступ без отдельной регистрации.'
                  : 'Приглашать сотрудников может администратор компании.'}
              </EmptyState>
            )}
            {members.length > 1 && (phone
              ? (
                <div className="rows">
                  {members.map((m) => {
                    const me = !!m.is_me
                    const locked = !myId || me // until the profile is known, no row is editable
                    return (
                      <div key={m.user_db_id} className={`collapse ${leaving === m.user_db_id ? '' : 'is-open'}`}>
                        <div>
                          <div className="stack gap-2" style={{ padding: '12px 0', borderTop: '1px solid var(--line)' }}>
                            <span className="row gap-2" style={{ minWidth: 0 }}>
                              <span className="ava">{memberInitials(m)}</span>
                              <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}><b className="t-sm">{memberName(m)}</b>{me && <span className="subtle t-sm"> (вы)</span>}<br /><span className="mono t-xs subtle">с {date(m.joined_at)}</span></span>
                            </span>
                            {isAdmin && !locked
                              ? (
                                <span className="row gap-2">
                                  <Select value={m.role} onChange={(r) => changeRole(m, r)} disabled={savingRole === m.user_db_id} options={ROLE_OPTIONS} label={`Роль: ${memberName(m)}`} />
                                  <Button variant="ghost" size="sm" onClick={() => setRemove(m)} disabled={savingRole === m.user_db_id}>Удалить</Button>
                                </span>
                              )
                              : <Badge tone={ROLES[m.role]?.tone} style={{ width: 'max-content' }}>{ROLES[m.role]?.text || m.role}</Badge>}
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              )
              : (
                <table className="tbl">
                  <thead><tr><th>Сотрудник</th><th>Роль</th><th>В компании</th>{isAdmin && <th><span className="sr">Действия</span></th>}</tr></thead>
                  <tbody>
                    {members.map((m) => {
                      const me = !!m.is_me
                      const locked = !myId || me // until the profile is known, no row is editable
                      const cell = (c) => <div className={`collapse ${leaving === m.user_db_id ? '' : 'is-open'}`}><div>{c}</div></div>
                      return (
                        <tr key={m.user_db_id} style={leaving === m.user_db_id ? { opacity: 0, transition: 'opacity 160ms var(--ease-out)' } : undefined}>
                          <td>{cell(<span className="row gap-2" style={{ minWidth: 0 }}><span className="ava">{memberInitials(m)}</span><span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>{memberName(m)}{me && <span className="subtle"> (вы)</span>}</span></span>)}</td>
                          <td>{cell(isAdmin && !locked
                            ? <Select value={m.role} onChange={(r) => changeRole(m, r)} disabled={savingRole === m.user_db_id} options={ROLE_OPTIONS} label={`Роль: ${memberName(m)}`} />
                            : <Badge tone={ROLES[m.role]?.tone}>{ROLES[m.role]?.text || m.role}</Badge>)}</td>
                          <td className="mono subtle">{cell(m.joined_at ? `с ${date(m.joined_at)}` : '—')}</td>
                          {isAdmin && <td style={{ textAlign: 'right' }}>{cell(locked ? null : <Button variant="ghost" size="sm" onClick={() => setRemove(m)} disabled={savingRole === m.user_db_id}>Удалить</Button>)}</td>}
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              ))}
          </Card>

          <Card title="Счета" className="rise" style={{ ...span(6), '--d': '180ms' }}>
            {invoices.length === 0
              ? <EmptyState small title="Счетов пока нет" actions={<Button variant="ghost" size="sm" to="/business/invoice">Получить счёт</Button>} />
              : (
                <div className="rows">
                  {invoices.map((inv) => (
                    <ListRow key={inv.id} icon="file" title={`Счёт № ${inv.id}`} sub={[date(inv.created_at), inv.inn ? `ИНН ${inv.inn}` : ''].filter(Boolean).join(' · ')}
                      amount={money(inv.amount)} badge={INVOICE[inv.status] || { text: inv.status }} />
                  ))}
                </div>
              )}
          </Card>

          <Card title="Операции" className="rise" style={{ ...span(6), '--d': '240ms' }}>
            {operations.length === 0
              ? (
                <EmptyState small title="Операций пока нет" actions={canBill ? <Button variant="ghost" size="sm" onClick={() => setTopup(true)}>Пополнить кошелёк</Button> : null}>
                  Здесь появятся пополнения кошелька и ежемесячные списания.
                </EmptyState>
              )
              : (
                <div className="rows">
                  {operations.map((tx) => {
                    const credit = tx.direction === 'credit'
                    return (
                      <ListRow key={tx.id} icon={credit ? 'topup' : 'receipt'} title={tx.description || (credit ? 'Пополнение' : 'Списание')} sub={dateTime(tx.created_at)}
                        amount={`${credit ? '+' : '−'}${money(Math.abs(Number(tx.amount) || 0))}`} amountTone={credit ? 'ok' : ''} />
                    )
                  })}
                </div>
              )}
          </Card>
        </div>
      </main>

      <TopupDialog open={topup} onClose={() => setTopup(false)} token={token} />
      <InviteDialog open={invite} onClose={() => setInvite(false)} token={token} />
      <RemoveDialog member={remove} onClose={() => setRemove(null)} onRemoved={removed} token={token} />
    </>
  )
}

// Пополнение кошелька компании: a redirect payment of ЮKassa (topup-init gives confirmation_url).
function TopupDialog({ open, onClose, token }) {
  const [pick, setPick] = useState(5000)
  const [own, setOwn] = useState('')
  const [shown, setShown] = useState(false) // the field error appears after leaving the field or «Оплатить»
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const sum = own ? parseInt(own, 10) || 0 : pick
  const bad = own && sum < 100 ? 'Минимум 100 ₽' : ''

  useEffect(() => { if (open) { setErr(''); setBusy(false); setShown(false) } }, [open])
  // «Назад» from the ЮKassa page restores this page from the back/forward cache with busy=true
  useEffect(() => {
    const h = (e) => { if (e.persisted) setBusy(false) }
    window.addEventListener('pageshow', h)
    return () => window.removeEventListener('pageshow', h)
  }, [])

  async function pay(e) {
    e.preventDefault()
    setShown(true)
    if (!sum || bad || busy) return
    setBusy(true)
    setErr('')
    try {
      const r = await initBusinessTopup(token, { amount: sum, returnUrl: `${window.location.origin}/business/dashboard` })
      if (r.confirmation_url) { window.location.assign(r.confirmation_url); return }
      setErr('Не удалось создать платёж — попробуйте ещё раз.')
    } catch (e2) {
      setErr(say(e2, 'Не удалось создать платёж — попробуйте ещё раз.'))
    }
    setBusy(false)
  }

  return (
    <Dialog open={open} onClose={onClose} title="Пополнение кошелька" busy={busy} closeOnOverlay={!own}>
      <form onSubmit={pay} noValidate className="stack gap-3">
        <p className="muted t-sm">Деньги придут после успешной оплаты в ЮKassa.</p>
        <span className="t-label" id="b-amounts-l">Сумма</span>
        <div className="amounts" role="radiogroup" aria-labelledby="b-amounts-l">
          {AMOUNTS.map((a) => (
            <span key={a} style={{ display: 'contents' }}>
              <input type="radio" id={`b-amt-${a}`} name="b-amount" checked={!own && pick === a} onChange={() => { setPick(a); setOwn('') }} />
              <label htmlFor={`b-amt-${a}`}>{group(a)}&nbsp;₽</label>
            </span>
          ))}
        </div>
        <Field id="b-own" label="Своя сумма, ₽" error={(shown && bad) || null}>
          <Input id="b-own" mono inputMode="numeric" placeholder="от 100 ₽" value={own} onBlur={() => setShown(true)} onChange={(e) => { setOwn(e.target.value.replace(/\D/g, '').slice(0, 7)); setShown(false) }} />
        </Field>
        {err && <Alert tone="bad" rise>{err}</Alert>}
        <DialogActions>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Отмена</Button>
          <Button variant="primary" type="submit" loading={busy}>{sum && !bad ? `Оплатить ${group(sum)} ₽` : 'Оплатить'}</Button>
        </DialogActions>
      </form>
    </Dialog>
  )
}

// Пригласить сотрудника: role → POST /api/v1/business/invite → a single-use link for 7 days.
function InviteDialog({ open, onClose, token }) {
  const [role, setRole] = useState('user')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [res, setRes] = useState(null)

  useEffect(() => { if (open) { setRes(null); setErr(''); setBusy(false); setRole('user') } }, [open])

  async function create() {
    if (busy) return
    setBusy(true)
    setErr('')
    try {
      setRes(await createBusinessInvite(token, { role }))
    } catch (e) {
      setErr(say(e, 'Не удалось создать приглашение — попробуйте ещё раз.'))
    }
    setBusy(false)
  }

  const left = res?.expires_at ? Math.max(1, Math.round((new Date(res.expires_at) - Date.now()) / 86400000)) : 7
  return (
    <Dialog open={open} onClose={onClose} title="Пригласить сотрудника" busy={busy}>
      {res
        ? (
          <div className="stack gap-3 swap">
            <p className="muted t-sm">Отправьте ссылку сотруднику — она действует {days(left)}{res.expires_at ? ` (до ${dateTime(res.expires_at)})` : ''}. Ссылка одноразовая.</p>
            <CopyField value={res.claim_url} label="Ссылка-приглашение" />
            <DialogActions><Button variant="primary" onClick={onClose} data-autofocus>Готово</Button></DialogActions>
          </div>
        )
        : (
          <div className="stack gap-3">
            <p className="muted t-sm">Создаст одноразовую ссылку на 7 дней.</p>
            <Field id="inv-role" label="Роль">
              <select id="inv-role" className="sel" style={{ width: "100%", height: 44 }} value={role} onChange={(e) => setRole(e.target.value)}>
                {INVITE_ROLES.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </Field>
            {err && <Alert tone="bad" rise>{err}</Alert>}
            <DialogActions>
              <Button variant="ghost" onClick={onClose} disabled={busy}>Отмена</Button>
              <Button variant="primary" onClick={create} loading={busy}>Создать ссылку</Button>
            </DialogActions>
          </div>
        )}
    </Dialog>
  )
}

// Удалить сотрудника?: DELETE /api/v1/business/members/:id (the server keeps the last admin).
function RemoveDialog({ member, onClose, onRemoved, token }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const last = useRef(member)
  if (member) last.current = member
  const m = last.current

  useEffect(() => { if (member) { setErr(''); setBusy(false) } }, [member])

  async function go() {
    if (busy || !m) return
    setBusy(true)
    setErr('')
    try {
      await removeBusinessMember(token, m.user_db_id)
      setBusy(false)
      onRemoved(m)
    } catch (e) {
      setErr(say(e, 'Не удалось удалить сотрудника — попробуйте ещё раз.'))
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!member} onClose={onClose} title="Удалить сотрудника?" busy={busy}>
      <p className="muted t-sm">{m ? memberName(m) : ''} потеряет доступ, место освободится.</p>
      {err && <Alert tone="bad" rise className="mt-3">{err}</Alert>}
      <DialogActions>
        <Button variant="ghost" onClick={onClose} disabled={busy}>Отмена</Button>
        <Button variant="danger" onClick={go} loading={busy}>Удалить</Button>
      </DialogActions>
    </Dialog>
  )
}
