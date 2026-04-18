import { useState, useMemo } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchPaymentsPaged, createPayment, updatePayment, cancelPayment, checkPaymentStatus, resendPaymentNotifications } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, TextArea, SelectInput } from '../components/CrudDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'
import { formatDate } from '../utils/format.js'

const STATUS_OPTIONS = [
  { value: '', label: 'Все' },
  { value: 'pending', label: 'Pending' },
  { value: 'succeeded', label: 'Succeeded' },
  { value: 'failed', label: 'Failed' },
  { value: 'canceled', label: 'Canceled' },
  { value: 'waiting_for_capture', label: 'Waiting for capture' },
]

const AMOUNT_COLORS = {
  succeeded: 'text-green-400',
  pending: 'text-yellow-400',
  waiting_for_capture: 'text-yellow-400',
  failed: 'text-red-400',
  canceled: 'text-red-400',
}


// Иконка способа оплаты ЮKassa
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
    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface-alt border border-white/10 text-xs">
      <span className="font-medium">{label}</span>
      {last4 && <span className="text-text-secondary">•••• {last4}</span>}
    </span>
  )
}

function TruncatedId({ value }) {
  const [copied, setCopied] = useState(false)
  if (!value) return <span className="text-text-secondary">{'\u2014'}</span>

  const display = value.length > 16 ? value.slice(0, 8) + '\u2026' + value.slice(-6) : value

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
      className="text-left font-mono text-xs hover:text-brand transition inline-flex items-center gap-1 group"
    >
      <span>{display}</span>
      <svg className="w-3.5 h-3.5 opacity-0 group-hover:opacity-100 transition-opacity text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
      </svg>
      {copied && <span className="text-green-400 text-[10px]">{'\u2713'}</span>}
    </button>
  )
}

function MetadataPopover({ data }) {
  const [open, setOpen] = useState(false)
  if (!data || (typeof data === 'object' && Object.keys(data).length === 0)) {
    return <span className="text-text-secondary">{'\u2014'}</span>
  }

  const jsonStr = typeof data === 'string' ? data : JSON.stringify(data, null, 2)

  return (
    <div className="relative inline-block">
      <button
        onClick={() => setOpen(!open)}
        className="px-2 py-0.5 rounded bg-surface-alt border border-white/10 text-xs text-text-secondary hover:text-text-primary hover:border-white/20 transition"
      >
        {open ? 'Скрыть' : 'JSON'}
      </button>
      {open && (
        <div className="absolute z-30 top-full left-0 mt-1 p-3 rounded-lg bg-surface-card border border-white/10 shadow-xl min-w-[280px] max-w-[400px] max-h-[300px] overflow-auto">
          <pre className="text-xs text-text-primary whitespace-pre-wrap break-all font-mono">{jsonStr}</pre>
        </div>
      )}
    </div>
  )
}

export default function PaymentsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchPaymentsPaged)

  const [dialog, setDialog] = useState(null)
  const [saving, setSaving] = useState(false)
  const [statusFilter, setStatusFilter] = useState('')

  const [userDbId, setUserDbId] = useState('')
  const [amount, setAmount] = useState('')
  const [currency, setCurrency] = useState('RUB')
  const [status, setStatus] = useState('pending')
  const [provider, setProvider] = useState('manual')
  const [externalId, setExternalId] = useState('')
  const [notes, setNotes] = useState('')
  const [metadata, setMetadata] = useState('')

  // Фильтрация по статусу на клиенте
  const filteredItems = useMemo(() => {
    if (!statusFilter) return paged.items
    return paged.items.filter(p => p.status === statusFilter)
  }, [paged.items, statusFilter])

  // Реальный доход: сумма succeeded + НЕ тестовых платежей по текущему фильтру
  // (считает сервер по всему result-set, а не по видимой странице).
  const realTotal = paged.real_total_amount ?? 0
  // Для справки — сумма видимых записей (любые статусы/test).
  const visibleSum = useMemo(() => {
    return filteredItems.reduce((sum, p) => sum + (parseFloat(p.amount) || 0), 0)
  }, [filteredItems])

  function openCreate() {
    setUserDbId('')
    setAmount('')
    setCurrency('RUB')
    setStatus('pending')
    setProvider('manual')
    setExternalId('')
    setNotes('')
    setMetadata('')
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    // Backend returns user_db_id (numeric, 0 if not set) and user_id (text GUID).
    // Many payments from YooKassa have only user_id_text (GUID) and user_db_id=0.
    // Prefer the non-zero numeric id; fall back to GUID so the field isn't blank.
    const uid = item.user_db_id && item.user_db_id > 0 ? item.user_db_id : (item.user_id || '')
    setUserDbId(String(uid))
    setAmount(String(item.amount || ''))
    setCurrency(item.currency || 'RUB')
    setStatus(item.status || 'pending')
    setProvider(item.provider || '')
    setExternalId(item.external_id || '')
    setNotes(item.notes || '')
    setMetadata(item.metadata ? JSON.stringify(item.metadata) : '')
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        user_db_id: parseInt(userDbId),
        amount: parseFloat(amount),
        currency,
        status,
        provider,
        external_id: externalId || undefined,
        notes: notes || undefined,
      }
      if (metadata) {
        try { payload.metadata = JSON.parse(metadata) } catch {
          toast.error(t.paymentMetadataInvalidJson || 'Metadata: невалидный JSON')
          setSaving(false)
          return
        }
      }
      if (dialog.mode === 'create') {
        await createPayment(payload)
        toast.success(t.create + ' OK')
      } else {
        await updatePayment(dialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setDialog(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      {/* Заголовок с количеством и кнопкой создания */}
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">
          {t.paymentsTitle}
          <span className="ml-2 text-base font-normal text-text-secondary">({paged.total})</span>
        </h2>
        <div className="flex gap-2">
          {can('payments.write') && (
            <>
              <button onClick={async () => {
                try {
                  const r = await fetch('/api/admin/sync-pending-payments', {method:'POST', headers:{Authorization:'Bearer '+localStorage.getItem('admin_token')}});
                  const d = await r.json();
                  if (r.ok) { toast.success(d.message || 'Синхронизация запущена'); paged.reload(); }
                  else toast.error(d.error || 'Ошибка');
                } catch(e) { toast.error(e.message) }
              }}
                className="px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-text-secondary text-sm hover:text-text-primary transition"
                title="Проверить статус всех pending платежей в ЮKassa (на случай если webhook пропал)">
                Sync pending
              </button>
              <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
                + {t.createPayment}
              </button>
            </>
          )}
        </div>
      </div>

      {/* Суммы: реальный доход (succeeded + не test) и сумма видимых записей */}
      <div className="mb-4 px-4 py-3 rounded-xl bg-surface-card border border-white/10 flex items-center gap-6 flex-wrap">
        <div>
          <span className="text-sm text-text-secondary">{t.paymentsRealRevenue || 'Реальный доход'}:</span>
          <span className="ml-2 text-lg font-bold text-green-400">
            {realTotal.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </span>
          <span className="ml-1 text-sm text-text-secondary">RUB</span>
          <span className="ml-2 text-xs text-text-secondary">({t.paymentsSucceededNonTest || 'succeeded, не test'})</span>
        </div>
        <div className="h-6 w-px bg-white/10" />
        <div>
          <span className="text-sm text-text-secondary">{t.paymentsVisibleSum || 'Сумма видимых'}:</span>
          <span className="ml-2 text-base font-semibold text-text-primary">
            {visibleSum.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
          </span>
          <span className="ml-1 text-sm text-text-secondary">
            {filteredItems[0]?.currency || 'RUB'}
          </span>
        </div>
        <div className="text-sm text-text-secondary">
          {t.paymentsShowing || 'Показано'}: {filteredItems.length}
        </div>
      </div>

      {/* Поиск + фильтр статуса */}
      <div className="flex flex-wrap items-end gap-3 mb-4">
        <div className="flex-1 min-w-[200px]">
          <SearchPaginationBar
            search={paged.search} onSearch={paged.doSearch}
            page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
            loading={paged.loading}
          />
        </div>
        <div className="mb-4">
          <label className="block text-xs text-text-secondary mb-1">{t.fieldStatus}</label>
          <select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            className="px-3 py-1.5 rounded-lg bg-surface-alt border border-white/10 text-text-primary text-sm focus:border-brand focus:ring-1 focus:ring-brand outline-none"
          >
            {STATUS_OPTIONS.map(o => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>
      </div>

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {filteredItems.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noPayments}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">User</th>
                <th className="py-2 px-3">{t.fieldAmount}</th>
                <th className="py-2 px-3">{t.fieldStatus}</th>
                <th className="py-2 px-3">Метод</th>
                <th className="py-2 px-3">Возврат</th>
                <th className="py-2 px-3">Чек</th>
                <th className="py-2 px-3">{t.paymentsDate || 'Дата'}</th>
                <th className="py-2 px-3">ЮKassa ID</th>
                <th className="py-2 px-3">{t.fieldMetadata}</th>
                {can('payments.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {filteredItems.map(p => {
                const amountColor = AMOUNT_COLORS[p.status] || 'text-text-primary'
                const refunded = parseFloat(p.refunded_amount || 0)
                const cancelReason = p.cancellation_reason || ''
                let metaObj = {}
                try { metaObj = typeof p.metadata === 'string' ? JSON.parse(p.metadata) : (p.metadata || {}) } catch {}
                const isRecovered = metaObj.recovered === true || metaObj.recovered === 'true'
                return (
                  <tr key={p.id} className="border-b border-white/5 hover:bg-white/5 transition">
                    <td className="py-2 px-3">
                      {p.id}
                      {p.test && <span className="ml-1 text-[9px] text-yellow-400 align-super">TEST</span>}
                      {isRecovered && <span className="ml-1 text-[9px] text-cyan-400 align-super" title="Платёж восстановлен через orphan recovery (webhook без исходной DB-записи)">RECOVERED</span>}
                    </td>
                    <td className="py-2 px-3 font-mono text-xs">{p.user_id || p.user_db_id || '—'}</td>
                    <td className={`py-2 px-3 font-medium ${amountColor}`}>
                      {parseFloat(p.amount).toLocaleString('ru-RU', { minimumFractionDigits: 2 })} {p.currency}
                    </td>
                    <td className="py-2 px-3">
                      <span title={cancelReason ? `Причина отмены: ${cancelReason}` : undefined}>
                        <StatusBadge status={p.status} />
                      </span>
                      {cancelReason && p.status === 'canceled' && (
                        <div className="text-[10px] text-red-400/80 mt-0.5 max-w-[140px] truncate" title={cancelReason}>
                          {cancelReason}
                        </div>
                      )}
                    </td>
                    <td className="py-2 px-3 text-xs">
                      {p.payment_method_type ? <PaymentMethodIcon type={p.payment_method_type} details={p.payment_method_details} /> : <span className="text-text-secondary">—</span>}
                    </td>
                    <td className="py-2 px-3 text-xs">
                      {refunded > 0 ? (
                        <span className="text-orange-400">{refunded.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽</span>
                      ) : (
                        <span className="text-text-secondary">—</span>
                      )}
                    </td>
                    <td className="py-2 px-3 text-center">
                      {p.receipt_registered ? <span className="text-green-400" title="Чек зарегистрирован в ОФД">✓</span> : <span className="text-text-secondary">—</span>}
                    </td>
                    <td className="py-2 px-3 text-text-secondary text-xs whitespace-nowrap">
                      {formatDate(p.created_at)}
                    </td>
                    <td className="py-2 px-3">
                      <TruncatedId value={p.yookassa_payment_id || p.external_id} />
                    </td>
                    <td className="py-2 px-3">
                      <MetadataPopover data={p.metadata} />
                    </td>
                    {can('payments.write') && (
                      <td className="py-2 px-3">
                        <div className="flex gap-1.5">
                          <button onClick={() => openEdit(p)} className="text-brand text-xs hover:underline">{t.edit}</button>
                          {(p.yookassa_payment_id || p.external_id) && (
                            <>
                              <button onClick={async () => {
                                try {
                                  const res = await checkPaymentStatus(p.id)
                                  toast.success(`ЮKassa: ${res.yookassa_status || res.local_status}`)
                                  paged.reload()
                                } catch(e) { toast.error(e.message) }
                              }} className="text-text-secondary text-xs hover:text-text-primary" title="Проверить статус в ЮKassa">
                                Status
                              </button>
                              {(p.status === 'succeeded' || p.status === 'completed') && refunded < parseFloat(p.amount) && (
                                <button onClick={async () => {
                                  const reason = prompt('Причина возврата:', 'Admin refund')
                                  if (reason === null) return
                                  try {
                                    const res = await cancelPayment(p.id, reason)
                                    toast.success(`Возврат: ${res.status || 'создан'}`)
                                    paged.reload()
                                  } catch(e) { toast.error(e.message) }
                                }} className="text-red-400 text-xs hover:underline">
                                  Refund
                                </button>
                              )}
                            </>
                          )}
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

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createPayment : t.editPayment}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldUserDbId}>
          <TextInput value={userDbId} onChange={setUserDbId} type="number" />
        </FormField>
        <FormField label={t.fieldAmount}>
          <TextInput value={amount} onChange={setAmount} type="number" />
        </FormField>
        <FormField label={t.fieldCurrency}>
          <SelectInput value={currency} onChange={setCurrency} options={[{ value: 'RUB', label: 'RUB' }, { value: 'USD', label: 'USD' }]} />
        </FormField>
        <FormField label={t.fieldStatus}>
          <SelectInput value={status} onChange={setStatus} options={[
            { value: 'pending', label: 'Pending' },
            { value: 'succeeded', label: 'Succeeded' },
            { value: 'waiting_for_capture', label: 'Waiting for capture' },
            { value: 'failed', label: 'Failed' },
            { value: 'canceled', label: 'Canceled' },
          ]} />
        </FormField>
        <FormField label={t.fieldProvider}>
          <TextInput value={provider} onChange={setProvider} />
        </FormField>
        <FormField label={t.fieldExternalId}>
          <TextInput value={externalId} onChange={setExternalId} />
        </FormField>
        <FormField label={t.fieldNotes}>
          <TextInput value={notes} onChange={setNotes} />
        </FormField>
        <FormField label={t.fieldMetadata}>
          <TextArea value={metadata} onChange={setMetadata} placeholder='{"key":"value"}' rows={2} />
        </FormField>
      </CrudDialog>
    </div>
  )
}
