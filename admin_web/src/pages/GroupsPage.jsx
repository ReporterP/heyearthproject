import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchGroupsPaged, createGroup, updateGroup, deleteGroup } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function GroupsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchGroupsPaged)

  const [dialog, setDialog] = useState(null) // { mode: 'create'|'edit', item }
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  // form state
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [isPremium, setIsPremium] = useState(false)

  function openCreate() {
    setName('')
    setDescription('')
    setIsPremium(false)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || '')
    setDescription(item.description || '')
    setIsPremium(item.is_premium || false)
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = { name, description, is_premium: isPremium }
      if (dialog.mode === 'create') {
        await createGroup(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateGroup(dialog.item.id, payload)
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
      await deleteGroup(confirm.id)
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
        <h2 className="text-2xl font-bold">{t.groupsTitle}</h2>
        {can('groups.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createGroup}
          </button>
        )}
      </div>

      <SearchPaginationBar
        search={paged.search}
        onSearch={paged.doSearch}
        page={paged.page}
        totalPages={paged.totalPages}
        onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noGroups}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldGroupName}</th>
                <th className="py-2 px-3">{t.fieldDescription}</th>
                <th className="py-2 px-3">{t.fieldIsPremium}</th>
                {can('groups.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map(g => (
                <tr key={g.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{g.id}</td>
                  <td className="py-2 px-3">{g.name}</td>
                  <td className="py-2 px-3 text-text-secondary">{g.description || '\u2014'}</td>
                  <td className="py-2 px-3">{g.is_premium ? '\u2713' : ''}</td>
                  {can('groups.write') && (
                    <td className="py-2 px-3 flex gap-2">
                      <button onClick={() => openEdit(g)} className="text-brand hover:underline">{t.edit}</button>
                      <button onClick={() => setConfirm(g)} className="text-red-400 hover:underline">{t.delete}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={!!dialog}
        title={dialog?.mode === 'create' ? t.createGroup : t.editGroup}
        onClose={() => setDialog(null)}
        onSubmit={handleSave}
        loading={saving}
      >
        <FormField label={t.fieldGroupName}>
          <TextInput value={name} onChange={setName} autoFocus />
        </FormField>
        <FormField label={t.fieldDescription}>
          <TextInput value={description} onChange={setDescription} />
        </FormField>
        <Checkbox label={t.fieldIsPremium} checked={isPremium} onChange={setIsPremium} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete group "${confirm?.name}"?`}
        onConfirm={handleDelete}
        onCancel={() => setConfirm(null)}
        loading={saving}
      />
    </div>
  )
}
