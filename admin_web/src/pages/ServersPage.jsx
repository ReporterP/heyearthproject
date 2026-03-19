import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import {
  fetchServersPaged, createServer, updateServer, deleteServer,
  runServerAction, fetchServerLogs, fetchServerFiles, fetchSshKeys,
} from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

export default function ServersPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchServersPaged)

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [logsModal, setLogsModal] = useState(null)
  const [filesModal, setFilesModal] = useState(null)
  const [sshKeys, setSshKeys] = useState([])

  // form
  const [name, setName] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('22')
  const [sshUser, setSshUser] = useState('root')
  const [sshPassword, setSshPassword] = useState('')
  const [sshKeyId, setSshKeyId] = useState('')
  const [location, setLocation] = useState('')
  const [tags, setTags] = useState('')
  const [countryCode, setCountryCode] = useState('')
  const [serverStatus, setServerStatus] = useState('active')
  const [isExitNode, setIsExitNode] = useState(false)

  async function loadSshKeys() {
    try { const data = await fetchSshKeys(); setSshKeys(data.keys || data || []) } catch {}
  }

  function openCreate() {
    setName(''); setHost(''); setPort('22'); setSshUser('root')
    setSshPassword(''); setSshKeyId(''); setLocation(''); setTags('')
    setCountryCode(''); setServerStatus('active'); setIsExitNode(false)
    loadSshKeys()
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || ''); setHost(item.host || item.ip_address || '')
    setPort(String(item.port || item.ssh_port || 22)); setSshUser(item.ssh_user || 'root')
    setSshPassword(''); setSshKeyId(item.ssh_key_id != null ? String(item.ssh_key_id) : '')
    setLocation(item.location || ''); setTags((item.tags || []).join(', '))
    setCountryCode(item.country_code || ''); setServerStatus(item.status || 'active')
    setIsExitNode(item.is_exit_node || false)
    loadSshKeys()
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        name, host, port: parseInt(port), ssh_user: sshUser,
        location, country_code: countryCode, status: serverStatus,
        is_exit_node: isExitNode,
        tags: tags.split(',').map(t => t.trim()).filter(Boolean),
      }
      if (sshPassword) payload.ssh_password = sshPassword
      if (sshKeyId) payload.ssh_key_id = parseInt(sshKeyId)
      if (dialog.mode === 'create') {
        await createServer(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateServer(dialog.item.id, payload)
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
      await deleteServer(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function doAction(server, action) {
    try {
      toast.info(`${action}...`)
      await runServerAction(server.id, action)
      toast.success(`${action} OK`)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function showLogs(server) {
    try {
      const text = await fetchServerLogs(server.id)
      setLogsModal({ server, text })
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function showFiles(server, path = '/opt/heyearth') {
    try {
      const data = await fetchServerFiles(server.id, path)
      setFilesModal({ server, path, entries: data.entries || data || [] })
    } catch (err) {
      toast.error(err.message)
    }
  }

  const actions = [
    { key: 'restart_agent', label: t.serverRestartAgent },
    { key: 'reprovision', label: t.serverReprovision },
    { key: 'sync_metrics', label: t.serverSyncMetrics },
    { key: 'sync_tunnel', label: t.serverSyncTunnel },
  ]

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.serversTitle}</h2>
        {can('servers.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createServer}
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
        <p className="text-text-secondary">{t.noServers}</p>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {paged.items.map(srv => (
            <div key={srv.id} className="bg-surface-card rounded-xl border border-white/10 p-4">
              <div className="flex items-start justify-between mb-2">
                <div>
                  <h3 className="font-semibold">{srv.name || srv.host}</h3>
                  <p className="text-sm text-text-secondary">{srv.host}:{srv.port || srv.ssh_port || 22}</p>
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge status={srv.status} />
                  {srv.is_exit_node && <span className="text-xs bg-blue-500/20 text-blue-400 px-2 py-0.5 rounded-full">{t.serverExitNode}</span>}
                </div>
              </div>
              <div className="text-xs text-text-secondary mb-3">
                {srv.location && <span>{srv.location}</span>}
                {srv.country_code && <span className="ml-2">{srv.country_code}</span>}
                {srv.tunnel_status && <span className="ml-2">Tunnel: {srv.tunnel_status}</span>}
              </div>
              <div className="flex flex-wrap gap-2">
                {can('servers.write') && actions.map(a => (
                  <button key={a.key} onClick={() => doAction(srv, a.key)}
                    className="text-xs px-2 py-1 rounded bg-surface-alt text-text-secondary hover:text-text-primary transition">
                    {a.label}
                  </button>
                ))}
                <button onClick={() => showLogs(srv)} className="text-xs px-2 py-1 rounded bg-surface-alt text-text-secondary hover:text-text-primary transition">{t.serverLogs}</button>
                <button onClick={() => showFiles(srv)} className="text-xs px-2 py-1 rounded bg-surface-alt text-text-secondary hover:text-text-primary transition">{t.serverFiles}</button>
                {can('servers.write') && (
                  <>
                    <button onClick={() => openEdit(srv)} className="text-xs px-2 py-1 rounded bg-brand/20 text-brand hover:bg-brand/30 transition">{t.edit}</button>
                    <button onClick={() => setConfirm(srv)} className="text-xs px-2 py-1 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 transition">{t.delete}</button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Logs modal */}
      {logsModal && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50" onClick={() => setLogsModal(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-3xl max-h-[80vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-3 border-b border-white/10">
              <h3 className="font-semibold">{t.serverLogsTitle} — {logsModal.server.name}</h3>
              <button onClick={() => setLogsModal(null)} className="text-text-secondary hover:text-text-primary">
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
              </button>
            </div>
            <pre className="flex-1 overflow-auto p-4 text-xs font-mono text-text-secondary whitespace-pre-wrap">{logsModal.text || 'No logs'}</pre>
          </div>
        </div>
      )}

      {/* Files modal */}
      {filesModal && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50" onClick={() => setFilesModal(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-lg max-h-[70vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-3 border-b border-white/10">
              <h3 className="font-semibold">{t.serverFilesTitle}</h3>
              <button onClick={() => setFilesModal(null)} className="text-text-secondary hover:text-text-primary">
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
              </button>
            </div>
            <p className="px-5 py-2 text-xs text-text-secondary font-mono">{filesModal.path}</p>
            <div className="flex-1 overflow-auto px-5 pb-4">
              {filesModal.entries.map((e, i) => (
                <div key={i}
                  className={`text-sm py-1 px-2 rounded cursor-pointer hover:bg-white/5 ${e.is_dir ? 'text-brand' : 'text-text-primary'}`}
                  onClick={() => e.is_dir && showFiles(filesModal.server, filesModal.path + '/' + e.name)}
                >
                  {e.is_dir ? '\uD83D\uDCC1 ' : '\uD83D\uDCC4 '}{e.name}
                  {!e.is_dir && e.size != null && <span className="text-text-secondary text-xs ml-2">({formatSize(e.size)})</span>}
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createServer : t.editServer}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        <FormField label={t.fieldServerName}><TextInput value={name} onChange={setName} autoFocus /></FormField>
        <FormField label={t.fieldHost}><TextInput value={host} onChange={setHost} /></FormField>
        <FormField label={t.fieldPort}><TextInput value={port} onChange={setPort} type="number" /></FormField>
        <FormField label={t.fieldSshUser}><TextInput value={sshUser} onChange={setSshUser} /></FormField>
        <FormField label={t.fieldSshPassword}><TextInput value={sshPassword} onChange={setSshPassword} type="password" /></FormField>
        <FormField label={t.fieldSshKey}>
          <SelectInput value={sshKeyId} onChange={setSshKeyId}
            placeholder={t.fieldSshKeyNone}
            options={sshKeys.map(k => ({ value: String(k.id), label: k.name }))} />
        </FormField>
        <FormField label={t.fieldLocation}><TextInput value={location} onChange={setLocation} /></FormField>
        <FormField label={t.fieldCountryCode}><TextInput value={countryCode} onChange={setCountryCode} /></FormField>
        <FormField label={t.fieldTags}><TextInput value={tags} onChange={setTags} /></FormField>
        <FormField label={t.fieldServerStatus}>
          <SelectInput value={serverStatus} onChange={setServerStatus} options={[
            { value: 'active', label: 'Active' },
            { value: 'inactive', label: 'Inactive' },
            { value: 'maintenance', label: 'Maintenance' },
          ]} />
        </FormField>
        <Checkbox label={t.serverExitNode} checked={isExitNode} onChange={setIsExitNode} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete server "${confirm?.name}"?`}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}
