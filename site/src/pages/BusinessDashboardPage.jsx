import { useEffect, useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { getBusinessMe, initBusinessTopup, createBusinessInvite, removeBusinessMember, changeBusinessMemberRole } from '../api/business.js'

/*
 * B2B tenant self-service dashboard. Rendered at /business/dashboard. The
 * logged-in user must have a tenant_members row; otherwise → /business.
 */

function formatMoney(n) {
  return new Intl.NumberFormat('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n || 0) + ' ₽'
}
function formatDate(s) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' }) } catch { return s }
}
function roleLabel(role) {
  return ({
    admin:   { text: 'Админ',         cls: 'bg-brand/20 text-brand' },
    billing: { text: 'Биллинг',       cls: 'bg-accent/20 text-accent' },
    viewer:  { text: 'Только чтение', cls: 'bg-white/10 text-text-secondary' },
    user:    { text: 'Сотрудник',     cls: 'bg-white/10 text-text-secondary' },
  }[role] || { text: role, cls: 'bg-white/10 text-text-secondary' })
}

export default function BusinessDashboardPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [actionError, setActionError] = useState('')

  const [topupOpen, setTopupOpen] = useState(false)
  const [topupAmount, setTopupAmount] = useState(5000)
  const [topupLoading, setTopupLoading] = useState(false)

  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviteRole, setInviteRole] = useState('user')
  const [inviteLoading, setInviteLoading] = useState(false)
  const [inviteResult, setInviteResult] = useState(null)

  const [confirmRemove, setConfirmRemove] = useState(null) // member pending removal
  const [removeBusy, setRemoveBusy] = useState(false)
  const [copiedField, setCopiedField] = useState('')

  async function load() {
    setLoading(true)
    try {
      const d = await getBusinessMe(token)
      setData(d)
      setError('')
    } catch (e) {
      if (e.message?.includes('no B2B membership')) { navigate('/business'); return }
      setError(e.message || 'Не удалось загрузить данные')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  async function handleTopup() {
    if (!(topupAmount >= 100)) return
    setTopupLoading(true)
    try {
      const returnUrl = window.location.origin + '/business/dashboard'
      const res = await initBusinessTopup(token, { amount: Number(topupAmount), returnUrl })
      if (res.confirmation_url) window.location.href = res.confirmation_url
    } catch (e) {
      setActionError(e.message || 'Не удалось создать платёж')
      setTopupOpen(false)
    } finally {
      setTopupLoading(false)
    }
  }

  async function handleCreateInvite() {
    setInviteLoading(true)
    setInviteResult(null)
    try {
      const res = await createBusinessInvite(token, { role: inviteRole })
      setInviteResult(res)
    } catch (e) {
      setActionError(e.message || 'Не удалось создать приглашение')
      setInviteOpen(false)
    } finally {
      setInviteLoading(false)
    }
  }

  async function doRemoveMember() {
    if (!confirmRemove) return
    setRemoveBusy(true)
    try {
      await removeBusinessMember(token, confirmRemove.user_db_id)
      setConfirmRemove(null)
      await load()
    } catch (e) {
      setActionError(e.message || 'Не удалось удалить участника')
      setConfirmRemove(null)
    } finally {
      setRemoveBusy(false)
    }
  }

  async function handleChangeRole(member, newRole) {
    if (newRole === member.role) return
    try {
      await changeBusinessMemberRole(token, member.user_db_id, newRole)
      await load()
    } catch (e) {
      setActionError(e.message || 'Не удалось изменить роль')
    }
  }

  function copy(text, field) {
    navigator.clipboard?.writeText?.(text)
    setCopiedField(field)
    setTimeout(() => setCopiedField(''), 1800)
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-10 h-10 rounded-full border-2 border-brand/25 border-t-brand animate-spin" />
    </div>
  )

  if (error || !data) return (
    <div className="max-w-xl mx-auto py-16 px-4">
      <div className="card rounded-[22px] p-6 !border-danger/20">
        <h1 className="serif text-lg font-semibold text-danger mb-2">Ошибка</h1>
        <p className="text-sm text-text-secondary mb-4">{error || 'Нет данных'}</p>
        <button onClick={load} className="btn-primary !py-2.5 !text-sm">Повторить</button>
      </div>
    </div>
  )

  const t = data.tenant
  const isAdmin = data.role === 'admin'
  const canBill = data.role === 'admin' || data.role === 'billing'
  const isInactive = t.status === 'inactive'
  const isSuspended = t.status === 'suspended'

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">

        {actionError && (
          <div className="mb-6 flex items-start gap-2 p-3 rounded-xl bg-danger/10 border border-danger/20">
            <svg className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" /></svg>
            <p className="text-sm text-danger flex-1">{actionError}</p>
            <button onClick={() => setActionError('')} aria-label="Скрыть" className="text-danger/70 hover:text-danger"><svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" d="M6 6l12 12M18 6L6 18" /></svg></button>
          </div>
        )}

        {/* Header */}
        <div className="flex items-center justify-between mb-8 gap-4 flex-wrap">
          <div>
            <p className="text-xs text-faint uppercase tracking-wider">Компания</p>
            <h1 className="serif text-2xl md:text-3xl font-semibold">{t.name}</h1>
            <p className="text-sm text-text-secondary mt-1">
              {t.inn && <>ИНН: <span className="font-mono">{t.inn}</span> · </>}
              Тариф: <span className="text-text-primary">{t.plan}</span>
              {' · '}Статус: <StatusBadge status={t.status} />
              {' · '}Ваша роль: <span className={`inline-flex px-1.5 py-0.5 rounded text-[11px] font-medium ${roleLabel(data.role).cls}`}>{roleLabel(data.role).text}</span>
            </p>
          </div>
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">← Личный кабинет</Link>
        </div>

        {/* Status banner */}
        {isInactive && (
          <div className="card rounded-[22px] p-5 mb-6 !border-accent/30 bg-accent/5">
            <p className="text-sm font-semibold text-accent">Аккаунт неактивен</p>
            <p className="text-xs text-accent/80 mt-1">Пополните кошелёк — после первого успешного платежа аккаунт активируется автоматически.</p>
          </div>
        )}
        {isSuspended && (
          <div className="card rounded-[22px] p-5 mb-6 !border-danger/30 bg-danger/5">
            <p className="text-sm font-semibold text-danger">Аккаунт приостановлен</p>
            <p className="text-xs text-danger/80 mt-1">Баланс закончился. Пополните кошелёк — доступ восстановится после следующего ежемесячного списания.</p>
          </div>
        )}

        {/* Balance + topup */}
        <div className="card-feat rounded-[22px] p-6 mb-6">
          <div className="flex items-start justify-between gap-4 flex-wrap">
            <div>
              <p className="text-xs text-faint uppercase tracking-wider mb-2">Баланс</p>
              <p className="serif text-4xl font-semibold text-text-primary tabular-nums">{formatMoney(t.balance)}</p>
              <p className="text-xs text-text-secondary mt-2">Списание {t.max_users} мест × тариф «{t.plan}» каждое 1-е число месяца.</p>
            </div>
            {canBill ? (
              <button onClick={() => setTopupOpen(true)} className="btn-primary !py-3 min-h-[48px]">Пополнить кошелёк</button>
            ) : (
              <span className="text-xs text-text-secondary max-w-[12rem]">Пополнение доступно только администратору и биллинг-менеджеру</span>
            )}
          </div>
        </div>

        {/* Members */}
        <div className="card rounded-[22px] p-6 mb-6">
          <div className="flex items-center justify-between mb-4 gap-3 flex-wrap">
            <div>
              <h2 className="serif text-lg font-semibold">Сотрудники</h2>
              <p className="text-xs text-text-secondary mt-0.5">{data.members.length} из {t.max_users || '∞'} мест занято</p>
            </div>
            {isAdmin && (
              <button onClick={() => { setInviteOpen(true); setInviteResult(null) }} className="btn-ghost !py-2 !px-4 !text-sm">+ Пригласить</button>
            )}
          </div>
          {data.members.length === 0 ? (
            <p className="text-sm text-text-secondary">Сотрудники не привязаны. Пригласите первого через кнопку выше.</p>
          ) : (
            <div className="space-y-2">
              {data.members.map(m => {
                const rl = roleLabel(m.role)
                return (
                  <div key={m.user_db_id} className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-brand/8 flex-wrap">
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium truncate">{m.username ? `@${m.username}` : (m.email || m.user_id)}</p>
                      <p className="text-[11px] text-faint truncate font-mono">{m.user_id}</p>
                    </div>
                    {isAdmin ? (
                      <select
                        value={m.role}
                        onChange={e => handleChangeRole(m, e.target.value)}
                        className="px-2 py-1 rounded-lg bg-white/5 border border-brand/12 text-[11px] font-medium hover:bg-white/10 transition"
                        title="Изменить роль"
                      >
                        <option value="admin">Админ</option>
                        <option value="billing">Биллинг</option>
                        <option value="viewer">Только чтение</option>
                        <option value="user">Сотрудник</option>
                      </select>
                    ) : (
                      <span className={`inline-flex px-2 py-0.5 rounded text-[11px] font-medium ${rl.cls}`}>{rl.text}</span>
                    )}
                    {isAdmin && (
                      <button onClick={() => setConfirmRemove(m)} className="text-xs text-danger hover:brightness-125 transition" title="Удалить из компании">Удалить</button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Invoices */}
        {data.invoices && data.invoices.length > 0 && (
          <div className="card rounded-[22px] p-6 mb-6">
            <h2 className="serif text-lg font-semibold mb-4">Счета</h2>
            <div className="space-y-2">
              {data.invoices.map(inv => (
                <div key={inv.id} className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-brand/8 flex-wrap">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium">
                      Счёт #{inv.id} · {formatMoney(inv.amount)}
                      {' · '}<span className={`text-xs px-1.5 py-0.5 rounded ${
                        inv.status === 'paid' ? 'bg-brand/15 text-brand' :
                        inv.status === 'pending' ? 'bg-accent/15 text-accent' :
                        'bg-white/5 text-faint'}`}>
                        {inv.status === 'paid' ? 'Оплачен' : inv.status === 'pending' ? 'Ожидание' : inv.status === 'cancelled' ? 'Отменён' : inv.status}
                      </span>
                    </p>
                    <p className="text-[11px] text-text-secondary mt-0.5 truncate">
                      ИНН {inv.inn} · {inv.legal_name} · создан {formatDate(inv.created_at)}
                      {inv.paid_at && <> · оплачен {formatDate(inv.paid_at)}</>}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Transactions */}
        <div className="card rounded-[22px] p-6">
          <h2 className="serif text-lg font-semibold mb-4">История операций</h2>
          {data.transactions.length === 0 ? (
            <p className="text-sm text-text-secondary">Пока пусто. Все пополнения и списания будут видны здесь.</p>
          ) : (
            <div className="space-y-2">
              {data.transactions.map(tx => (
                <div key={tx.id} className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-brand/8">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium truncate">{tx.description || (tx.direction === 'credit' ? 'Пополнение' : 'Списание')}</p>
                    <p className="text-[11px] text-faint mt-0.5">{formatDate(tx.created_at)}</p>
                  </div>
                  <span className={`font-semibold tabular-nums ${tx.direction === 'credit' ? 'text-brand' : 'text-text-primary'}`}>
                    {tx.direction === 'credit' ? '+' : '−'}{formatMoney(tx.amount)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Topup modal */}
      {topupOpen && (
        <ModalOverlay onClose={() => setTopupOpen(false)}>
          <h2 className="serif text-lg font-semibold mb-1">Пополнение кошелька</h2>
          <p className="text-sm text-text-secondary mb-5">Деньги придут после успешной оплаты в ЮKassa.</p>
          <label className="block text-xs text-text-secondary mb-1">Сумма, ₽</label>
          <input type="number" min="100" step="100" value={topupAmount} onChange={e => setTopupAmount(e.target.value)}
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:ring-2 focus:ring-brand/25 focus:outline-none text-base" />
          <div className="flex gap-2 mt-2 flex-wrap">
            {[1000, 5000, 10000, 25000].map(v => (
              <button key={v} onClick={() => setTopupAmount(v)} className="px-3 py-1.5 rounded-lg bg-white/5 border border-brand/12 hover:bg-white/10 text-xs transition">{v} ₽</button>
            ))}
          </div>
          <div className="flex gap-3 mt-6">
            <button onClick={() => setTopupOpen(false)} className="btn-ghost flex-1 !py-3 !text-sm">Отмена</button>
            <button onClick={handleTopup} disabled={topupLoading || !(topupAmount >= 100)} className="btn-primary flex-1 !py-3 !text-sm disabled:opacity-50">{topupLoading ? '…' : 'Оплатить'}</button>
          </div>
        </ModalOverlay>
      )}

      {/* Invite modal */}
      {inviteOpen && (
        <ModalOverlay onClose={() => setInviteOpen(false)}>
          <h2 className="serif text-lg font-semibold mb-1">Пригласить сотрудника</h2>
          <p className="text-sm text-text-secondary mb-5">Создаст одноразовую ссылку на 7 дней.</p>
          <label className="block text-xs text-text-secondary mb-1">Роль</label>
          <select value={inviteRole} onChange={e => setInviteRole(e.target.value)}
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:ring-2 focus:ring-brand/25 focus:outline-none text-sm">
            <option value="user">Сотрудник — только доступ</option>
            <option value="viewer">Только чтение — видит кабинет</option>
            <option value="billing">Биллинг-менеджер — может пополнять</option>
            <option value="admin">Админ — полный доступ</option>
          </select>
          {inviteResult ? (
            <div className="mt-5 p-4 rounded-xl bg-brand/10 border border-brand/30">
              <p className="text-xs text-brand uppercase tracking-wider mb-2">Ссылка создана</p>
              <code className="block font-mono text-xs break-all text-text-primary select-all">{inviteResult.claim_url}</code>
              <p className="text-[11px] text-text-secondary mt-2">Действует до {formatDate(inviteResult.expires_at)}</p>
              <button onClick={() => copy(inviteResult.claim_url, 'invite')} className="mt-3 w-full px-4 py-2.5 rounded-xl bg-white/5 border border-brand/12 hover:bg-white/10 text-sm font-medium transition">
                {copiedField === 'invite' ? '✓ Скопировано' : 'Копировать ссылку'}
              </button>
            </div>
          ) : (
            <div className="flex gap-3 mt-6">
              <button onClick={() => setInviteOpen(false)} className="btn-ghost flex-1 !py-3 !text-sm">Отмена</button>
              <button onClick={handleCreateInvite} disabled={inviteLoading} className="btn-primary flex-1 !py-3 !text-sm disabled:opacity-50">{inviteLoading ? '…' : 'Создать ссылку'}</button>
            </div>
          )}
        </ModalOverlay>
      )}

      {/* Remove-member confirm modal */}
      {confirmRemove && (
        <ModalOverlay onClose={() => !removeBusy && setConfirmRemove(null)}>
          <div className="text-center">
            <div className="w-12 h-12 mx-auto rounded-full bg-danger/12 border border-danger/25 text-danger flex items-center justify-center mb-4">
              <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.3 3.9 2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" /></svg>
            </div>
            <h2 className="serif text-lg font-semibold mb-2">Удалить сотрудника?</h2>
            <p className="text-sm text-text-secondary mb-6">{confirmRemove.username ? `@${confirmRemove.username}` : (confirmRemove.email || confirmRemove.user_id)} потеряет доступ к компании.</p>
            <div className="flex gap-3">
              <button onClick={() => setConfirmRemove(null)} disabled={removeBusy} className="btn-ghost flex-1 !py-2.5 !text-sm disabled:opacity-50">Отмена</button>
              <button onClick={doRemoveMember} disabled={removeBusy} className="flex-1 inline-flex items-center justify-center !py-2.5 !text-sm rounded-full font-semibold bg-danger text-white hover:brightness-110 active:scale-[0.97] transition disabled:opacity-50">{removeBusy ? 'Удаляем…' : 'Удалить'}</button>
            </div>
          </div>
        </ModalOverlay>
      )}
    </section>
  )
}

function StatusBadge({ status }) {
  const map = {
    active:    { text: 'Активен',      cls: 'text-brand' },
    inactive:  { text: 'Неактивен',    cls: 'text-accent' },
    suspended: { text: 'Приостановлен', cls: 'text-danger' },
  }
  const s = map[status] || { text: status, cls: 'text-text-secondary' }
  return <span className={`${s.cls} font-medium`}>{s.text}</span>
}

function ModalOverlay({ onClose, children }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-fade-in" onClick={onClose}>
      <div className="relative w-full max-w-md max-h-[90vh] overflow-y-auto rounded-[22px] glass-strong p-6 animate-fade-in-scale" onClick={e => e.stopPropagation()}>
        <button onClick={onClose} aria-label="Закрыть" className="absolute top-3 right-3 p-1.5 rounded-lg hover:bg-white/10 text-text-secondary hover:text-text-primary transition">
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
        </button>
        <div className="pr-8">{children}</div>
      </div>
    </div>
  )
}
