import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchPaymentsPaged, createPayment, updatePayment } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, TextArea, SelectInput } from '../components/CrudDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

export default function PaymentsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchPaymentsPaged)

  const [dialog, setDialog] = useState(null)
  const [saving, setSaving] = useState(false)

  const [userDbId, setUserDbId] = useState('')
  const [amount, setAmount] = useState('')
  const [currency, setCurrency] = useState('RUB')
  const [status, setStatus] = useState('pending')
  const [provider, setProvider] = useState('manual')
  const [externalId, setExternalId] = useState('')
  const [notes, setNotes] = useState('')
  const [metadata, setMetadata] = useState('')

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
    setUserDbId(String(item.user_db_id || ''))
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
        try { payload.metadata = JSON.parse(metadata) } catch {}
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
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.paymentsTitle}</h2>
        {can('payments.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createPayment}
          </button>
        )}
      </div>

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noPayments}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldUserDbId}</th>
                <th className="py-2 px-3">{t.fieldAmount}</th>
                <th className="py-2 px-3">{t.fieldStatus}</th>
                <th className="py-2 px-3">{t.fieldProvider}</th>
                <th className="py-2 px-3">{t.fieldNotes}</th>
                {can('payments.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map(p => (
                <tr key={p.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{p.id}</td>
                  <td className="py-2 px-3">{p.user_db_id}</td>
                  <td className="py-2 px-3">{p.amount} {p.currency}</td>
                  <td className="py-2 px-3"><StatusBadge status={p.status} /></td>
                  <td className="py-2 px-3">{p.provider}</td>
                  <td className="py-2 px-3 text-text-secondary truncate max-w-[200px]">{p.notes || '\u2014'}</td>
                  {can('payments.write') && (
                    <td className="py-2 px-3">
                      <button onClick={() => openEdit(p)} className="text-brand hover:underline">{t.edit}</button>
                    </td>
                  )}
                </tr>
              ))}
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
