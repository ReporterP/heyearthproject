import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchSubscriptionsPaged, saveSubscription, deleteSubscription } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function SubscriptionsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchSubscriptionsPaged)

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  const [userDbId, setUserDbId] = useState('')
  const [groupId, setGroupId] = useState('')
  const [validUntil, setValidUntil] = useState('')
  const [autoRenew, setAutoRenew] = useState(false)

  function openCreate() {
    setUserDbId('')
    setGroupId('')
    setValidUntil('')
    setAutoRenew(false)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setUserDbId(String(item.user_db_id || ''))
    setGroupId(String(item.group_id || ''))
    setValidUntil(item.valid_until || '')
    setAutoRenew(item.auto_renew || false)
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      await saveSubscription({
        user_db_id: parseInt(userDbId),
        group_id: parseInt(groupId),
        valid_until: validUntil,
        auto_renew: autoRenew,
      })
      toast.success(t.save + ' OK')
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
      await deleteSubscription(confirm.user_db_id, confirm.group_id)
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
        <h2 className="text-2xl font-bold">{t.subscriptionsTitle}</h2>
        {can('subscriptions.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.saveSubscription}
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
        <p className="text-text-secondary">{t.noSubscriptions}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">{t.fieldUserDbId}</th>
                <th className="py-2 px-3">{t.fieldGroupId}</th>
                <th className="py-2 px-3">{t.fieldValidUntil}</th>
                <th className="py-2 px-3">{t.fieldAutoRenew}</th>
                {can('subscriptions.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map((s, i) => (
                <tr key={`${s.user_db_id}-${s.group_id}-${i}`} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{s.user_db_id}</td>
                  <td className="py-2 px-3">{s.group_id}</td>
                  <td className="py-2 px-3">{s.valid_until ? new Date(s.valid_until).toLocaleDateString() : '\u2014'}</td>
                  <td className="py-2 px-3">{s.auto_renew ? '\u2713' : ''}</td>
                  {can('subscriptions.write') && (
                    <td className="py-2 px-3 flex gap-2">
                      <button onClick={() => openEdit(s)} className="text-brand hover:underline">{t.edit}</button>
                      <button onClick={() => setConfirm(s)} className="text-red-400 hover:underline">{t.delete}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.saveSubscription : t.editSubscription}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldUserDbId}>
          <TextInput value={userDbId} onChange={setUserDbId} type="number" />
        </FormField>
        <FormField label={t.fieldGroupId}>
          <TextInput value={groupId} onChange={setGroupId} type="number" />
        </FormField>
        <FormField label={t.fieldValidUntil}>
          <TextInput value={validUntil} onChange={setValidUntil} placeholder="2025-12-31T23:59:59Z" />
        </FormField>
        <Checkbox label={t.fieldAutoRenew} checked={autoRenew} onChange={setAutoRenew} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete subscription user=${confirm?.user_db_id} group=${confirm?.group_id}?`}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}
