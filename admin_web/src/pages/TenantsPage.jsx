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
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.tenantsTitle}</h2>
        {can('tenants.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createTenant}
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
                    <td className="py-2 px-3 flex gap-2">
                      <button onClick={() => openEdit(tn)} className="text-brand hover:underline">{t.edit}</button>
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
    </div>
  )
}
