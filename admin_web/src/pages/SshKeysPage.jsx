import { useEffect, useReducer } from 'react'
import { Key } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchSshKeys, createSshKey, updateSshKey, deleteSshKey } from '../api/admin.js'
import CrudDialog, { FormField, TextInput, TextArea } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import {
  Button,
  Card,
  PageHeader,
  Skeleton,
  EmptyState,
} from '../components/ui/index.js'

function reducer(state, action) {
  switch (action.type) {
    case 'LOAD_START': return { ...state, loading: true }
    case 'LOAD_OK': return { ...state, loading: false, keys: action.keys }
    case 'LOAD_ERR': return { ...state, loading: false, error: action.error }
    case 'SET': return { ...state, ...action.patch }
    default: return state
  }
}

const init = { keys: [], loading: true, error: '', dialog: null, confirm: null, saving: false, name: '', privateKey: '' }

export default function SshKeysPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [s, dispatch] = useReducer(reducer, init)

  async function load() {
    dispatch({ type: 'LOAD_START' })
    try {
      const data = await fetchSshKeys()
      const keys = Array.isArray(data) ? data : (Array.isArray(data?.keys) ? data.keys : [])
      dispatch({ type: 'LOAD_OK', keys })
    } catch (err) {
      dispatch({ type: 'LOAD_ERR', error: err.message })
    }
  }

  useEffect(() => { load() }, [])

  function openCreate() {
    dispatch({ type: 'SET', patch: { name: '', privateKey: '', dialog: { mode: 'create' } } })
  }

  function openEdit(item) {
    dispatch({ type: 'SET', patch: { name: item.name || '', privateKey: '', dialog: { mode: 'edit', item } } })
  }

  async function handleSave() {
    dispatch({ type: 'SET', patch: { saving: true } })
    try {
      if (s.dialog.mode === 'create') {
        await createSshKey(s.name, s.privateKey)
        toast.success(t.create + ' OK')
      } else {
        await updateSshKey(s.dialog.item.id, s.name, s.privateKey || undefined)
        toast.success(t.save + ' OK')
      }
      dispatch({ type: 'SET', patch: { dialog: null, saving: false } })
      load()
    } catch (err) {
      toast.error(err.message)
      dispatch({ type: 'SET', patch: { saving: false } })
    }
  }

  async function handleDelete() {
    dispatch({ type: 'SET', patch: { saving: true } })
    try {
      await deleteSshKey(s.confirm.id)
      toast.success(t.delete + ' OK')
      dispatch({ type: 'SET', patch: { confirm: null, saving: false } })
      load()
    } catch (err) {
      toast.error(err.message)
      dispatch({ type: 'SET', patch: { saving: false } })
    }
  }

  function copyFingerprint(fp) {
    navigator.clipboard.writeText(fp).then(() => toast.info('Copied'))
  }

  const headerActions = can('servers.write') ? (
    <Button variant="primary" size="md" onClick={openCreate}>+ {t.addSshKey}</Button>
  ) : null

  return (
    <div>
      <PageHeader title={t.sshKeysTitle} actions={headerActions} />

      {s.error && <p className="text-danger text-sm mb-4">{s.error}</p>}

      {s.loading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
      ) : s.keys.length === 0 ? (
        <EmptyState
          icon={<Key className="w-12 h-12" />}
          title={t.noSshKeys}
          description="Добавьте SSH-ключ для подключения к серверам"
          action={can('servers.write') ? <Button variant="primary" onClick={openCreate}>+ {t.addSshKey}</Button> : null}
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {s.keys.map(k => (
            <Card key={k.id} className="p-4">
              <h3 className="font-semibold mb-1 text-primary">{k.name}</h3>
              {k.fingerprint && (
                <p
                  className="text-xs font-mono text-tertiary truncate cursor-pointer hover:text-primary"
                  onClick={() => copyFingerprint(k.fingerprint)}
                  title="Click to copy"
                >
                  {t.labelFingerprint}: {k.fingerprint}
                </p>
              )}
              {can('servers.write') && (
                <div className="flex gap-2 mt-3">
                  <Button variant="secondary" size="sm" onClick={() => openEdit(k)}>{t.edit}</Button>
                  <Button variant="danger" size="sm" onClick={() => dispatch({ type: 'SET', patch: { confirm: k } })}>{t.delete}</Button>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}

      <CrudDialog
        open={!!s.dialog}
        title={s.dialog?.mode === 'create' ? t.addSshKey : t.editSshKey}
        onClose={() => dispatch({ type: 'SET', patch: { dialog: null } })}
        onSubmit={handleSave} loading={s.saving}
      >
        <FormField label={t.fieldKeyName}>
          <TextInput value={s.name} onChange={v => dispatch({ type: 'SET', patch: { name: v } })} autoFocus />
        </FormField>
        <FormField label={s.dialog?.mode === 'create' ? t.fieldPrivateKey : t.fieldPrivateKeyOptional}>
          <TextArea value={s.privateKey} onChange={v => dispatch({ type: 'SET', patch: { privateKey: v } })} rows={6} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
        </FormField>
      </CrudDialog>

      <ConfirmDialog
        open={!!s.confirm}
        message={t.deleteSshKeyConfirm(s.confirm?.name)}
        onConfirm={handleDelete} onCancel={() => dispatch({ type: 'SET', patch: { confirm: null } })} loading={s.saving}
      />
    </div>
  )
}
