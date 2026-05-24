import { useState, useEffect, useCallback } from 'react'
import { CreditCard, X } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchSubscriptionsPaged, saveSubscription, deleteSubscription, fetchPlans } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatDateShort } from '../utils/format.js'
import {
  Button,
  Input,
  Select,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  Badge,
  TableSkeleton,
  EmptyState,
  Dialog,
} from '../components/ui/index.js'

const STATUS_VARIANT = {
  active: 'success',
  expired: 'danger',
  cancelled: 'default',
  pending_renewal: 'warning',
}

export default function SubscriptionsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [f, setF] = useUrlState({ search: '', plan_id: '', status: '', sort: 'valid_until', dir: 'desc', page: 1 })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 20

  const [plans, setPlans] = useState([])
  const [editing, setEditing] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [searchInput, setSearchInput] = useState(f.search)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchSubscriptionsPaged({
      page: f.page, pageSize, search: f.search,
      plan_id: f.plan_id, status: f.status,
      sort: f.sort, dir: f.dir,
    })
      .then(data => {
        setItems(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.search, f.plan_id, f.status, f.sort, f.dir])

  useEffect(() => { load() }, [load])

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
        plan_id: parseInt(editing.plan_id) || 0,
        valid_until: validUntil,
        auto_renew: editing.auto_renew,
        status: editing.status || 'active',
      })
      toast.success('Сохранено')
      setEditing(null)
      load()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteSubscription(confirm.user_db_id, confirm.group_id)
      toast.success('Удалено')
      setConfirm(null)
      load()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="Подписки"
        subtitle="Активные подписки пользователей — планы, статусы, промокоды"
        actions={can('subscriptions.write') ? (
          <Button variant="primary" onClick={openCreate}>+ Создать подписку</Button>
        ) : null}
      />

      {/* Filters */}
      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-3">
        <form onSubmit={onSearchSubmit} className="flex flex-col sm:flex-row sm:items-center gap-2 w-full sm:flex-1 sm:min-w-[200px] sm:max-w-md">
          <Input
            type="text"
            placeholder={t.search}
            value={searchInput}
            onChange={e => setSearchInput(e.target.value)}
            className="w-full sm:flex-1"
          />
          <Button type="submit" variant="secondary" className="w-full sm:w-auto">{t.search}</Button>
        </form>
        <Select
          value={f.plan_id}
          onChange={e => setF({ plan_id: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="План"
        >
          <option value="">Все планы</option>
          {plans.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
        </Select>
        <Select
          value={f.status}
          onChange={e => setF({ status: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="Статус"
        >
          <option value="">Все статусы</option>
          <option value="active">Active</option>
          <option value="expired">Expired</option>
          <option value="cancelled">Cancelled</option>
          <option value="pending_renewal">Pending Renewal</option>
        </Select>
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={8} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<CreditCard className="w-12 h-12" />}
          title="Нет подписок"
          description="Подписки пользователей появятся здесь"
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="user_id" sortState={sortState} onSort={onSort}>Пользователь</Th>
              <Th sortKey="plan_id" sortState={sortState} onSort={onSort}>План</Th>
              <Th>Статус</Th>
              <Th sortKey="valid_until" sortState={sortState} onSort={onSort}>Действует до</Th>
              <Th align="center">Оплата</Th>
              <Th>Промокод</Th>
              <Th align="center">Авто</Th>
              {can('subscriptions.write') && <Th>Действия</Th>}
            </Tr>
          </Thead>
          <Tbody>
            {items.map((s, i) => {
              const isExpired = s.valid_until && new Date(s.valid_until) < new Date()
              const statusKey = isExpired ? 'expired' : (s.status || 'active')
              return (
                <Tr key={`${s.user_db_id}-${s.group_id}-${i}`} className={isExpired ? 'opacity-50' : ''}>
                  <Td mono className="text-xs text-accent">{(s.user_id || '').slice(0, 16)}{s.user_id && s.user_id.length > 16 ? '…' : ''}</Td>
                  <Td>
                    {s.plan_name
                      ? <Badge variant="accent">{s.plan_name}</Badge>
                      : <span className="text-tertiary text-xs">{s.group_name || '—'}</span>}
                  </Td>
                  <Td>
                    <Badge variant={STATUS_VARIANT[statusKey] || 'default'}>{statusKey}</Badge>
                  </Td>
                  <Td>{formatDateShort(s.valid_until)}</Td>
                  <Td align="right" mono>
                    {s.price_paid > 0
                      ? <span className="text-success font-medium">{s.price_paid}₽</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td>
                    {s.promocode_used
                      ? <span className="font-mono text-xs text-warning">{s.promocode_used}</span>
                      : '—'}
                  </Td>
                  <Td align="center">
                    {s.auto_renew ? <span className="text-success">✓</span> : ''}
                  </Td>
                  {can('subscriptions.write') && (
                    <Td>
                      <div className="flex gap-1">
                        <Button size="sm" variant="ghost" onClick={() => openEdit(s)}>Ред.</Button>
                        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(s)}>Уд.</Button>
                      </div>
                    </Td>
                  )}
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}

      {/* Edit/Create dialog */}
      <Dialog
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing?._existing ? 'Редактировать подписку' : 'Создать подписку'}
        maxWidth="max-w-lg"
      >
        {editing && (
          <div className="space-y-4">
            <div>
              <label className="text-xs text-secondary block mb-1">Пользователь</label>
              {editing._existing ? (
                <p className="font-mono text-sm text-accent">{editing.user_id || `DB #${editing.user_db_id}`}</p>
              ) : (
                <Input
                  type="number"
                  value={editing.user_db_id}
                  onChange={e => setEditing({ ...editing, user_db_id: e.target.value })}
                  placeholder="User DB ID (число)"
                />
              )}
            </div>
            <div>
              <label className="text-xs text-secondary block mb-1">Тарифный план</label>
              <Select
                value={editing.plan_id || ''}
                onChange={e => setEditing({ ...editing, plan_id: parseInt(e.target.value) || 0 })}
              >
                <option value="">— без плана (legacy) —</option>
                {plans.map(p => (
                  <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽ / {p.duration_days}дн.)</option>
                ))}
              </Select>
            </div>
            <div>
              <label className="text-xs text-secondary block mb-1">Действует до</label>
              <Input
                type="date"
                value={editing.valid_until_date || (editing.valid_until ? editing.valid_until.slice(0, 10) : '')}
                onChange={e => setEditing({ ...editing, valid_until_date: e.target.value })}
              />
            </div>
            <div>
              <label className="text-xs text-secondary block mb-1">Статус</label>
              <Select
                value={editing.status || 'active'}
                onChange={e => setEditing({ ...editing, status: e.target.value })}
              >
                <option value="active">Active</option>
                <option value="expired">Expired</option>
                <option value="cancelled">Cancelled</option>
                <option value="pending_renewal">Pending Renewal</option>
              </Select>
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={editing.auto_renew}
                onChange={e => setEditing({ ...editing, auto_renew: e.target.checked })}
                className="rounded"
              />
              <span className="text-sm text-primary">Автопродление</span>
            </label>

            <div className="flex justify-end gap-2 pt-4 border-t border-default">
              <Button variant="ghost" onClick={() => setEditing(null)}>Отмена</Button>
              <Button variant="primary" onClick={handleSave} loading={saving}>Сохранить</Button>
            </div>
          </div>
        )}
      </Dialog>

      {confirm && (
        <ConfirmDialog
          open={true}
          message={`Удалить подписку ${confirm.user_id || confirm.user_db_id}?`}
          onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
        />
      )}
    </div>
  )
}
