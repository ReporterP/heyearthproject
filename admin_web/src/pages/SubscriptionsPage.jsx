import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchSubscriptionsPaged, saveSubscription, deleteSubscription, fetchPlans } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

function formatDate(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export default function SubscriptionsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchSubscriptionsPaged)

  const [plans, setPlans] = useState([])
  const [editing, setEditing] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    fetchPlans().then(p => setPlans(Array.isArray(p) ? p : [])).catch(() => {})
  }, [])

  function openCreate() {
    setEditing({
      user_db_id: '', user_id: '', group_id: 1, plan_id: plans[0]?.id || 1,
      valid_until_date: '', auto_renew: true, status: 'active',
    })
  }

  function openEdit(item) {
    setEditing({
      ...item,
      valid_until_date: item.valid_until ? item.valid_until.slice(0, 10) : '',
      _existing: true,
    })
  }

  async function handleSave() {
    if (!editing.user_db_id && !editing.user_id) { toast.error('User ID обязателен'); return }
    setSaving(true)
    try {
      const validUntil = editing.valid_until_date
        ? editing.valid_until_date + 'T23:59:59+03:00'
        : editing.valid_until
      await saveSubscription({
        user_db_id: parseInt(editing.user_db_id) || 0,
        group_id: parseInt(editing.group_id) || 1,
        valid_until: validUntil,
        auto_renew: editing.auto_renew,
      })
      toast.success('Сохранено')
      setEditing(null)
      paged.reload()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteSubscription(confirm.user_db_id, confirm.group_id)
      toast.success('Удалено')
      setConfirm(null)
      paged.reload()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-2xl font-bold">Подписки</h2>
          <p className="text-sm text-text-secondary mt-1">Активные подписки пользователей — планы, статусы, промокоды</p>
        </div>
        {can('subscriptions.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + Создать подписку
          </button>
        )}
      </div>

      <SearchPaginationBar search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage} loading={paged.loading} />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary py-8 text-center">Нет подписок</p>
      ) : (
        <div className="overflow-x-auto mt-4">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left text-xs uppercase">
                <th className="py-2 px-3">Пользователь</th>
                <th className="py-2 px-3">План</th>
                <th className="py-2 px-3">Статус</th>
                <th className="py-2 px-3">Действует до</th>
                <th className="py-2 px-3 text-center">Оплата</th>
                <th className="py-2 px-3">Промокод</th>
                <th className="py-2 px-3 text-center">Авто</th>
                {can('subscriptions.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map((s, i) => {
                const isExpired = s.valid_until && new Date(s.valid_until) < new Date()
                return (
                  <tr key={`${s.user_db_id}-${s.group_id}-${i}`} className={`border-b border-white/5 hover:bg-white/5 transition ${isExpired ? 'opacity-40' : ''}`}>
                    <td className="py-2 px-3">
                      <span className="font-mono text-xs text-brand">{(s.user_id || '').slice(0, 16)}...</span>
                    </td>
                    <td className="py-2 px-3">
                      {s.plan_name ? (
                        <span className="px-2 py-0.5 rounded bg-brand/10 text-brand text-xs font-medium">{s.plan_name}</span>
                      ) : (
                        <span className="text-text-secondary text-xs">{s.group_name || '—'}</span>
                      )}
                    </td>
                    <td className="py-2 px-3">
                      <StatusBadge status={isExpired ? 'expired' : (s.status || 'active')} />
                    </td>
                    <td className="py-2 px-3">{formatDate(s.valid_until)}</td>
                    <td className="py-2 px-3 text-center">
                      {s.price_paid > 0 ? <span className="text-green-400 font-medium">{s.price_paid}₽</span> : <span className="text-text-secondary">—</span>}
                    </td>
                    <td className="py-2 px-3">
                      {s.promocode_used ? <span className="font-mono text-xs text-amber-400">{s.promocode_used}</span> : '—'}
                    </td>
                    <td className="py-2 px-3 text-center">
                      {s.auto_renew ? <span className="text-green-400">✓</span> : ''}
                    </td>
                    {can('subscriptions.write') && (
                      <td className="py-2 px-3">
                        <div className="flex gap-2">
                          <button onClick={() => openEdit(s)} className="text-brand text-xs hover:underline">Ред.</button>
                          <button onClick={() => setConfirm(s)} className="text-red-400 text-xs hover:underline">Уд.</button>
                        </div>
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Edit/Create Modal */}
      {editing && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={() => setEditing(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-lg p-6" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-5">
              <h3 className="text-lg font-semibold">{editing._existing ? 'Редактировать подписку' : 'Создать подписку'}</h3>
              <button onClick={() => setEditing(null)} className="text-text-secondary hover:text-text-primary">✕</button>
            </div>

            {/* User */}
            <div className="mb-4">
              <label className="text-xs text-text-secondary block mb-1">Пользователь</label>
              {editing._existing ? (
                <p className="font-mono text-sm text-brand">{editing.user_id || `DB #${editing.user_db_id}`}</p>
              ) : (
                <input type="number" value={editing.user_db_id} onChange={e => setEditing({...editing, user_db_id: e.target.value})}
                  placeholder="User DB ID (число)" className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
              )}
            </div>

            {/* Plan */}
            <div className="mb-4">
              <label className="text-xs text-text-secondary block mb-1">Тарифный план</label>
              <select value={editing.plan_id || ''} onChange={e => {
                const pid = parseInt(e.target.value) || 0
                const plan = plans.find(p => p.id === pid)
                setEditing({...editing, plan_id: pid, group_id: editing.group_id})
              }} className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm">
                <option value="">— без плана (legacy) —</option>
                {plans.map(p => (
                  <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽ / {p.duration_days}дн.)</option>
                ))}
              </select>
            </div>

            {/* Valid until */}
            <div className="mb-4">
              <label className="text-xs text-text-secondary block mb-1">Действует до</label>
              <input type="date" value={editing.valid_until_date || (editing.valid_until ? editing.valid_until.slice(0, 10) : '')}
                onChange={e => setEditing({...editing, valid_until_date: e.target.value})}
                className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
            </div>

            {/* Status */}
            <div className="mb-4">
              <label className="text-xs text-text-secondary block mb-1">Статус</label>
              <select value={editing.status || 'active'} onChange={e => setEditing({...editing, status: e.target.value})}
                className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm">
                <option value="active">Active</option>
                <option value="expired">Expired</option>
                <option value="cancelled">Cancelled</option>
                <option value="pending_renewal">Pending Renewal</option>
              </select>
            </div>

            {/* Auto renew */}
            <label className="flex items-center gap-2 mb-5 cursor-pointer">
              <input type="checkbox" checked={editing.auto_renew} onChange={e => setEditing({...editing, auto_renew: e.target.checked})} className="rounded" />
              <span className="text-sm">Автопродление</span>
            </label>

            <div className="flex justify-end gap-3 pt-4 border-t border-white/10">
              <button onClick={() => setEditing(null)} className="px-4 py-2 rounded-lg bg-surface-alt text-text-secondary text-sm">Отмена</button>
              <button onClick={handleSave} disabled={saving} className="px-6 py-2 rounded-lg bg-brand text-white text-sm font-medium disabled:opacity-50">
                {saving ? 'Сохранение...' : 'Сохранить'}
              </button>
            </div>
          </div>
        </div>
      )}

      {confirm && (
        <ConfirmDialog open={true}
          message={`Удалить подписку ${confirm.user_id || confirm.user_db_id}?`}
          onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving} />
      )}
    </div>
  )
}
