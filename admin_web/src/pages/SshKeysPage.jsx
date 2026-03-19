import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchSshKeys, createSshKey, updateSshKey, deleteSshKey } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, TextArea } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'

export default function SshKeysPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)

  const [name, setName] = useState('')
  const [privateKey, setPrivateKey] = useState('')

  async function load() {
    setLoading(true)
    try {
      const data = await fetchSshKeys()
      setKeys(data.keys || data || [])
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function openCreate() {
    setName(''); setPrivateKey('')
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || ''); setPrivateKey('')
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      if (dialog.mode === 'create') {
        await createSshKey(name, privateKey)
        toast.success(t.create + ' OK')
      } else {
        await updateSshKey(dialog.item.id, name, privateKey || undefined)
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
      await deleteSshKey(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  function copyFingerprint(fp) {
    navigator.clipboard.writeText(fp).then(() => toast.info('Copied'))
  }

  if (loading) return <p className="text-text-secondary">{t.loading}</p>
  if (error) return <p className="text-red-400">{error}</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.sshKeysTitle}</h2>
        {can('servers.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.addSshKey}
          </button>
        )}
      </div>

      {keys.length === 0 ? (
        <p className="text-text-secondary">{t.noSshKeys}</p>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {keys.map(k => (
            <div key={k.id} className="bg-surface-card rounded-xl border border-white/10 p-4">
              <h3 className="font-semibold mb-1">{k.name}</h3>
              {k.fingerprint && (
                <p
                  className="text-xs font-mono text-text-secondary truncate cursor-pointer hover:text-text-primary"
                  onClick={() => copyFingerprint(k.fingerprint)}
                  title="Click to copy"
                >
                  {t.labelFingerprint}: {k.fingerprint}
                </p>
              )}
              {can('servers.write') && (
                <div className="flex gap-2 mt-3">
                  <button onClick={() => openEdit(k)} className="text-xs px-2 py-1 rounded bg-brand/20 text-brand hover:bg-brand/30 transition">{t.edit}</button>
                  <button onClick={() => setConfirm(k)} className="text-xs px-2 py-1 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 transition">{t.delete}</button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      <CrudDialog
        open={!!dialog}
        title={dialog?.mode === 'create' ? t.addSshKey : t.editSshKey}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldKeyName}><TextInput value={name} onChange={setName} autoFocus /></FormField>
        <FormField label={dialog?.mode === 'create' ? t.fieldPrivateKey : t.fieldPrivateKeyOptional}>
          <TextArea value={privateKey} onChange={setPrivateKey} rows={6} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
        </FormField>
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={t.deleteSshKeyConfirm(confirm?.name)}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}
