import { useState, useEffect, useRef, useMemo } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import {
  fetchServersPaged, createServer, updateServer, deleteServer,
  runServerAction, fetchServerLogs, fetchServerFiles, fetchSshKeys,
  fetchServerDetail,
} from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

/* ── SVG icon helpers ── */
const Icon = ({ d, className = 'w-4 h-4' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d={d} />
  </svg>
)

const IconRestart  = () => <Icon d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
const IconProvision = () => <Icon d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
const IconSync     = () => <Icon d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4" />
const IconTunnel   = () => <Icon d="M13 10V3L4 14h7v7l9-11h-7z" />
const IconLogs     = () => <Icon d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
const IconFolder   = () => <Icon d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
const IconEdit     = () => <Icon d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
const IconTrash    = () => <Icon d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
const IconCopy     = () => <Icon d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
const IconList     = () => <Icon d="M4 6h16M4 10h16M4 14h16M4 18h16" />
const IconGrid     = () => <Icon d="M4 5a1 1 0 011-1h4a1 1 0 011 1v4a1 1 0 01-1 1H5a1 1 0 01-1-1V5zm10 0a1 1 0 011-1h4a1 1 0 011 1v4a1 1 0 01-1 1h-15a1 1 0 01-1-1V5zM4 15a1 1 0 011-1h4a1 1 0 011 1v4a1 1 0 01-1 1H5a1 1 0 01-1-1v-4zm10 0a1 1 0 011-1h4a1 1 0 011 1v4a1 1 0 01-1 1h-4a1 1 0 01-1-1v-4z" />
const IconServer   = () => <Icon d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-7-4h.01M12 16h.01" />
const IconClose    = () => <Icon className="w-5 h-5" d="M6 18L18 6M6 6l12 12" />
const IconChart    = () => <Icon d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />

/* ── Tunnel status badge (colored) ── */
function TunnelBadge({ status }) {
  if (!status) return null
  const key = status.toLowerCase()
  const colors = {
    healthy:     'bg-green-500/20 text-green-400',
    configuring: 'bg-yellow-500/20 text-yellow-400',
    error:       'bg-red-500/20 text-red-400',
  }
  const cls = colors[key] || 'bg-gray-500/20 text-gray-400'
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${cls}`}>
      <IconTunnel />
      {status}
    </span>
  )
}

/* ── Tag chip ── */
const TAG_COLORS = [
  'bg-blue-500/20 text-blue-400',
  'bg-purple-500/20 text-purple-400',
  'bg-teal-500/20 text-teal-400',
  'bg-orange-500/20 text-orange-400',
  'bg-pink-500/20 text-pink-400',
  'bg-cyan-500/20 text-cyan-400',
  'bg-lime-500/20 text-lime-400',
  'bg-amber-500/20 text-amber-400',
]

function TagChip({ tag }) {
  const colorIdx = Math.abs([...tag].reduce((a, c) => a + c.charCodeAt(0), 0)) % TAG_COLORS.length
  return (
    <span className={`inline-block px-2 py-0.5 rounded-full text-xs font-medium ${TAG_COLORS[colorIdx]}`}>
      {tag}
    </span>
  )
}

/* ── Copy button ── */
function CopyButton({ text, toast }) {
  const [copied, setCopied] = useState(false)
  async function handleCopy(e) {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Copy failed')
    }
  }
  return (
    <button onClick={handleCopy} title="Copy IP"
      className="inline-flex items-center gap-1 text-text-secondary hover:text-brand transition">
      {copied
        ? <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
        : <IconCopy />
      }
    </button>
  )
}

/* ── Metrics mini-panel (shown in detail) ── */
function MetricsPanel({ metrics }) {
  if (!metrics) return null
  // Backend returns: cpu_usage, memory_usage, active_connections, synced_at
  const items = [
    { label: 'CPU', value: metrics.cpu_usage != null ? `${Number(metrics.cpu_usage).toFixed(1)}%` : (metrics.cpu_percent != null ? `${Number(metrics.cpu_percent).toFixed(1)}%` : null) },
    { label: 'RAM', value: metrics.memory_usage != null ? `${Number(metrics.memory_usage).toFixed(1)}%` : (metrics.memory_percent != null ? `${Number(metrics.memory_percent).toFixed(1)}%` : null) },
    { label: 'Disk', value: metrics.disk_usage != null ? `${Number(metrics.disk_usage).toFixed(1)}%` : (metrics.disk_percent != null ? `${Number(metrics.disk_percent).toFixed(1)}%` : null) },
    { label: 'Connections', value: metrics.active_connections != null ? String(metrics.active_connections) : null },
    { label: 'Synced', value: metrics.synced_at ? formatRelativeTime(metrics.synced_at) : null },
    { label: 'Uptime', value: metrics.uptime ? formatUptime(metrics.uptime) : null },
    { label: 'Load', value: metrics.load_avg != null ? String(metrics.load_avg) : null },
  ].filter(i => i.value != null)
  if (items.length === 0) return <p className="text-xs text-text-secondary mt-2">No metrics data available</p>
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 mt-3 p-3 rounded-lg bg-surface-alt/50">
      {items.map(i => (
        <div key={i.label}>
          <p className="text-[10px] uppercase tracking-wider text-text-secondary">{i.label}</p>
          <p className="text-sm font-semibold">{i.value}</p>
        </div>
      ))}
    </div>
  )
}

/* ── Status filter tabs ── */
const STATUS_FILTERS = ['all', 'online', 'provisioning', 'offline', 'maintenance']

function StatusFilterBar({ value, onChange, counts }) {
  const { t } = useT()
  const labels = {
    all: t.serverFilterAll || 'Все',
    online: t.serverFilterOnline || 'Онлайн',
    provisioning: t.serverFilterProvisioning || 'Настройка',
    offline: t.serverFilterOffline || 'Оффлайн',
    maintenance: t.serverFilterMaintenance || 'Обслуживание',
  }
  return (
    <div className="flex items-center gap-1 mb-4 flex-wrap">
      {STATUS_FILTERS.map(s => {
        const active = value === s
        const count = counts[s]
        return (
          <button key={s} onClick={() => onChange(s)}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium transition ${
              active
                ? 'bg-brand text-white'
                : 'bg-surface-alt text-text-secondary hover:text-text-primary hover:bg-surface-alt/80'
            }`}>
            {labels[s]}
            {count != null && (
              <span className={`ml-1.5 px-1.5 py-0.5 rounded-full text-xs ${
                active ? 'bg-white/20' : 'bg-white/10'
              }`}>{count}</span>
            )}
          </button>
        )
      })}
    </div>
  )
}

/* ── Logs Modal (improved) ── */
function LogsModal({ logsModal, setLogsModal, fetchLogs, toast }) {
  const { t } = useT()
  const [service, setService] = useState('node-agent')
  const [tail, setTail] = useState(100)
  const [text, setText] = useState(logsModal.text || '')
  const [loading, setLoading] = useState(false)
  const preRef = useRef(null)

  const services = [
    { value: 'node-agent', label: 'Node Agent' },
    { value: 'xray', label: 'Xray' },
    { value: 'nginx', label: 'Nginx' },
  ]
  const tailOptions = [50, 100, 200, 500, 1000]

  // auto-scroll to bottom
  useEffect(() => {
    if (preRef.current) {
      preRef.current.scrollTop = preRef.current.scrollHeight
    }
  }, [text])

  async function doRefresh() {
    setLoading(true)
    try {
      const newText = await fetchLogs(logsModal.server.id, { service, tail })
      setText(newText)
    } catch (err) {
      toast.error(err.message)
    } finally {
      setLoading(false)
    }
  }

  // Reload when service or tail changes
  useEffect(() => {
    doRefresh()
  }, [service, tail])

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50" onClick={() => setLogsModal(null)}>
      <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-4xl max-h-[85vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-white/10">
          <div className="flex items-center gap-3">
            <h3 className="font-semibold">{t.serverLogsTitle} — {logsModal.server.name}</h3>
          </div>
          <button onClick={() => setLogsModal(null)} className="text-text-secondary hover:text-text-primary">
            <IconClose />
          </button>
        </div>

        {/* Toolbar */}
        <div className="flex items-center gap-3 px-5 py-2 border-b border-white/5 flex-wrap">
          <label className="text-xs text-text-secondary">{t.serverLogsService}:</label>
          <select value={service} onChange={e => setService(e.target.value)}
            className="px-2 py-1 rounded-lg bg-surface-alt border border-white/10 text-sm text-text-primary focus:border-brand outline-none">
            {services.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>

          <label className="text-xs text-text-secondary ml-2">{t.serverLogsTailLines || 'Lines'}:</label>
          <select value={tail} onChange={e => setTail(Number(e.target.value))}
            className="px-2 py-1 rounded-lg bg-surface-alt border border-white/10 text-sm text-text-primary focus:border-brand outline-none">
            {tailOptions.map(n => <option key={n} value={n}>{n}</option>)}
          </select>

          <button onClick={doRefresh} disabled={loading}
            className="ml-auto flex items-center gap-1 px-3 py-1 rounded-lg bg-brand/20 text-brand text-sm hover:bg-brand/30 transition disabled:opacity-50">
            <IconRestart />
            {t.serverLogsRefresh}
          </button>
        </div>

        {/* Logs content */}
        <pre ref={preRef}
          className="flex-1 overflow-auto p-4 text-xs font-mono text-text-secondary whitespace-pre-wrap">
          {loading ? (t.loading || 'Loading...') : (text || 'No logs')}
        </pre>
      </div>
    </div>
  )
}

/* ── Main page ── */
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
  const [viewMode, setViewMode] = useState('cards') // 'cards' | 'list'
  const [statusFilter, setStatusFilter] = useState('all')
  const [expandedMetrics, setExpandedMetrics] = useState({}) // serverId -> detail data

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
    try {
      const data = await fetchSshKeys()
      const keys = Array.isArray(data) ? data : (Array.isArray(data?.keys) ? data.keys : [])
      setSshKeys(keys)
    } catch {}
  }

  // Filtered items
  const filteredItems = useMemo(() => {
    if (statusFilter === 'all') return paged.items
    return paged.items.filter(srv => (srv.status || '').toLowerCase() === statusFilter)
  }, [paged.items, statusFilter])

  // Status counts
  const statusCounts = useMemo(() => {
    const counts = { all: paged.items.length, online: 0, provisioning: 0, offline: 0, maintenance: 0 }
    paged.items.forEach(srv => {
      const s = (srv.status || '').toLowerCase()
      if (counts[s] != null) counts[s]++
    })
    return counts
  }, [paged.items])

  const onlineCount = useMemo(() =>
    paged.items.filter(s => (s.status || '').toLowerCase() === 'online').length,
    [paged.items]
  )

  function openCreate() {
    setName(''); setHost(''); setPort('22'); setSshUser('root')
    setSshPassword(''); setSshKeyId(''); setLocation(''); setTags('')
    setCountryCode(''); setServerStatus('online'); setIsExitNode(false)
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
        name, ip_address: host, ssh_port: parseInt(port), ssh_user: sshUser,
        country_code: countryCode, status: serverStatus,
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

  async function toggleMetrics(serverId) {
    // If already expanded — collapse
    if (expandedMetrics[serverId]) {
      setExpandedMetrics(prev => {
        const n = { ...prev }; delete n[serverId]; return n
      })
      return
    }
    // Expand — fetch detail
    try {
      setExpandedMetrics(prev => ({ ...prev, [serverId]: 'loading' }))
      const detail = await fetchServerDetail(serverId)
      setExpandedMetrics(prev => ({ ...prev, [serverId]: detail }))
    } catch (err) {
      setExpandedMetrics(prev => {
        const n = { ...prev }; delete n[serverId]; return n
      })
      toast.error(err.message)
    }
  }

  const actions = [
    { key: 'restart_agent', label: t.serverRestartAgent, icon: <IconRestart /> },
    { key: 'reprovision', label: t.serverReprovision, icon: <IconProvision /> },
    { key: 'sync_metrics', label: t.serverSyncMetrics, icon: <IconSync /> },
    { key: 'sync_tunnel', label: t.serverSyncTunnel, icon: <IconTunnel /> },
  ]

  const ipAddress = (srv) => srv.ip_address || srv.host || ''
  const lastHealthCheck = (srv) => srv.last_health_check || srv.last_seen_at || srv.updated_at
  const activeConns = (srv) => srv.active_connections ?? srv.connections_count

  /* ── Card view for a single server ── */
  function ServerCard({ srv }) {
    const ip = ipAddress(srv)
    const healthTime = lastHealthCheck(srv)
    const conns = activeConns(srv)
    const detail = expandedMetrics[srv.id]

    return (
      <div className="bg-surface-card rounded-xl border border-white/10 p-4 flex flex-col">
        {/* Top row: name + status */}
        <div className="flex items-start justify-between mb-2">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="font-semibold truncate">{srv.name || ip}</h3>
              {srv.is_exit_node && (
                <span className="shrink-0 text-xs bg-blue-500/20 text-blue-400 px-2 py-0.5 rounded-full">{t.serverExitNode}</span>
              )}
            </div>
            {/* IP address prominent */}
            <div className="flex items-center gap-1.5 mt-1">
              <span className="text-sm font-mono text-brand">{ip}</span>
              {ip && <CopyButton text={ip} toast={toast} />}
              <span className="text-xs text-text-secondary ml-1">:{srv.port || srv.ssh_port || 22}</span>
            </div>
          </div>
          <StatusBadge status={srv.status} />
        </div>

        {/* Details row */}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-text-secondary mb-2">
          {srv.location && <span>{srv.location}</span>}
          {srv.country_code && <span>{srv.country_code.toUpperCase()}</span>}
          {conns != null && (
            <span className="flex items-center gap-1">
              <IconServer />
              {conns} {t.serverActiveConns || 'conn.'}
            </span>
          )}
          {healthTime && (
            <span title={healthTime}>
              {t.serverLastHealth || 'Health'}: {formatRelativeTime(healthTime)}
            </span>
          )}
        </div>

        {/* Tunnel status */}
        <div className="flex flex-wrap items-center gap-2 mb-2">
          <TunnelBadge status={srv.tunnel_status} />
        </div>

        {/* Tags */}
        {srv.tags && srv.tags.length > 0 && (
          <div className="flex flex-wrap gap-1 mb-3">
            {srv.tags.map(tag => <TagChip key={tag} tag={tag} />)}
          </div>
        )}

        {/* Metrics toggle */}
        <button onClick={() => toggleMetrics(srv.id)}
          className="flex items-center gap-1 text-xs text-text-secondary hover:text-brand transition mb-2 self-start">
          <IconChart />
          {detail ? (t.serverHideMetrics || 'Hide metrics') : (t.serverShowMetrics || 'Show metrics')}
        </button>
        {detail === 'loading' && <p className="text-xs text-text-secondary mt-2">{t.loading || 'Loading...'}</p>}
        {detail && detail !== 'loading' && <MetricsPanel metrics={detail.load_metrics || detail.metrics || detail} />}

        {/* Actions */}
        <div className="flex flex-wrap gap-1.5 mt-auto pt-3 border-t border-white/5">
          {can('servers.write') && actions.map(a => (
            <button key={a.key} onClick={() => doAction(srv, a.key)}
              className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary hover:bg-surface-alt/80 transition"
              title={a.label}>
              {a.icon}
              <span className="hidden sm:inline">{a.label}</span>
            </button>
          ))}
          <button onClick={() => showLogs(srv)}
            className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary transition"
            title={t.serverLogs}>
            <IconLogs />
            <span className="hidden sm:inline">{t.serverLogs}</span>
          </button>
          <button onClick={() => showFiles(srv)}
            className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary transition"
            title={t.serverFiles}>
            <IconFolder />
            <span className="hidden sm:inline">{t.serverFiles}</span>
          </button>
          {can('servers.write') && (
            <>
              <button onClick={() => openEdit(srv)}
                className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-brand/20 text-brand hover:bg-brand/30 transition"
                title={t.edit}>
                <IconEdit />
                <span className="hidden sm:inline">{t.edit}</span>
              </button>
              <button onClick={() => setConfirm(srv)}
                className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-red-500/20 text-red-400 hover:bg-red-500/30 transition"
                title={t.delete}>
                <IconTrash />
                <span className="hidden sm:inline">{t.delete}</span>
              </button>
            </>
          )}
        </div>
      </div>
    )
  }

  /* ── List view row ── */
  function ServerRow({ srv }) {
    const ip = ipAddress(srv)
    const healthTime = lastHealthCheck(srv)
    const conns = activeConns(srv)

    return (
      <tr className="border-b border-white/5 hover:bg-surface-alt/30 transition">
        <td className="py-3 px-3">
          <div className="font-semibold text-sm">{srv.name || ip}</div>
          {srv.location && <div className="text-xs text-text-secondary">{srv.location}</div>}
        </td>
        <td className="py-3 px-3">
          <div className="flex items-center gap-1.5">
            <span className="font-mono text-sm text-brand">{ip}</span>
            {ip && <CopyButton text={ip} toast={toast} />}
          </div>
        </td>
        <td className="py-3 px-3"><StatusBadge status={srv.status} /></td>
        <td className="py-3 px-3"><TunnelBadge status={srv.tunnel_status} /></td>
        <td className="py-3 px-3 text-sm text-center">{conns ?? '—'}</td>
        <td className="py-3 px-3 text-xs text-text-secondary">
          {healthTime ? formatRelativeTime(healthTime) : '—'}
        </td>
        <td className="py-3 px-3">
          <div className="flex flex-wrap gap-1">
            {(srv.tags || []).map(tag => <TagChip key={tag} tag={tag} />)}
            {srv.is_exit_node && (
              <span className="text-xs bg-blue-500/20 text-blue-400 px-2 py-0.5 rounded-full">{t.serverExitNode}</span>
            )}
          </div>
        </td>
        <td className="py-3 px-3">
          <div className="flex items-center gap-1">
            <button onClick={() => showLogs(srv)} className="p-1 rounded hover:bg-surface-alt transition text-text-secondary hover:text-text-primary" title={t.serverLogs}><IconLogs /></button>
            <button onClick={() => showFiles(srv)} className="p-1 rounded hover:bg-surface-alt transition text-text-secondary hover:text-text-primary" title={t.serverFiles}><IconFolder /></button>
            <button onClick={() => toggleMetrics(srv.id)} className="p-1 rounded hover:bg-surface-alt transition text-text-secondary hover:text-text-primary" title={t.serverShowMetrics || 'Metrics'}><IconChart /></button>
            {can('servers.write') && (
              <>
                <button onClick={() => openEdit(srv)} className="p-1 rounded hover:bg-brand/20 transition text-brand" title={t.edit}><IconEdit /></button>
                <button onClick={() => setConfirm(srv)} className="p-1 rounded hover:bg-red-500/20 transition text-red-400" title={t.delete}><IconTrash /></button>
              </>
            )}
          </div>
        </td>
      </tr>
    )
  }

  return (
    <div>
      {/* Header with counts */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-2xl font-bold">{t.serversTitle}</h2>
          <p className="text-sm text-text-secondary mt-0.5">
            {t.serverTotalCount || 'Total'}: {paged.total}
            <span className="mx-2">|</span>
            <span className="text-green-400">{t.serverOnlineCount || 'Online'}: {onlineCount}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          {/* View mode toggle */}
          <div className="flex items-center rounded-lg border border-white/10 overflow-hidden">
            <button onClick={() => setViewMode('cards')}
              className={`p-2 transition ${viewMode === 'cards' ? 'bg-brand text-white' : 'bg-surface-alt text-text-secondary hover:text-text-primary'}`}
              title={t.serverViewCards || 'Cards'}>
              <IconGrid />
            </button>
            <button onClick={() => setViewMode('list')}
              className={`p-2 transition ${viewMode === 'list' ? 'bg-brand text-white' : 'bg-surface-alt text-text-secondary hover:text-text-primary'}`}
              title={t.serverViewList || 'List'}>
              <IconList />
            </button>
          </div>
          {can('servers.write') && (
            <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
              + {t.createServer}
            </button>
          )}
        </div>
      </div>

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {/* Status filter */}
      <StatusFilterBar value={statusFilter} onChange={setStatusFilter} counts={statusCounts} />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {filteredItems.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noServers}</p>
      ) : viewMode === 'cards' ? (
        /* ── Card grid ── */
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filteredItems.map(srv => <ServerCard key={srv.id} srv={srv} />)}
        </div>
      ) : (
        /* ── List table ── */
        <div className="overflow-x-auto rounded-xl border border-white/10">
          <table className="w-full text-left">
            <thead>
              <tr className="bg-surface-alt/50 text-xs text-text-secondary uppercase tracking-wider">
                <th className="py-2 px-3 font-medium">{t.fieldServerName}</th>
                <th className="py-2 px-3 font-medium">IP</th>
                <th className="py-2 px-3 font-medium">{t.fieldStatus}</th>
                <th className="py-2 px-3 font-medium">Tunnel</th>
                <th className="py-2 px-3 font-medium text-center">{t.serverActiveConns || 'Conn.'}</th>
                <th className="py-2 px-3 font-medium">{t.serverLastHealth || 'Health'}</th>
                <th className="py-2 px-3 font-medium">{t.fieldTags}</th>
                <th className="py-2 px-3 font-medium">{t.serverActionsCol || 'Actions'}</th>
              </tr>
            </thead>
            <tbody>
              {filteredItems.map(srv => <ServerRow key={srv.id} srv={srv} />)}
            </tbody>
          </table>
        </div>
      )}

      {/* Logs modal (improved) */}
      {logsModal && (
        <LogsModal
          logsModal={logsModal}
          setLogsModal={setLogsModal}
          fetchLogs={fetchServerLogs}
          toast={toast}
        />
      )}

      {/* Files modal */}
      {filesModal && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50" onClick={() => setFilesModal(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 w-full max-w-lg max-h-[70vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-3 border-b border-white/10">
              <h3 className="font-semibold">{t.serverFilesTitle}</h3>
              <button onClick={() => setFilesModal(null)} className="text-text-secondary hover:text-text-primary">
                <IconClose />
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
            { value: 'online', label: 'Онлайн' },
            { value: 'provisioning', label: 'Настройка' },
            { value: 'offline', label: 'Оффлайн' },
            { value: 'maintenance', label: 'Обслуживание' },
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

/* ── Helpers ── */

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

function formatUptime(seconds) {
  if (typeof seconds !== 'number') return String(seconds)
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

function formatRelativeTime(isoString) {
  if (!isoString) return ''
  try {
    const date = new Date(isoString)
    const now = new Date()
    const diffMs = now - date
    const diffSec = Math.floor(diffMs / 1000)
    if (diffSec < 60) return `${diffSec}s ago`
    const diffMin = Math.floor(diffSec / 60)
    if (diffMin < 60) return `${diffMin}m ago`
    const diffH = Math.floor(diffMin / 60)
    if (diffH < 24) return `${diffH}h ago`
    const diffD = Math.floor(diffH / 24)
    return `${diffD}d ago`
  } catch {
    return isoString
  }
}
