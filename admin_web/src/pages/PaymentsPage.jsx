import { useState, useEffect, useCallback, useMemo } from 'react'
import { Wallet, Copy as CopyIcon, Check } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchPaymentsPaged, createPayment, updatePayment, checkPaymentStatus, syncPendingPayments } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import ManualPaymentDialog from '../components/payments/ManualPaymentDialog.jsx'
import FailingRenewals from '../components/payments/FailingRenewals.jsx'
import EmailLog from '../components/payments/EmailLog.jsx'
import RefundDialog from '../components/payments/RefundDialog.jsx'
import { formatDate } from '../utils/format.js'
import { refundableAmount } from '../utils/refund.js'
import { paymentKindLabel } from '../utils/wallet.js'
import { paymentStatusName } from '../utils/protocols.js'
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
  EmptyState, Tabs, TabPanel,
} from '../components/ui/index.js'

const STATUS_OPTIONS = [
  { value: '', label: 'Все статусы' },
  ...['succeeded', 'pending', 'waiting_for_capture', 'canceled', 'failed', 'refunded'].map((value) => ({ value, label: paymentStatusName(value) })),
]

const STATUS_VARIANT = {
  succeeded: 'success',
  completed: 'success',
  pending: 'warning',
  waiting_for_capture: 'warning',
  failed: 'danger',
  canceled: 'danger',
}

function PaymentMethodIcon({ type, details }) {
  const LABELS = {
    bank_card: 'Карта',
    sbp: 'СБП',
    yoo_money: 'ЮMoney',
    tinkoff_bank: 'T-Pay',
    sberbank: 'Сбер',
    alfabank: 'Alfa',
    installments: 'Части',
    apple_pay: 'Apple Pay',
    google_pay: 'Google Pay',
  }
  const label = LABELS[type] || type || '—'
  let last4 = ''
  try {
    if (details && typeof details === 'object' && details.last4) last4 = details.last4
  } catch {}
  return (
    <Badge variant="default">
      <span className="font-medium">{label}</span>
      {last4 && <span className="text-tertiary ml-1">•••• {last4}</span>}
    </Badge>
  )
}

function TruncatedId({ value }) {
  const [copied, setCopied] = useState(false)
  if (!value) return <span className="text-tertiary">—</span>

  const display = value.length > 16 ? value.slice(0, 8) + '…' + value.slice(-6) : value

  function handleCopy() {
    navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <button
      onClick={handleCopy}
      title={value}
      aria-label="Копировать ID"
      className="text-left font-mono text-xs hover:text-accent transition inline-flex items-center gap-1 group"
    >
      <span>{display}</span>
      {copied ? <Check className="w-3 h-3 text-success" /> : <CopyIcon className="w-3 h-3 opacity-0 group-hover:opacity-100 transition-opacity text-tertiary" />}
    </button>
  )
}

function MetadataPopover({ data }) {
  const [open, setOpen] = useState(false)
  if (!data || (typeof data === 'object' && Object.keys(data).length === 0)) {
    return <span className="text-tertiary">—</span>
  }

  const jsonStr = typeof data === 'string' ? data : JSON.stringify(data, null, 2)

  return (
    <div className="relative inline-block">
      <Button size="sm" variant="ghost" onClick={() => setOpen(!open)}>
        {open ? 'Скрыть' : 'JSON'}
      </Button>
      {open && (
        <div className="absolute z-30 top-full left-0 mt-1 p-3 rounded bg-overlay border border-default min-w-[280px] max-w-[400px] max-h-[300px] overflow-auto">
          <pre className="text-xs text-primary whitespace-pre-wrap break-all font-mono">{jsonStr}</pre>
        </div>
      )}
    </div>
  )
}

function PaymentsList() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [f, setF] = useUrlState({ status: '', from: '', to: '', sort: 'created_at', dir: 'desc', page: 1 })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [realTotal, setRealTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 20

  const [dialog, setDialog] = useState(null) // { mode: 'create' } | { mode: 'edit', item } — manual payments only
  const [refundFor, setRefundFor] = useState(null) // payment in the refund dialog
  const [checkingId, setCheckingId] = useState(null) // payment whose YooKassa status is being fetched
  const canActions = can('payments.write') || can('payments.refund')


  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchPaymentsPaged({
      page: f.page, pageSize,
      status: f.status, from: f.from, to: f.to,
      sort: f.sort, dir: f.dir,
    })
      .then(data => {
        setItems(data.items || [])
        // An open refund dialog follows the fresh row (refundable_amount may have changed).
        setRefundFor(cur => cur && ((data.items || []).find(p => p.id === cur.id) || cur))
        setTotal(data.total || 0)
        setRealTotal(data.real_total_amount ?? 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.status, f.from, f.to, f.sort, f.dir])

  useEffect(() => { load() }, [load])

  const visibleSum = useMemo(() => {
    return items.reduce((sum, p) => sum + (parseFloat(p.amount) || 0), 0)
  }, [items])

  async function saveManual(body) {
    if (dialog.mode === 'create') await createPayment(body)
    else await updatePayment(dialog.item.id, body)
    toast.success(dialog.mode === 'create' ? 'Ручной платёж записан' : 'Сохранено')
    setDialog(null)
    load()
  }

  async function handleSyncPending() {
    try {
      const d = await syncPendingPayments()
      toast.success(d?.message || 'Проверка запущена')
      load()
    } catch (e) { toast.error(e.message) }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-4">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
        <p className="text-sm text-secondary">Платежей: <span className="font-mono tabular-nums">{total}</span></p>
        {can('payments.write') && (
          <div className="flex flex-col sm:flex-row gap-2">
            <Button variant="secondary" onClick={handleSyncPending} title="Спросить у ЮKassa статус всех ожидающих платежей">
              Проверить ожидающие
            </Button>
            <Button variant="primary" onClick={() => setDialog({ mode: 'create' })}>+ Ручной платёж</Button>
          </div>
        )}
      </div>

      {/* Stats panel */}
      <div className="px-4 py-3 rounded border border-default bg-elevated/40 flex items-center gap-6 flex-wrap">
        <div>
          <span className="text-sm text-secondary">{t.paymentsRealRevenue || 'Реальный доход'}:</span>
          <span className="ml-2 text-lg font-bold text-success tabular-nums">
            {realTotal.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </span>
          <span className="ml-1 text-sm text-tertiary">RUB</span>
          <span className="ml-2 text-xs text-tertiary">({t.paymentsSucceededNonTest || 'succeeded, не test'})</span>
        </div>
        <div className="h-6 w-px bg-default" />
        <div>
          <span className="text-sm text-secondary">{t.paymentsVisibleSum || 'Сумма видимых'}:</span>
          <span className="ml-2 text-base font-semibold text-primary tabular-nums">
            {visibleSum.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </span>
          <span className="ml-1 text-sm text-tertiary">{items[0]?.currency || 'RUB'}</span>
        </div>
        <div className="text-sm text-tertiary">
          {t.paymentsShowing || 'Показано'}: {items.length}
        </div>
      </div>

      {/* Filters */}
      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-end gap-3">
        <div className="w-full sm:w-auto">
          <label className="block text-xs text-secondary mb-1">{t.fieldStatus}</label>
          <Select
            value={f.status}
            onChange={e => setF({ status: e.target.value, page: 1 })}
            className="w-full sm:w-48"
          >
            {STATUS_OPTIONS.map(o => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </Select>
        </div>
        <div className="w-full sm:w-auto">
          <label className="block text-xs text-secondary mb-1">С даты</label>
          <Input
            type="date"
            value={f.from}
            onChange={e => setF({ from: e.target.value, page: 1 })}
            className="w-full sm:w-44"
          />
        </div>
        <div className="w-full sm:w-auto">
          <label className="block text-xs text-secondary mb-1">По дату</label>
          <Input
            type="date"
            value={f.to}
            onChange={e => setF({ to: e.target.value, page: 1 })}
            className="w-full sm:w-44"
          />
        </div>
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={10} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Wallet className="w-12 h-12" />}
          title={t.noPayments || 'Платежей нет'}
          description="Платежи будут отображаться здесь после оплаты"
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="id" sortState={sortState} onSort={onSort}>ID</Th>
              <Th sortKey="user_id" sortState={sortState} onSort={onSort}>User</Th>
              <Th>Тип</Th>
              <Th sortKey="amount" sortState={sortState} onSort={onSort} align="right">{t.fieldAmount}</Th>
              <Th sortKey="status" sortState={sortState} onSort={onSort}>{t.fieldStatus}</Th>
              <Th>Метод</Th>
              <Th align="right">Возврат</Th>
              <Th align="center">Чек</Th>
              <Th sortKey="created_at" sortState={sortState} onSort={onSort}>{t.paymentsDate || 'Дата'}</Th>
              <Th>ЮKassa ID</Th>
              <Th>{t.fieldMetadata}</Th>
              {canActions && <Th>Действия</Th>}
            </Tr>
          </Thead>
          <Tbody>
            {items.map(p => {
              const refunded = parseFloat(p.refunded_amount || 0)
              const cancelReason = p.cancellation_reason || ''
              let metaObj = {}
              try { metaObj = typeof p.metadata === 'string' ? JSON.parse(p.metadata) : (p.metadata || {}) } catch {}
              const isRecovered = metaObj.recovered === true || metaObj.recovered === 'true'
              return (
                <Tr key={p.id}>
                  <Td mono>
                    {p.id}
                    {p.test && <span className="ml-1 text-[9px] text-warning align-super">TEST</span>}
                    {isRecovered && <span className="ml-1 text-[9px] text-info align-super" title="Платёж восстановлен через orphan recovery">RECOVERED</span>}
                  </Td>
                  <Td mono className="text-xs">{p.user_id || p.user_db_id || '—'}</Td>
                  <Td>
                    <Badge variant={metaObj.kind === 'wallet_topup' ? 'accent' : 'default'}>{paymentKindLabel(metaObj.kind)}</Badge>
                    {metaObj.purchase_status === 'failed' && <div className="text-[10px] text-warning mt-0.5" title={metaObj.purchase_reason || ''}>покупка не прошла</div>}
                  </Td>
                  <Td align="right" mono className="font-medium">
                    {parseFloat(p.amount).toLocaleString('ru-RU', { minimumFractionDigits: 2 })} {p.currency}
                  </Td>
                  <Td>
                    <span title={cancelReason ? `Причина отмены: ${cancelReason}` : undefined}>
                      <Badge variant={STATUS_VARIANT[p.status] || 'default'}>{paymentStatusName(p.status)}</Badge>
                    </span>
                    {cancelReason && p.status === 'canceled' && (
                      <div className="text-[10px] text-danger/80 mt-0.5 max-w-[140px] truncate" title={cancelReason}>
                        {cancelReason}
                      </div>
                    )}
                  </Td>
                  <Td className="text-xs">
                    {p.payment_method_type
                      ? <PaymentMethodIcon type={p.payment_method_type} details={p.payment_method_details} />
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="right" mono className="text-xs">
                    {refunded > 0
                      ? <span className="text-warning">{refunded.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="center">
                    {p.receipt_registered
                      ? <span className="text-success" title="Чек зарегистрирован в ОФД">✓</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td className="text-tertiary text-xs whitespace-nowrap">{formatDate(p.created_at)}</Td>
                  <Td><TruncatedId value={p.yookassa_payment_id || p.external_id} /></Td>
                  <Td><MetadataPopover data={p.metadata} /></Td>
                  {canActions && (
                    <Td>
                      <div className="flex gap-1">
                        {can('payments.write') && p.provider === 'manual' && <Button size="sm" variant="ghost" onClick={() => setDialog({ mode: 'edit', item: p })}>Изменить</Button>}
                        {can('payments.refund') && (p.yookassa_payment_id || p.external_id) && (
                          <>
                            <Button size="sm" variant="ghost" title="Запросить статус в ЮKassa"
                              loading={checkingId === p.id} disabled={checkingId !== null} onClick={async () => {
                              setCheckingId(p.id)
                              try {
                                const res = await checkPaymentStatus(p.id)
                                toast.success(`ЮKassa: ${res.yookassa_status || res.local_status}`)
                              } catch (e) {
                                // The server's own text (e.g. a paid payment it could not apply).
                                toast.error(e.message)
                              } finally {
                                setCheckingId(null)
                                // A 500 may still have recorded the YooKassa status or a refund.
                                load()
                              }
                            }}>
                              Статус
                            </Button>
                            {/* refundable_amount is 0 unless the server can refund the payment. */}
                            {refundableAmount(p) > 0 && (
                              <Button size="sm" variant="ghost" className="text-danger" onClick={() => setRefundFor(p)}>
                                Вернуть
                              </Button>
                            )}
                          </>
                        )}
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

      {dialog && <ManualPaymentDialog dialog={dialog} onClose={() => setDialog(null)} onSave={saveManual} />}

      {refundFor && (
        <RefundDialog
          payment={refundFor}
          onClose={() => setRefundFor(null)}
          onDone={(msg, level) => {
            setRefundFor(null)
            if (level === 'warn') toast.warn(msg)
            else toast.success(msg)
            load()
          }}
          onRefresh={load}
        />
      )}
    </div>
  )
}

const PAYMENT_TABS = [
  { key: 'payments', label: 'Платежи' },
  { key: 'renewals', label: 'Не прошло продление' },
  { key: 'emails', label: 'Письма' },
]

/**
 * PaymentsPage — «Платежи»: the payments with refunds and manual records, the subscriptions the
 * wallet cannot renew (C10: what «Дунинг» was) and the email log (was «Письма»).
 */
export default function PaymentsPage() {
  const [f, setF] = useUrlState({ tab: 'payments' })
  const tab = PAYMENT_TABS.some((x) => x.key === f.tab) ? f.tab : 'payments'
  return (
    <div className="space-y-4">
      <PageHeader title="Платежи" subtitle="Оплаты и возвраты через ЮKassa, кошельки, письма клиентам" />
      <Tabs id="payments-tab" label="Раздел платежей" tabs={PAYMENT_TABS} value={tab} onChange={(k) => setF({ tab: k })} />
      <TabPanel tabsId="payments-tab" tabKey={tab}>
        {tab === 'payments' && <PaymentsList />}
        {tab === 'renewals' && <FailingRenewals />}
        {tab === 'emails' && <EmailLog />}
      </TabPanel>
    </div>
  )
}
