import { useEffect, useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { getBusinessMe, initBusinessTopup, createBusinessInvite, removeBusinessMember, changeBusinessMemberRole } from '../api/business.js'

/*
 * B2B tenant self-service dashboard.
 *
 * Rendered at /business/dashboard. The logged-in user must have a tenant_members
 * row; otherwise we redirect them to /business which is the marketing page.
 *
 * Layout:
 *   - balance card + topup CTA
 *   - status banners (inactive / suspended)
 *   - members list with role badges + invite + remove actions
 *   - transaction history (last 20 wallet entries)
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
    admin:   { text: 'Админ',         cls: 'bg-brand/20 text-brand-light' },
    billing: { text: 'Биллинг',       cls: 'bg-blue-500/20 text-blue-300' },
    viewer:  { text: 'Только чтение', cls: 'bg-gray-500/20 text-gray-300' },
    user:    { text: 'Сотрудник',     cls: 'bg-white/10 text-text-secondary' },
  }[role] || { text: role, cls: 'bg-white/10 text-text-secondary' })
}

export default function BusinessDashboardPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Modals
  const [topupOpen, setTopupOpen] = useState(false)
  const [topupAmount, setTopupAmount] = useState(5000)
  const [topupLoading, setTopupLoading] = useState(false)

  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviteRole, setInviteRole] = useState('user')
  const [inviteLoading, setInviteLoading] = useState(false)
  const [inviteResult, setInviteResult] = useState(null) // { claim_url, role, expires_at }

  const [copiedField, setCopiedField] = useState('')

  async function load() {
    setLoading(true)
    try {
      const d = await getBusinessMe(token)
      setData(d)
      setError('')
    } catch (e) {
      // 404 means the user has no membership — send them to the marketing page.
      if (e.message?.includes('no B2B membership')) {
        navigate('/business')
        return
      }
      setError(e.message || 'Ошибка загрузки')
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
      alert(e.message || 'Не удалось создать платёж')
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
      alert(e.message || 'Не удалось создать приглашение')
    } finally {
      setInviteLoading(false)
    }
  }

  async function handleRemoveMember(member) {
    if (!confirm(`Удалить ${member.username || member.email || member.user_id} из компании?`)) return
    try {
      await removeBusinessMember(token, member.user_db_id)
      await load()
    } catch (e) {
      alert(e.message || 'Не удалось удалить участника')
    }
  }

  async function handleChangeRole(member, newRole) {
    if (newRole === member.role) return
    try {
      await changeBusinessMemberRole(token, member.user_db_id, newRole)
      await load()
    } catch (e) {
      alert(e.message || 'Не удалось изменить роль')
      // The select element will snap back to the server-side role after load().
    }
  }

  function copy(text, field) {
    navigator.clipboard?.writeText?.(text)
    setCopiedField(field)
    setTimeout(() => setCopiedField(''), 1800)
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
    </div>
  )

  if (error || !data) return (
    <div className="max-w-xl mx-auto py-16 px-4">
      <div className="glass-card rounded-2xl p-6 border border-red-500/20">
        <h1 className="text-lg font-bold text-red-400 mb-2">Ошибка</h1>
        <p className="text-sm text-text-secondary mb-4">{error || 'Нет данных'}</p>
        <button onClick={load} className="text-sm text-brand-light hover:text-accent">Повторить</button>
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

        {/* Header */}
        <div className="flex items-center justify-between mb-8">
          <div>
            <p className="text-xs text-text-secondary uppercase tracking-wider">Компания</p>
            <h1 className="text-2xl md:text-3xl font-bold">{t.name}</h1>
            <p className="text-sm text-text-secondary mt-1">
              {t.inn && <>ИНН: <span className="font-mono">{t.inn}</span> · </>}
              Тариф: <span className="text-text-primary">{t.plan}</span>
              {' · '}Статус: <StatusBadge status={t.status} />
              {' · '}Ваша роль: <span className={`inline-flex px-1.5 py-0.5 rounded text-[11px] font-medium ${roleLabel(data.role).cls}`}>{roleLabel(data.role).text}</span>
            </p>
          </div>
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">
            ← Личный кабинет
          </Link>
        </div>

        {/* Status banner */}
        {isInactive && (
          <div className="glass-card rounded-2xl p-5 mb-6 border border-amber-500/30 bg-amber-500/5">
            <p className="text-sm font-semibold text-amber-300">Аккаунт неактивен</p>
            <p className="text-xs text-amber-300/80 mt-1">Пополните кошелёк — после первого успешного платежа аккаунт активируется автоматически.</p>
          </div>
        )}
        {isSuspended && (
          <div className="glass-card rounded-2xl p-5 mb-6 border border-red-500/30 bg-red-500/5">
            <p className="text-sm font-semibold text-red-300">Аккаунт приостановлен</p>
            <p className="text-xs text-red-300/80 mt-1">Баланс закончился. Пополните кошелёк — доступ восстановится после следующего ежемесячного списания.</p>
          </div>
        )}

        {/* Balance + topup */}
        <div className="glass-card rounded-2xl p-6 mb-6 border-2 border-brand/20">
          <div className="flex items-start justify-between gap-4 flex-wrap">
            <div>
              <p className="text-xs text-text-secondary uppercase tracking-wider mb-2">Баланс</p>
              <p className="text-4xl font-bold text-text-primary">{formatMoney(t.balance)}</p>
              <p className="text-xs text-text-secondary mt-2">
                Списание {t.max_users} мест × тариф «{t.plan}» каждое 1-е число месяца.
              </p>
            </div>
            {canBill ? (
              <button
                onClick={() => setTopupOpen(true)}
                className="px-5 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-sm font-semibold hover:shadow-lg hover:shadow-brand/25 transition-all min-h-[48px]"
              >Пополнить кошелёк</button>
            ) : (
              <span className="text-xs text-text-secondary">Пополнение доступно только администратору и биллинг-менеджеру</span>
            )}
          </div>
        </div>

        {/* Members */}
        <div className="glass-card rounded-2xl p-6 mb-6">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h2 className="text-lg font-semibold">Сотрудники</h2>
              <p className="text-xs text-text-secondary mt-0.5">
                {data.members.length} из {t.max_users || '∞'} мест занято
              </p>
            </div>
            {isAdmin && (
              <button
                onClick={() => { setInviteOpen(true); setInviteResult(null) }}
                className="px-4 py-2 rounded-xl bg-white/5 border border-white/10 hover:bg-white/10 text-sm font-medium transition min-h-[44px]"
              >+ Пригласить</button>
            )}
          </div>
          {data.members.length === 0 ? (
            <p className="text-sm text-text-secondary">Сотрудники не привязаны. Пригласите первого через кнопку выше.</p>
          ) : (
            <div className="space-y-2">
              {data.members.map(m => {
                const rl = roleLabel(m.role)
                return (
                  <div key={m.user_db_id} className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium truncate">{m.username ? `@${m.username}` : (m.email || m.user_id)}</p>
                      <p className="text-[11px] text-text-secondary truncate font-mono">{m.user_id}</p>
                    </div>
                    {isAdmin ? (
                      <select
                        value={m.role}
                        onChange={e => handleChangeRole(m, e.target.value)}
                        className="px-2 py-1 rounded-lg bg-white/5 border border-white/10 text-[11px] font-medium hover:bg-white/10 transition"
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
                      <button
                        onClick={() => handleRemoveMember(m)}
                        className="text-xs text-red-400 hover:text-red-300 transition"
                        title="Удалить из компании"
                      >Удалить</button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Invoices */}
        {data.invoices && data.invoices.length > 0 && (
          <div className="glass-card rounded-2xl p-6 mb-6">
            <h2 className="text-lg font-semibold mb-4">Счета</h2>
            <div className="space-y-2">
              {data.invoices.map(inv => (
                <div key={inv.id} className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-white/5 flex-wrap">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium">
                      Счёт #{inv.id} · {formatMoney(inv.amount)}
                      {' · '}<span className={`text-xs px-1.5 py-0.5 rounded ${
                        inv.status === 'paid' ? 'bg-green-500/15 text-green-400' :
                        inv.status === 'pending' ? 'bg-yellow-500/15 text-yellow-400' :
                        'bg-gray-500/15 text-gray-400'}`}>
                        {inv.status === 'paid' ? 'Оплачен' : inv.status === 'pending' ? 'Ожидание' : inv.status === 'cancelled' ? 'Отменён' : inv.status}
                      </span>
                    </p>
                    <p className="text-[11px] text-text-secondary mt-0.5 truncate">
                      ИНН {inv.inn} · {inv.legal_name}
                      {' · '}создан {formatDate(inv.created_at)}
                      {inv.paid_at && <> · оплачен {formatDate(inv.paid_at)}</>}
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Transactions */}
        <div className="glass-card rounded-2xl p-6">
          <h2 className="text-lg font-semibold mb-4">История операций</h2>
          {data.transactions.length === 0 ? (
            <p className="text-sm text-text-secondary">Пока пусто. Все пополнения и списания будут видны здесь.</p>
          ) : (
            <div className="space-y-2">
              {data.transactions.map(tx => (
                <div key={tx.id} className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-white/5">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium truncate">{tx.description || (tx.direction === 'credit' ? 'Пополнение' : 'Списание')}</p>
                    <p className="text-[11px] text-text-secondary mt-0.5">{formatDate(tx.created_at)}</p>
                  </div>
                  <span className={`font-semibold ${tx.direction === 'credit' ? 'text-green-400' : 'text-text-primary'}`}>
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
          <h2 className="text-lg font-bold mb-1">Пополнение кошелька</h2>
          <p className="text-sm text-text-secondary mb-5">Деньги придут после успешной оплаты в ЮKassa.</p>
          <label className="block text-xs text-text-secondary mb-1">Сумма, ₽</label>
          <input
            type="number" min="100" step="100" value={topupAmount}
            onChange={e => setTopupAmount(e.target.value)}
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:outline-none text-base"
          />
          <div className="flex gap-2 mt-2 flex-wrap">
            {[1000, 5000, 10000, 25000].map(v => (
              <button key={v} onClick={() => setTopupAmount(v)} className="px-3 py-1.5 rounded-lg bg-white/5 border border-white/10 hover:bg-white/10 text-xs">{v} ₽</button>
            ))}
          </div>
          <div className="flex gap-3 mt-6">
            <button onClick={() => setTopupOpen(false)} className="flex-1 px-4 py-3 rounded-xl bg-white/5 border border-white/10 text-sm">Отмена</button>
            <button
              onClick={handleTopup}
              disabled={topupLoading || !(topupAmount >= 100)}
              className="flex-1 px-4 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-sm font-semibold disabled:opacity-50"
            >{topupLoading ? '...' : 'Оплатить'}</button>
          </div>
        </ModalOverlay>
      )}

      {/* Invite modal */}
      {inviteOpen && (
        <ModalOverlay onClose={() => setInviteOpen(false)}>
          <h2 className="text-lg font-bold mb-1">Пригласить сотрудника</h2>
          <p className="text-sm text-text-secondary mb-5">Создаст одноразовую ссылку на 7 дней.</p>
          <label className="block text-xs text-text-secondary mb-1">Роль</label>
          <select
            value={inviteRole}
            onChange={e => setInviteRole(e.target.value)}
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:outline-none text-sm"
          >
            <option value="user">Сотрудник — только прокси-доступ</option>
            <option value="viewer">Только чтение — видит кабинет</option>
            <option value="billing">Биллинг-менеджер — может пополнять</option>
            <option value="admin">Админ — полный доступ</option>
          </select>
          {inviteResult ? (
            <div className="mt-5 p-4 rounded-xl bg-green-500/10 border border-green-500/30">
              <p className="text-xs text-green-300 uppercase tracking-wider mb-2">Ссылка создана</p>
              <code className="block font-mono text-xs break-all text-text-primary select-all">{inviteResult.claim_url}</code>
              <p className="text-[11px] text-text-secondary mt-2">Действует до {formatDate(inviteResult.expires_at)}</p>
              <button
                onClick={() => copy(inviteResult.claim_url, 'invite')}
                className="mt-3 w-full px-4 py-2.5 rounded-xl bg-white/5 border border-white/10 hover:bg-white/10 text-sm font-medium"
              >{copiedField === 'invite' ? '✓ Скопировано' : 'Копировать ссылку'}</button>
            </div>
          ) : (
            <div className="flex gap-3 mt-6">
              <button onClick={() => setInviteOpen(false)} className="flex-1 px-4 py-3 rounded-xl bg-white/5 border border-white/10 text-sm">Отмена</button>
              <button
                onClick={handleCreateInvite}
                disabled={inviteLoading}
                className="flex-1 px-4 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-sm font-semibold disabled:opacity-50"
              >{inviteLoading ? '...' : 'Создать ссылку'}</button>
            </div>
          )}
        </ModalOverlay>
      )}
    </section>
  )
}

function StatusBadge({ status }) {
  const map = {
    active:    { text: 'Активен',  cls: 'text-green-400' },
    inactive:  { text: 'Неактивен', cls: 'text-amber-400' },
    suspended: { text: 'Приостановлен', cls: 'text-red-400' },
  }
  const s = map[status] || { text: status, cls: 'text-text-secondary' }
  return <span className={`${s.cls} font-medium`}>{s.text}</span>
}

function ModalOverlay({ onClose, children }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div
        className="relative w-full max-w-md max-h-[90vh] overflow-y-auto rounded-2xl glass-strong border border-white/10 p-6"
        onClick={e => e.stopPropagation()}
      >
        <button
          onClick={onClose}
          className="absolute top-3 right-3 p-1.5 rounded-lg hover:bg-white/10 text-text-secondary hover:text-text-primary transition"
        >
          <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
        <div className="pr-8">{children}</div>
      </div>
    </div>
  )
}
