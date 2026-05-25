import { useState, useEffect, useCallback } from 'react'
import { Pencil, Plus, X } from 'lucide-react'
import { useT } from '../../i18n/index.jsx'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchUserSubscriptions, saveSubscription, updateSubscription, deleteSubscription, fetchPlans } from '../../api/admin.js'
import { Button, Input, Select, Badge, Table, Thead, Tbody, Tr, Th, Td, EmptyState } from '../ui/index.js'
import ConfirmDialog from '../ConfirmDialog.jsx'
import { formatDateShort } from '../../utils/format.js'

const STATUS_VARIANT = { active: 'success', expired: 'danger', cancelled: 'default', pending_renewal: 'warning' }
const STATUS_OPTIONS = [
  { value: 'active', label: 'active' },
  { value: 'expired', label: 'expired' },
  { value: 'cancelled', label: 'cancelled' },
  { value: 'pending_renewal', label: 'pending_renewal' },
]

export default function SubscriptionsTab({ user, onChanged }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [items, setItems] = useState([])
  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState(null)
  const [busy, setBusy] = useState(false)

  // Inline create / edit form state.
  // `editing` is null for closed, 'new' for create mode, or a row object for edit mode.
  const [editing, setEditing] = useState(null)
  const [draft, setDraft] = useState({ plan_id: '', valid_until_date: '', auto_renew: true, status: 'active' })

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchUserSubscriptions(user.id)
      .then(data => setItems(data.items || []))
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [user.id])

  useEffect(() => { load() }, [load])
  useEffect(() => { fetchPlans().then(p => setPlans(Array.isArray(p) ? p : [])).catch(() => {}) }, [])

  function openCreate() {
    const defaultDate = new Date()
    defaultDate.setDate(defaultDate.getDate() + 30)
    setDraft({
      plan_id: plans[0]?.id || '',
      valid_until_date: defaultDate.toISOString().slice(0, 10),
      auto_renew: true,
      status: 'active',
    })
    setEditing('new')
  }

  function openEdit(row) {
    setDraft({
      plan_id: row.plan_id || '',
      valid_until_date: row.valid_until ? row.valid_until.slice(0, 10) : '',
      auto_renew: !!row.auto_renew,
      status: row.status || 'active',
    })
    setEditing(row)
  }

  async function submitForm(e) {
    e.preventDefault()
    setBusy(true)
    try {
      const isNew = editing === 'new'
      if (isNew) {
        await saveSubscription({
          user_db_id: user.id,
          plan_id: Number(draft.plan_id) || null,
          valid_until: draft.valid_until_date ? new Date(draft.valid_until_date).toISOString() : null,
          auto_renew: !!draft.auto_renew,
          status: draft.status,
        })
        toast.success(t.create + ' OK')
      } else {
        await updateSubscription(editing.id, {
          plan_id: Number(draft.plan_id) || 0,
          valid_until: draft.valid_until_date ? new Date(draft.valid_until_date).toISOString() : '',
          auto_renew: !!draft.auto_renew,
          status: draft.status,
        })
        toast.success(t.save + ' OK')
      }
      setEditing(null)
      load()
      onChanged?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    setBusy(true)
    try {
      await deleteSubscription(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      load()
      onChanged?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-primary">{t.drawerTabSubs}</h3>
        {can('subscriptions.write') && !editing && (
          <Button size="sm" variant="primary" onClick={openCreate} icon={<Plus className="w-3.5 h-3.5" />}>
            {t.drawerAddSub || '+ Подписка'}
          </Button>
        )}
      </div>

      {editing && (
        <form onSubmit={submitForm} className="rounded-md border border-default bg-elevated p-3 space-y-2">
          <p className="text-xs text-tertiary">
            {editing === 'new' ? (t.create || 'Создать') : `${t.edit || 'Ред.'} #${editing.id}`}
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label className="text-xs text-tertiary">
              {t.fieldPlan || 'Тариф'}
              <Select value={draft.plan_id} onChange={e => setDraft({ ...draft, plan_id: e.target.value })} className="w-full mt-1">
                <option value="">—</option>
                {plans.map(p => <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽ / {p.duration_days}д)</option>)}
              </Select>
            </label>
            <label className="text-xs text-tertiary">
              {t.fieldValidUntil || 'Действует до'}
              <Input type="date" value={draft.valid_until_date} onChange={e => setDraft({ ...draft, valid_until_date: e.target.value })} className="w-full mt-1" />
            </label>
            <label className="text-xs text-tertiary">
              {t.fieldStatus || 'Статус'}
              <Select value={draft.status} onChange={e => setDraft({ ...draft, status: e.target.value })} className="w-full mt-1">
                {STATUS_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </Select>
            </label>
            <label className="text-xs text-tertiary flex items-center gap-2 mt-5">
              <input type="checkbox" checked={draft.auto_renew} onChange={e => setDraft({ ...draft, auto_renew: e.target.checked })} />
              {t.fieldAutoRenew || 'Авто-продление'}
            </label>
          </div>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(null)}>{t.cancel}</Button>
            <Button size="sm" variant="primary" type="submit" loading={busy}>
              {editing === 'new' ? t.create : t.save}
            </Button>
          </div>
        </form>
      )}

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
      ) : items.length === 0 ? (
        <EmptyState title={t.noSubscriptions || 'Подписок нет'} description={t.noSubscriptionsDesc || 'Добавьте первую через кнопку выше'} />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>{t.fieldPeriod || 'Период'}</Th>
              <Th>{t.fieldPlan || 'План'}</Th>
              <Th align="right">{t.fieldPrice || 'Цена'}</Th>
              <Th>{t.fieldPromo || 'Промокод'}</Th>
              <Th>{t.fieldStatus || 'Статус'}</Th>
              <Th></Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map(s => (
              <Tr key={s.id}>
                <Td className="text-xs">
                  {formatDateShort(s.started_at)} → {formatDateShort(s.valid_until)}
                </Td>
                <Td>{s.plan_name || s.plan || '—'}</Td>
                <Td mono align="right">
                  {s.price_paid > 0 ? `${s.price_paid}₽` : (s.price_rub != null ? `${s.price_rub}₽` : '—')}
                </Td>
                <Td mono className="text-xs">{s.promocode_used || s.promocode || '—'}</Td>
                <Td><Badge variant={STATUS_VARIANT[s.status] || 'default'}>{s.status}</Badge></Td>
                <Td align="right">
                  {can('subscriptions.write') && (
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => openEdit(s)}
                        aria-label={t.edit}
                        className="text-tertiary hover:text-primary transition p-1 rounded hover:bg-hover"
                      >
                        <Pencil className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={() => setConfirm(s)}
                        aria-label={t.delete}
                        className="text-tertiary hover:text-danger transition p-1 rounded hover:bg-hover"
                      >
                        <X className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  )}
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}

      <ConfirmDialog
        open={!!confirm}
        message={t.deleteSubscriptionConfirm ? t.deleteSubscriptionConfirm(confirm?.id) : `Удалить подписку #${confirm?.id}?`}
        onConfirm={handleDelete}
        onCancel={() => setConfirm(null)}
        loading={busy}
      />
    </div>
  )
}
