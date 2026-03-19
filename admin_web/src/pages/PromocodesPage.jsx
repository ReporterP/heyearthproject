import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchPromocodesPaged, createPromocode, updatePromocode, deletePromocode } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function PromocodesPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchPromocodesPaged)

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  const [code, setCode] = useState('')
  const [groupId, setGroupId] = useState('')
  const [maxUses, setMaxUses] = useState('')
  const [isActive, setIsActive] = useState(true)

  function openCreate() {
    setCode('')
    setGroupId('')
    setMaxUses('')
    setIsActive(true)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setCode(item.code || '')
    setGroupId(item.group_id != null ? String(item.group_id) : '')
    setMaxUses(item.max_uses != null ? String(item.max_uses) : '')
    setIsActive(item.is_active !== false)
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        code,
        is_active: isActive,
        ...(groupId ? { group_id: parseInt(groupId) } : {}),
        ...(maxUses ? { max_uses: parseInt(maxUses) } : {}),
      }
      if (dialog.mode === 'create') {
        await createPromocode(payload)
        toast.success(t.create + ' OK')
      } else {
        await updatePromocode(dialog.item.code, payload)
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
      await deletePromocode(confirm.code)
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
        <h2 className="text-2xl font-bold">{t.promocodesTitle}</h2>
        {can('promocodes.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createPromocode}
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
        <p className="text-text-secondary">{t.noPromocodes}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">{t.fieldCode}</th>
                <th className="py-2 px-3">{t.fieldGroupId}</th>
                <th className="py-2 px-3">{t.fieldMaxUses}</th>
                <th className="py-2 px-3">Uses</th>
                <th className="py-2 px-3">{t.fieldIsActive}</th>
                {can('promocodes.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map(p => (
                <tr key={p.code} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3 font-mono">{p.code}</td>
                  <td className="py-2 px-3">{p.group_id ?? '\u2014'}</td>
                  <td className="py-2 px-3">{p.max_uses ?? '\u221e'}</td>
                  <td className="py-2 px-3">{p.use_count ?? 0}</td>
                  <td className="py-2 px-3">{p.is_active ? '\u2713' : '\u2717'}</td>
                  {can('promocodes.write') && (
                    <td className="py-2 px-3 flex gap-2">
                      <button onClick={() => openEdit(p)} className="text-brand hover:underline">{t.edit}</button>
                      <button onClick={() => setConfirm(p)} className="text-red-400 hover:underline">{t.delete}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createPromocode : t.editPromocode}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldCode}>
          <TextInput value={code} onChange={setCode} disabled={dialog?.mode === 'edit'} autoFocus />
        </FormField>
        <FormField label={t.fieldGroupIdOptional}>
          <TextInput value={groupId} onChange={setGroupId} type="number" />
        </FormField>
        <FormField label={t.fieldMaxUses}>
          <TextInput value={maxUses} onChange={setMaxUses} type="number" />
        </FormField>
        <Checkbox label={t.fieldIsActive} checked={isActive} onChange={setIsActive} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete promocode "${confirm?.code}"?`}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}
