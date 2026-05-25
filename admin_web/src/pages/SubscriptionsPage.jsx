import { useState, useEffect, useCallback, useMemo } from 'react'
import { CreditCard } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchSubscriptionsPaged, deleteSubscription, fetchPlans } from '../api/admin.js'
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
  const navigate = useNavigate()

  const [f, setF] = useUrlState({
    search: '', plan_id: '', status: '',
    expiring_days: '', with_promo: '',
    sort: 'valid_until', dir: 'desc', page: 1,
  })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 20

  const [plans, setPlans] = useState([])
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

  // Client-side filter — backend doesn't support expiring_in_days / with_promo yet.
  // Applied after fetch on the current page (acceptable for audit view).
  const visibleItems = useMemo(() => {
    let out = items
    if (f.expiring_days) {
      const days = parseInt(f.expiring_days, 10)
      if (days > 0) {
        const now = Date.now()
        const cutoff = now + days * 86400000
        out = out.filter(s => {
          if (!s.valid_until) return false
          const ts = new Date(s.valid_until).getTime()
          return ts >= now && ts <= cutoff
        })
      }
    }
    if (f.with_promo === '1') {
      out = out.filter(s => !!s.promocode_used)
    }
    return out
  }, [items, f.expiring_days, f.with_promo])

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteSubscription(confirm.id)
      toast.success('Удалено')
      setConfirm(null)
      load()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  function openUserDrawer(s) {
    if (!s.user_db_id) return
    navigate(`/users?open=${s.user_db_id}&tab=subscriptions`)
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="История подписок"
        subtitle="Полная история подписок всех пользователей — для аудита и анализа. Создание новых подписок — на странице пользователя."
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
        <Select
          value={f.expiring_days}
          onChange={e => setF({ expiring_days: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="Истекают в течение"
        >
          <option value="">Истекают: любые</option>
          <option value="3">Истекают за 3 дня</option>
          <option value="7">Истекают за 7 дней</option>
          <option value="14">Истекают за 14 дней</option>
          <option value="30">Истекают за 30 дней</option>
        </Select>
        <label className="flex items-center gap-1.5 text-sm text-secondary cursor-pointer select-none">
          <input
            type="checkbox"
            checked={f.with_promo === '1'}
            onChange={e => setF({ with_promo: e.target.checked ? '1' : '', page: 1 })}
            className="rounded"
          />
          С промокодом
        </label>
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : visibleItems.length === 0 ? (
        <EmptyState
          icon={<CreditCard className="w-12 h-12" />}
          title="Нет подписок"
          description={
            f.expiring_days || f.with_promo === '1' || f.search || f.plan_id || f.status
              ? 'По текущим фильтрам ничего не найдено'
              : 'История подписок появится здесь, как только пользователи начнут оформлять планы'
          }
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="user_id" sortState={sortState} onSort={onSort}>Пользователь</Th>
              <Th sortKey="plan_id" sortState={sortState} onSort={onSort}>План</Th>
              <Th>Статус</Th>
              <Th sortKey="valid_until" sortState={sortState} onSort={onSort}>Действует до</Th>
              <Th align="right">Оплата</Th>
              <Th>Промокод</Th>
              <Th align="center">Авто</Th>
              <Th align="right">Действия</Th>
            </Tr>
          </Thead>
          <Tbody>
            {visibleItems.map((s, i) => {
              const isExpired = s.valid_until && new Date(s.valid_until) < new Date()
              const statusKey = isExpired ? 'expired' : (s.status || 'active')
              return (
                <Tr
                  key={s.id ?? `${s.user_db_id}-${i}`}
                  className={`cursor-pointer hover:bg-hover ${isExpired ? 'opacity-60' : ''}`}
                  onClick={() => openUserDrawer(s)}
                >
                  <Td mono className="text-xs text-accent">
                    {(s.user_id || '').slice(0, 16)}{s.user_id && s.user_id.length > 16 ? '…' : ''}
                  </Td>
                  <Td>
                    {s.plan_name
                      ? <Badge variant="accent">{s.plan_name}</Badge>
                      : <span className="text-tertiary text-xs">—</span>}
                  </Td>
                  <Td>
                    <Badge variant={STATUS_VARIANT[statusKey] || 'default'}>{statusKey}</Badge>
                  </Td>
                  <Td mono>{formatDateShort(s.valid_until)}</Td>
                  <Td align="right" mono>
                    {s.price_paid > 0
                      ? <span className="text-success font-medium">{s.price_paid}₽</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td>
                    {s.promocode_used
                      ? <span className="font-mono text-xs text-warning">{s.promocode_used}</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="center">
                    {s.auto_renew ? <span className="text-success">✓</span> : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="right">
                    <div className="flex gap-1 justify-end" onClick={e => e.stopPropagation()}>
                      <Button size="sm" variant="ghost" onClick={() => openUserDrawer(s)}>
                        Открыть юзера
                      </Button>
                      {can('subscriptions.write') && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          onClick={() => setConfirm(s)}
                          aria-label="Удалить"
                        >
                          ✕
                        </Button>
                      )}
                    </div>
                  </Td>
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary tabular-nums">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}

      {confirm && (
        <ConfirmDialog
          open={true}
          message={`Удалить подписку ${confirm.user_id || confirm.user_db_id}?`}
          onConfirm={handleDelete}
          onCancel={() => setConfirm(null)}
          loading={saving}
        />
      )}
    </div>
  )
}
