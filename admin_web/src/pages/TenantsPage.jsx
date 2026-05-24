import { useState } from 'react'
import { Building2 } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchTenantsPaged, createTenant, updateTenant, deleteTenant } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, TextArea, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import {
  Badge,
  Button,
  Card,
  CardBody,
  Dialog,
  EmptyState,
  Input,
  PageHeader,
  Select,
  Table,
  Tbody,
  Td,
  Th,
  Thead,
  Tr,
  TableSkeleton,
} from '../components/ui/index.js'

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

  const planVariant = (p) => p === 'enterprise' ? 'accent' : p === 'standard' ? 'info' : 'default'

  const actions = (
    <>
      {can('tenants.read') && (
        <Button
          variant="secondary"
          size="sm"
          onClick={openUnassigned}
          title="Привязать осиротевшие платежи B2B к компаниям"
        >Привязать платежи</Button>
      )}
      {can('tenants.write') && (
        <Button onClick={openCreate}>+ {t.createTenant}</Button>
      )}
    </>
  )

  return (
    <div>
      <PageHeader
        title={t.tenantsTitle}
        subtitle="Компании, использующие платформу в B2B-режиме"
        actions={actions}
      />

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-danger mb-4">{paged.error}</p>}

      {paged.loading ? (
        <TableSkeleton rows={6} cols={6} />
      ) : paged.items.length === 0 ? (
        <EmptyState
          icon={<Building2 size={32} />}
          title={t.noTenants || 'Нет компаний'}
          description="Создайте первую B2B-компанию через кнопку выше."
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>ID</Th>
              <Th>{t.fieldTenantName}</Th>
              <Th>{t.fieldSlug}</Th>
              <Th>{t.fieldPlan}</Th>
              <Th align="right">{t.fieldMaxUsers}</Th>
              <Th>{t.fieldContactEmail}</Th>
              {can('tenants.write') && <Th></Th>}
            </Tr>
          </Thead>
          <Tbody>
            {paged.items.map(tn => (
              <Tr key={tn.id}>
                <Td mono>{tn.id}</Td>
                <Td>{tn.name}</Td>
                <Td mono><span className="text-xs">{tn.slug}</span></Td>
                <Td><Badge variant={planVariant(tn.plan)}>{tn.plan}</Badge></Td>
                <Td align="right" mono>{tn.max_users ?? '—'}</Td>
                <Td><span className="text-secondary">{tn.contact_email || '—'}</span></Td>
                {can('tenants.write') && (
                  <Td>
                    <div className="flex gap-1 flex-wrap">
                      <Button variant="ghost" size="sm" onClick={() => openEdit(tn)}>{t.edit}</Button>
                      <Button variant="ghost" size="sm" onClick={() => setInvoiceTenant(tn)}>Счёт</Button>
                      <Button variant="ghost" size="sm" onClick={() => setConfirm(tn)} className="text-danger hover:text-danger">{t.delete}</Button>
                    </div>
                  </Td>
                )}
              </Tr>
            ))}
          </Tbody>
        </Table>
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
      <Dialog
        open={!!invoiceTenant}
        onClose={() => { if (!invoiceLoading && !invoiceResult) { setInvoiceTenant(null); setInvoiceResult(null) } }}
        title={invoiceTenant ? `Счёт для ${invoiceTenant.name}` : ''}
        maxWidth="max-w-lg"
      >
        {invoiceResult ? (
          <div>
            <p className="text-sm text-success mb-3">Счёт создан на <span className="tabular-nums">{invoiceResult.amount}₽</span></p>
            <p className="text-xs text-tertiary mb-3">Ссылка для оплаты отправлена на {invoiceResult.email}. Также можете скопировать её ниже:</p>
            <div className="p-3 rounded border border-default bg-canvas text-xs font-mono break-all select-all text-primary">
              {invoiceResult.confirmation_url}
            </div>
            <div className="flex justify-end mt-4">
              <Button onClick={() => { setInvoiceTenant(null); setInvoiceResult(null) }}>Закрыть</Button>
            </div>
          </div>
        ) : invoiceTenant && (
          <div className="space-y-3">
            <div>
              <label className="text-xs text-tertiary mb-1 block">ИНН (10 или 12 цифр)</label>
              <Input type="text" value={invoiceInn} onChange={e => setInvoiceInn(e.target.value.replace(/\D/g, '').slice(0, 12))}
                className="font-mono" placeholder="7707083893" />
            </div>
            <div>
              <label className="text-xs text-tertiary mb-1 block">Название организации</label>
              <Input type="text" value={invoiceCompany} onChange={e => setInvoiceCompany(e.target.value)} placeholder="ООО Ромашка" />
            </div>
            <div>
              <label className="text-xs text-tertiary mb-1 block">Сумма (₽)</label>
              <Input type="number" value={invoiceAmount} onChange={e => setInvoiceAmount(e.target.value)} min="100" placeholder="5000" />
            </div>
            <div>
              <label className="text-xs text-tertiary mb-1 block">Email для отправки счёта</label>
              <Input type="email" value={invoiceEmail} onChange={e => setInvoiceEmail(e.target.value)} placeholder={invoiceTenant.contact_email || 'buyer@company.ru'} />
            </div>
            <div>
              <label className="text-xs text-tertiary mb-1 block">Описание (необязательно)</label>
              <textarea value={invoiceDescription} onChange={e => setInvoiceDescription(e.target.value)} rows={2}
                className="w-full px-3 py-1.5 rounded border border-default bg-elevated text-sm text-primary placeholder:text-tertiary focus:outline-none focus:border-strong focus:ring-1 focus:ring-accent/40" />
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <Button variant="ghost" onClick={() => setInvoiceTenant(null)} disabled={invoiceLoading}>Отмена</Button>
              <Button
                onClick={async () => {
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
                }}
                disabled={invoiceLoading || !invoiceInn || !invoiceCompany || !invoiceAmount}
                loading={invoiceLoading}
              >Создать счёт</Button>
            </div>
          </div>
        )}
      </Dialog>

      {/* Unassigned b2b_invoice reconciliation (P1.6) */}
      <Dialog
        open={unassignedOpen}
        onClose={() => setUnassignedOpen(false)}
        title="Привязать платежи B2B"
        maxWidth="max-w-3xl"
      >
        <p className="text-xs text-tertiary mb-4">Платежи b2b_invoice без tenant_id — выберите компанию для зачисления.</p>

        {unassignedLoading ? (
          <p className="text-sm text-tertiary py-8 text-center">Загрузка…</p>
        ) : unassignedItems.length === 0 ? (
          <EmptyState
            icon={<Building2 size={28} />}
            title="Все платежи привязаны"
            description="Новые инвойсы автоматически получают tenant_id (либо по явному admin-выбору, либо по INN-lookup)."
          />
        ) : (
          <div className="space-y-2">
            {unassignedItems.map(p => (
              <Card key={p.id}>
                <CardBody className="p-3">
                  <div className="flex items-center justify-between gap-3 flex-wrap">
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-primary tabular-nums">
                        #{p.id} · {p.amount.toLocaleString('ru-RU')} {p.currency}
                        {' · '}
                        <Badge variant={p.status === 'completed' ? 'success' : 'warning'}>{p.status}</Badge>
                      </p>
                      <p className="text-[11px] text-tertiary mt-0.5">
                        {p.company_name && <>«{p.company_name}» · </>}
                        {p.inn && <>ИНН {p.inn} · </>}
                        {p.payer_email && <>{p.payer_email}</>}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Select
                        value={attachTargets[p.id] || ''}
                        onChange={e => setAttachTargets(prev => ({ ...prev, [p.id]: e.target.value }))}
                        className="w-auto text-xs"
                      >
                        <option value="">— компания —</option>
                        {(paged.items || []).map(tn => (
                          <option key={tn.id} value={tn.id}>{tn.name} ({tn.status})</option>
                        ))}
                      </Select>
                      <Button
                        size="sm"
                        disabled={!attachTargets[p.id] || attachBusy[p.id]}
                        loading={attachBusy[p.id]}
                        onClick={() => attachPayment(p.id)}
                      >Привязать</Button>
                    </div>
                  </div>
                </CardBody>
              </Card>
            ))}
          </div>
        )}
      </Dialog>
    </div>
  )
}
