import { useState, useEffect } from 'react'
import { EyeOff } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchTrafficMaskingRules, createTrafficMaskingRule, updateTrafficMaskingRule, deleteTrafficMaskingRule } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import {
  Button,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  TableSkeleton,
  EmptyState,
} from '../components/ui/index.js'

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
    setError('')
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

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.trafficMaskingTitle}
        subtitle={t.trafficMaskingSubtitle || 'Подмена SNI домена в трафике'}
        actions={can('traffic_masking.write') ? (
          <Button variant="primary" onClick={openCreate}>+ {t.trafficMaskingCreate}</Button>
        ) : null}
      />

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={6} cols={5} />
      ) : rules.length === 0 ? (
        <EmptyState
          icon={<EyeOff className="w-12 h-12" />}
          title={t.trafficMaskingNoRules}
          description={t.trafficMaskingNoRulesDesc || 'Создайте первое правило подмены SNI домена.'}
          action={can('traffic_masking.write') ? (
            <Button variant="primary" onClick={openCreate}>+ {t.trafficMaskingCreate}</Button>
          ) : null}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>ID</Th>
              <Th>{t.trafficMaskingSourceDomain}</Th>
              <Th>{t.trafficMaskingMaskDomain}</Th>
              <Th>{t.trafficMaskingActive}</Th>
              {can('traffic_masking.write') && <Th>Действия</Th>}
            </Tr>
          </Thead>
          <Tbody>
            {rules.map(r => (
              <Tr key={r.id}>
                <Td mono>{r.id}</Td>
                <Td mono className="text-xs">{r.source_domain}</Td>
                <Td mono className="text-xs">{r.mask_domain}</Td>
                <Td>
                  <button
                    onClick={() => can('traffic_masking.write') && !togglingId && toggleActive(r)}
                    disabled={togglingId === r.id}
                    aria-label={r.is_active ? 'Деактивировать' : 'Активировать'}
                    className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${togglingId === r.id ? 'opacity-50 cursor-wait' : ''} ${r.is_active ? 'bg-success/30' : 'bg-hover'}`}
                  >
                    <span className={`inline-block h-3.5 w-3.5 rounded-full transition-transform ${r.is_active ? 'translate-x-[18px] bg-success' : 'translate-x-1 bg-tertiary'}`} />
                  </button>
                </Td>
                {can('traffic_masking.write') && (
                  <Td>
                    <div className="flex items-center gap-2">
                      <Button size="sm" variant="ghost" onClick={() => openEdit(r)}>{t.edit}</Button>
                      <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(r)}>{t.delete}</Button>
                    </div>
                  </Td>
                )}
              </Tr>
            ))}
          </Tbody>
        </Table>
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
