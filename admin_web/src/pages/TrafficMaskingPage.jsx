import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchTrafficMaskingRules, createTrafficMaskingRule, updateTrafficMaskingRule, deleteTrafficMaskingRule } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function TrafficMaskingPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [rules, setRules] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  const [sourceDomain, setSourceDomain] = useState('')
  const [maskDomain, setMaskDomain] = useState('')
  const [isActive, setIsActive] = useState(true)

  async function load() {
    setLoading(true)
    try {
      const data = await fetchTrafficMaskingRules()
      setRules(data.rules || data || [])
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function openCreate() {
    setSourceDomain(''); setMaskDomain(''); setIsActive(true)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setSourceDomain(item.source_domain || ''); setMaskDomain(item.mask_domain || '')
    setIsActive(item.is_active !== false)
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = { source_domain: sourceDomain, mask_domain: maskDomain, is_active: isActive }
      if (dialog.mode === 'create') {
        await createTrafficMaskingRule(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateTrafficMaskingRule(dialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setDialog(null)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteTrafficMaskingRule(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  const [togglingId, setTogglingId] = useState(null)

  async function toggleActive(rule) {
    setTogglingId(rule.id)
    try {
      await updateTrafficMaskingRule(rule.id, { is_active: !rule.is_active })
      toast.success('OK')
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setTogglingId(null)
    }
  }

  if (loading) return <p className="text-text-secondary">{t.loading}</p>
  if (error) return <p className="text-red-400">{error}</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.trafficMaskingTitle}</h2>
        {can('traffic_masking.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.trafficMaskingCreate}
          </button>
        )}
      </div>

      {rules.length === 0 ? (
        <p className="text-text-secondary">{t.trafficMaskingNoRules}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.trafficMaskingSourceDomain}</th>
                <th className="py-2 px-3">{t.trafficMaskingMaskDomain}</th>
                <th className="py-2 px-3">{t.trafficMaskingActive}</th>
                {can('traffic_masking.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {rules.map(r => (
                <tr key={r.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{r.id}</td>
                  <td className="py-2 px-3 font-mono text-xs">{r.source_domain}</td>
                  <td className="py-2 px-3 font-mono text-xs">{r.mask_domain}</td>
                  <td className="py-2 px-3">
                    <button
                      onClick={() => can('traffic_masking.write') && !togglingId && toggleActive(r)}
                      disabled={togglingId === r.id}
                      className={`w-10 h-6 rounded-full transition relative ${togglingId === r.id ? 'opacity-50 cursor-wait' : ''} ${r.is_active ? 'bg-brand' : 'bg-white/20'}`}
                    >
                      <span className={`absolute top-0.5 w-5 h-5 rounded-full bg-white transition-transform ${r.is_active ? 'left-[18px]' : 'left-0.5'}`} />
                    </button>
                  </td>
                  {can('traffic_masking.write') && (
                    <td className="py-2 px-3 flex gap-2">
                      <button onClick={() => openEdit(r)} className="text-brand hover:underline">{t.edit}</button>
                      <button onClick={() => setConfirm(r)} className="text-red-400 hover:underline">{t.delete}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.trafficMaskingCreate : t.trafficMaskingEdit}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.trafficMaskingSourceDomain}><TextInput value={sourceDomain} onChange={setSourceDomain} autoFocus placeholder="example.com" /></FormField>
        <FormField label={t.trafficMaskingMaskDomain}><TextInput value={maskDomain} onChange={setMaskDomain} placeholder="mask.example.com" /></FormField>
        <Checkbox label={t.trafficMaskingActive} checked={isActive} onChange={setIsActive} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={t.trafficMaskingDeleteConfirm(confirm?.source_domain)}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}
