import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchTenantsPaged, createTenant, updateTenant, deleteTenant } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, TextArea, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function TenantsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchTenantsPaged)

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [invoiceTenant, setInvoiceTenant] = useState(null)
  const [invoiceInn, setInvoiceInn] = useState('')
  const [invoiceCompany, setInvoiceCompany] = useState('')
  const [invoiceAmount, setInvoiceAmount] = useState('')
  const [invoiceEmail, setInvoiceEmail] = useState('')
  const [invoiceDescription, setInvoiceDescription] = useState('')
  const [invoiceLoading, setInvoiceLoading] = useState(false)
  const [invoiceResult, setInvoiceResult] = useState(null)

  // Unassigned-invoices reconciliation modal (P1.6).
  // Lists b2b_invoice payments with no tenant_id in metadata so admin can
  // link them retroactively. After fix the count should naturally drop to 0.
  const [unassignedOpen, setUnassignedOpen] = useState(false)
  const [unassignedItems, setUnassignedItems] = useState([])
  const [unassignedLoading, setUnassignedLoading] = useState(false)
  const [attachTargets, setAttachTargets] = useState({}) // {payment_id: tenant_id}
  const [attachBusy, setAttachBusy] = useState({})       // {payment_id: bool}

  async function openUnassigned() {
    setUnassignedOpen(true)
    setUnassignedLoading(true)
    try {
      const res = await fetch('/api/admin/payments/unassigned-b2b-invoices', {
        headers: { Authorization: 'Bearer ' + localStorage.getItem('admin_token') },
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'load failed')
      setUnassignedItems(data.items || [])
    } catch (e) {
      toast.error(e.message)
    } finally {
      setUnassignedLoading(false)
    }
  }

  async function attachPayment(paymentId) {
    const tenantId = attachTargets[paymentId]
    if (!tenantId) { toast.error('Выберите компанию'); return }
    setAttachBusy(b => ({ ...b, [paymentId]: true }))
    try {
      const res = await fetch(`/api/admin/payments/${paymentId}/attach-tenant`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + localStorage.getItem('admin_token') },
        body: JSON.stringify({ tenant_id: Number(tenantId) }),
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'attach failed')
      toast.success(data.credited ? `Привязано и зачислено в «${data.tenant_name}»` : `Привязано к «${data.tenant_name}»`)
      // Drop from list — done.
      setUnassignedItems(items => items.filter(p => p.id !== paymentId))
    } catch (e) {
      toast.error(e.message)
    } finally {
      setAttachBusy(b => ({ ...b, [paymentId]: false }))
    }
  }

  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [contactEmail, setContactEmail] = useState('')
  const [plan, setPlan] = useState('basic')
  const [maxUsers, setMaxUsers] = useState('')
  const [maxServers, setMaxServers] = useState('')
  const [useProviderServers, setUseProviderServers] = useState(false)
  const [billingNotes, setBillingNotes] = useState('')

  function openCreate() {
    setName(''); setSlug(''); setContactEmail(''); setPlan('basic')
    setMaxUsers(''); setMaxServers(''); setUseProviderServers(false); setBillingNotes('')
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || '')
    setSlug(item.slug || '')
    setContactEmail(item.contact_email || '')
    setPlan(item.plan || 'basic')
    setMaxUsers(item.max_users != null ? String(item.max_users) : '')
    setMaxServers(item.max_servers != null ? String(item.max_servers) : '')
    setUseProviderServers(item.use_provider_servers || false)
    setBillingNotes(item.billing_notes || '')
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        name, slug, contact_email: contactEmail, plan,
        use_provider_servers: useProviderServers,
        billing_notes: billingNotes || undefined,
        ...(maxUsers ? { max_users: parseInt(maxUsers) } : {}),
        ...(maxServers ? { max_servers: parseInt(maxServers) } : {}),
      }
      if (dialog.mode === 'create') {
        await createTenant(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateTenant(dialog.item.id, payload)
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

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteTenant(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4 flex-wrap gap-2">
        <h2 className="text-2xl font-bold">{t.tenantsTitle}</h2>
        <div className="flex items-center gap-2">
          {can('tenants.read') && (
            <button
              onClick={openUnassigned}
              className="px-3 py-2 rounded-lg bg-amber-500/15 text-amber-300 border border-amber-500/30 text-sm font-medium hover:bg-amber-500/25 transition"
              title="Привязать осиротевшие платежи B2B к компаниям"
            >Привязать платежи</button>
          )}
          {can('tenants.write') && (
            <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
              + {t.createTenant}
            </button>
          )}
        </div>
      </div>

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noTenants}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldTenantName}</th>
                <th className="py-2 px-3">{t.fieldSlug}</th>
                <th className="py-2 px-3">{t.fieldPlan}</th>
                <th className="py-2 px-3">{t.fieldMaxUsers}</th>
                <th className="py-2 px-3">{t.fieldContactEmail}</th>
                {can('tenants.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map(tn => (
                <tr key={tn.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{tn.id}</td>
                  <td className="py-2 px-3">{tn.name}</td>
                  <td className="py-2 px-3 font-mono text-xs">{tn.slug}</td>
                  <td className="py-2 px-3">{tn.plan}</td>
                  <td className="py-2 px-3">{tn.max_users ?? '\u2014'}</td>
                  <td className="py-2 px-3 text-text-secondary">{tn.contact_email || '\u2014'}</td>
                  {can('tenants.write') && (
                    <td className="py-2 px-3 flex gap-2 flex-wrap">
                      <button onClick={() => openEdit(tn)} className="text-brand hover:underline">{t.edit}</button>
                      <button onClick={() => setInvoiceTenant(tn)} className="text-green-400 hover:underline text-xs">Счёт</button>
                      <button onClick={() => setConfirm(tn)} className="text-red-400 hover:underline">{t.delete}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createTenant : t.editTenant}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldTenantName}><TextInput value={name} onChange={setName} autoFocus /></FormField>
        <FormField label={t.fieldSlug}><TextInput value={slug} onChange={setSlug} /></FormField>
        <FormField label={t.fieldContactEmail}><TextInput value={contactEmail} onChange={setContactEmail} type="email" /></FormField>
        <FormField label={t.fieldPlan}>
          <SelectInput value={plan} onChange={setPlan} options={[
            { value: 'basic', label: 'Basic' },
            { value: 'standard', label: 'Standard' },
            { value: 'enterprise', label: 'Enterprise' },
          ]} />
        </FormField>
        <FormField label={t.fieldMaxUsers}><TextInput value={maxUsers} onChange={setMaxUsers} type="number" /></FormField>
        <FormField label={t.fieldMaxServers}><TextInput value={maxServers} onChange={setMaxServers} type="number" /></FormField>
        <Checkbox label={t.fieldUseProviderServers} checked={useProviderServers} onChange={setUseProviderServers} />
        <FormField label={t.fieldBillingNotes}><TextArea value={billingNotes} onChange={setBillingNotes} rows={2} /></FormField>
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={t.deleteTenantConfirm(confirm?.name)}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />

      {/* Invoice dialog (B2B) */}
      {invoiceTenant && (
        <div className="fixed inset-0 z-40 bg-black/60 flex items-center justify-center p-4" onClick={() => { if (!invoiceLoading && !invoiceResult) setInvoiceTenant(null) }}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-lg p-6" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-lg font-semibold">Счёт для {invoiceTenant.name}</h3>
              <button onClick={() => { setInvoiceTenant(null); setInvoiceResult(null) }} className="text-text-secondary hover:text-text-primary">✕</button>
            </div>

            {invoiceResult ? (
              <div>
                <p className="text-sm text-green-400 mb-3">✓ Счёт создан на {invoiceResult.amount}₽</p>
                <p className="text-xs text-text-secondary mb-3">Ссылка для оплаты отправлена на {invoiceResult.email}. Также можете скопировать её ниже:</p>
                <div className="p-3 rounded-lg bg-surface-alt border border-white/10 text-xs font-mono break-all select-all">
                  {invoiceResult.confirmation_url}
                </div>
                <button onClick={() => { setInvoiceTenant(null); setInvoiceResult(null) }} className="mt-4 px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium">Закрыть</button>
              </div>
            ) : (
              <div className="space-y-3">
                <div>
                  <label className="text-xs text-text-secondary mb-1 block">ИНН (10 или 12 цифр)</label>
                  <input type="text" value={invoiceInn} onChange={e => setInvoiceInn(e.target.value.replace(/\D/g, '').slice(0, 12))}
                    className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono" placeholder="7707083893" />
                </div>
                <div>
                  <label className="text-xs text-text-secondary mb-1 block">Название организации</label>
                  <input type="text" value={invoiceCompany} onChange={e => setInvoiceCompany(e.target.value)}
                    className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" placeholder="ООО Ромашка" />
                </div>
                <div>
                  <label className="text-xs text-text-secondary mb-1 block">Сумма (₽)</label>
                  <input type="number" value={invoiceAmount} onChange={e => setInvoiceAmount(e.target.value)} min="100"
                    className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" placeholder="5000" />
                </div>
                <div>
                  <label className="text-xs text-text-secondary mb-1 block">Email для отправки счёта</label>
                  <input type="email" value={invoiceEmail} onChange={e => setInvoiceEmail(e.target.value)}
                    className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" placeholder={invoiceTenant.contact_email || 'buyer@company.ru'} />
                </div>
                <div>
                  <label className="text-xs text-text-secondary mb-1 block">Описание (необязательно)</label>
                  <textarea value={invoiceDescription} onChange={e => setInvoiceDescription(e.target.value)} rows={2}
                    className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm" />
                </div>

                <div className="flex justify-end gap-2 pt-2">
                  <button onClick={() => setInvoiceTenant(null)} disabled={invoiceLoading}
                    className="px-4 py-2 rounded-lg bg-surface-alt text-text-secondary text-sm">Отмена</button>
                  <button onClick={async () => {
                    setInvoiceLoading(true)
                    try {
                      const r = await fetch('/api/v1/business/create-invoice', {
                        method: 'POST', headers: {'Content-Type':'application/json'},
                        body: JSON.stringify({
                          inn: invoiceInn, company_name: invoiceCompany,
                          amount: parseInt(invoiceAmount) || 0,
                          email: invoiceEmail || invoiceTenant.contact_email || '',
                          description: invoiceDescription,
                        })
                      })
                      const data = await r.json()
                      if (!r.ok) { toast.error(data.error || 'Ошибка'); return }
                      setInvoiceResult(data)
                      toast.success('Счёт создан')
                    } catch(e) { toast.error(e.message) }
                    finally { setInvoiceLoading(false) }
                  }} disabled={invoiceLoading || !invoiceInn || !invoiceCompany || !invoiceAmount}
                    className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium disabled:opacity-50">
                    {invoiceLoading ? 'Создаём...' : 'Создать счёт'}
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Unassigned b2b_invoice reconciliation (P1.6) */}
      {unassignedOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={() => setUnassignedOpen(false)}>
          <div onClick={e => e.stopPropagation()} className="w-full max-w-3xl max-h-[85vh] overflow-y-auto rounded-2xl bg-surface-card border border-white/10 p-6">
            <div className="flex items-center justify-between mb-4">
              <div>
                <h3 className="text-lg font-bold">Привязать платежи B2B</h3>
                <p className="text-xs text-text-secondary mt-0.5">Платежи b2b_invoice без tenant_id — выберите компанию для зачисления.</p>
              </div>
              <button onClick={() => setUnassignedOpen(false)} className="text-text-secondary hover:text-text-primary">✕</button>
            </div>

            {unassignedLoading ? (
              <p className="text-sm text-text-secondary py-8 text-center">Загрузка…</p>
            ) : unassignedItems.length === 0 ? (
              <div className="text-center py-8">
                <p className="text-sm text-text-secondary">Все платежи привязаны 🎉</p>
                <p className="text-xs text-text-secondary mt-1">Новые инвойсы автоматически получают tenant_id (либо по явному admin-выбору, либо по INN-lookup).</p>
              </div>
            ) : (
              <div className="space-y-2">
                {unassignedItems.map(p => (
                  <div key={p.id} className="p-3 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between gap-3 flex-wrap">
                      <div className="min-w-0 flex-1">
                        <p className="text-sm font-medium">
                          #{p.id} · {p.amount.toLocaleString('ru-RU')} {p.currency}
                          {' · '}<span className={`text-xs px-1.5 py-0.5 rounded ${p.status === 'completed' ? 'bg-green-500/15 text-green-400' : 'bg-yellow-500/15 text-yellow-400'}`}>{p.status}</span>
                        </p>
                        <p className="text-[11px] text-text-secondary mt-0.5">
                          {p.company_name && <>«{p.company_name}» · </>}
                          {p.inn && <>ИНН {p.inn} · </>}
                          {p.payer_email && <>{p.payer_email}</>}
                        </p>
                      </div>
                      <div className="flex items-center gap-2">
                        <select
                          value={attachTargets[p.id] || ''}
                          onChange={e => setAttachTargets(t => ({ ...t, [p.id]: e.target.value }))}
                          className="px-2 py-1.5 rounded-lg bg-surface-alt border border-white/10 text-xs text-text-primary"
                        >
                          <option value="">— компания —</option>
                          {(paged.items || []).map(t => (
                            <option key={t.id} value={t.id}>{t.name} ({t.status})</option>
                          ))}
                        </select>
                        <button
                          disabled={!attachTargets[p.id] || attachBusy[p.id]}
                          onClick={() => attachPayment(p.id)}
                          className="px-3 py-1.5 rounded-lg bg-brand text-white text-xs font-medium hover:bg-brand-dark transition disabled:opacity-50"
                        >{attachBusy[p.id] ? '…' : 'Привязать'}</button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
