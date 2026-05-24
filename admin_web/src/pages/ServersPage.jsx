import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Server } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import {
  fetchServersPaged, createServer, updateServer, deleteServer,
  runServerAction, fetchServerLogs, fetchServerFiles, fetchSshKeys,
  fetchServerDetail, generateTunnelCreds,
} from '../api/admin.js'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import ServerLifecycleBadge from '../components/ServerLifecycleBadge.jsx'
import StatusBadge from '../components/StatusBadge.jsx'
import ProvisionProgressDialog from '../components/ProvisionProgressDialog.jsx'
import ConfigEditorDialog from '../components/ConfigEditorDialog.jsx'
import { formatRelativeTime } from '../utils/format.js'
import {
  Button,
  Input,
  Select,
  Badge,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  TableSkeleton,
  EmptyState,
  Dialog,
} from '../components/ui/index.js'

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
const IconConfig   = () => <Icon d="M10 20l4-16m4 4l4 4-4 4M6 16l-4-4 4-4" />

/* ── Tunnel status badge (colored) ── */
function TunnelBadge({ status }) {
  if (!status) return null
  const key = status.toLowerCase()
  const colors = {
    healthy:     'bg-success/20 text-success',
    configuring: 'bg-yellow-500/20 text-yellow-400',
    error:       'bg-danger/20 text-danger',
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
      className="inline-flex items-center gap-1 text-secondary hover:text-accent transition">
      {copied
        ? <svg className="w-4 h-4 text-success" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
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
  if (items.length === 0) return <p className="text-xs text-secondary mt-2">No metrics data available</p>
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 gap-2 mt-3 p-3 rounded-lg bg-elevated/50">
      {items.map(i => (
        <div key={i.label}>
          <p className="text-[10px] uppercase tracking-wider text-secondary">{i.label}</p>
          <p className="text-sm font-semibold">{i.value}</p>
        </div>
      ))}
    </div>
  )
}

/* ── Status filter (now via Select bound to useUrlState lifecycle_state) ── */
const LIFECYCLE_OPTIONS = [
  { value: '', label: 'Все состояния' },
  { value: 'provisioning', label: 'Провижн' },
  { value: 'online', label: 'Online' },
  { value: 'offline', label: 'Offline' },
  { value: 'deleted', label: 'Deleted' },
]

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
    { value: 'hysteria2', label: 'Hysteria2' },
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
      <div className="bg-elevated rounded-xl border border-default w-full max-w-4xl max-h-[85vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-default">
          <div className="flex items-center gap-3">
            <h3 className="font-semibold">{t.serverLogsTitle} — {logsModal.server.name}</h3>
          </div>
          <button onClick={() => setLogsModal(null)} className="text-secondary hover:text-primary">
            <IconClose />
          </button>
        </div>

        {/* Toolbar */}
        <div className="flex items-center gap-3 px-5 py-2 border-b border-white/5 flex-wrap">
          <label className="text-xs text-secondary">{t.serverLogsService}:</label>
          <select value={service} onChange={e => setService(e.target.value)}
            className="px-2 py-1 rounded-lg bg-elevated border border-default text-sm text-primary focus:border-brand outline-none">
            {services.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>

          <label className="text-xs text-secondary ml-2">{t.serverLogsTailLines || 'Lines'}:</label>
          <select value={tail} onChange={e => setTail(Number(e.target.value))}
            className="px-2 py-1 rounded-lg bg-elevated border border-default text-sm text-primary focus:border-brand outline-none">
            {tailOptions.map(n => <option key={n} value={n}>{n}</option>)}
          </select>

          <button onClick={doRefresh} disabled={loading}
            className="ml-auto flex items-center gap-1 px-3 py-1 rounded-lg bg-accent/20 text-accent text-sm hover:bg-accent/30 transition disabled:opacity-50">
            <IconRestart />
            {t.serverLogsRefresh}
          </button>
        </div>

        {/* Logs content */}
        <pre ref={preRef}
          className="flex-1 overflow-auto p-4 text-xs font-mono text-secondary whitespace-pre-wrap">
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
  const navigate = useNavigate()

  const [f, setF] = useUrlState({ search: '', lifecycle_state: '', country: '', sort: 'id', dir: 'asc', page: 1 })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const pageSize = 20
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [searchInput, setSearchInput] = useState(f.search)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    const params = { page: f.page, pageSize, search: f.search, sort: f.sort, dir: f.dir }
    if (f.lifecycle_state) params.lifecycle_state = f.lifecycle_state
    if (f.country) params.country = f.country
    fetchServersPaged(params)
      .then(data => {
        setItems(data.items || data.data || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.search, f.sort, f.dir, f.lifecycle_state, f.country])

  useEffect(() => { load() }, [load])

  const reload = load
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  // Compatibility shim — old code references paged.items / paged.reload
  const paged = {
    items, total, page: f.page, totalPages, loading, error, reload,
    setPage: (p) => setF({ page: p }),
    search: f.search,
    doSearch: (val) => setF({ search: val, page: 1 }),
  }

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [logsModal, setLogsModal] = useState(null)
  const [filesModal, setFilesModal] = useState(null)
  const [sshKeys, setSshKeys] = useState([])
  const [viewMode, setViewMode] = useState('cards') // 'cards' | 'list'
  const [configServer, setConfigServer] = useState(null) // server for config editor
  const [expandedMetrics, setExpandedMetrics] = useState({}) // serverId -> detail data
  const [progressModal, setProgressModal] = useState(null) // { id, name }
  const [actionConfirm, setActionConfirm] = useState(null) // { server, action }

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
  // Tunnel config (все серверы в админке — exit nodes)
  const isExitNode = true
  const [tunnelProtocol, setTunnelProtocol] = useState('vless')
  const [tunnelPort, setTunnelPort] = useState('443')
  const [tunnelUuid, setTunnelUuid] = useState('')
  const [tunnelPublicKey, setTunnelPublicKey] = useState('')
  const [tunnelPrivateKey, setTunnelPrivateKey] = useState('')
  const [tunnelShortId, setTunnelShortId] = useState('')
  const [tunnelSni, setTunnelSni] = useState('')
  const [tunnelSsMethod, setTunnelSsMethod] = useState('2022-blake3-aes-128-gcm')
  const [tunnelSsPassword, setTunnelSsPassword] = useState('')

  async function loadSshKeys() {
    try {
      const data = await fetchSshKeys()
      const keys = Array.isArray(data) ? data : (Array.isArray(data?.keys) ? data.keys : [])
      setSshKeys(keys)
    } catch {}
  }

  // Realtime polling — refresh servers list every 10s when tab is visible
  // (backend StartServerMetricsCollector обновляет metrics_synced_at)
  useEffect(() => {
    const interval = setInterval(() => {
      if (document.visibilityState === 'visible') {
        paged.reload()
      }
    }, 10000)
    return () => clearInterval(interval)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Items already filtered by backend via ?lifecycle_state / ?country
  const filteredItems = items

  const onlineCount = useMemo(() =>
    items.filter(s => (s.status || '').toLowerCase() === 'online').length,
    [items]
  )

  async function openCreate() {
    setName(''); setHost(''); setPort('22'); setSshUser('root')
    setSshPassword(''); setSshKeyId(''); setLocation(''); setTags('')
    setCountryCode(''); setServerStatus('online')
    setTunnelProtocol('vless'); setTunnelPort('443')
    setTunnelSni('nltimes.nl')
    setTunnelSsMethod('2022-blake3-aes-128-gcm')
    // Автогенерация credentials
    try {
      const creds = await generateTunnelCreds()
      setTunnelUuid(creds.vless_uuid || '')
      setTunnelPublicKey(creds.reality_public_key || '')
      setTunnelPrivateKey(creds.reality_private_key || '')
      setTunnelShortId(creds.reality_short_id || '')
      setTunnelSsPassword(creds.ss_password || '')
    } catch {
      setTunnelUuid(''); setTunnelPublicKey(''); setTunnelPrivateKey('')
      setTunnelShortId(''); setTunnelSsPassword('')
    }
    loadSshKeys()
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setName(item.name || ''); setHost(item.host || item.ip_address || '')
    setPort(String(item.port || item.ssh_port || 22)); setSshUser(item.ssh_user || 'root')
    setSshPassword(''); setSshKeyId(item.ssh_key_id != null ? String(item.ssh_key_id) : '')
    setLocation(item.location || ''); setTags((item.tags || []).join(', '))
    setCountryCode(item.country_code || ''); setServerStatus(item.status || 'online')
    setTunnelProtocol(item.tunnel_protocol || 'vless')
    setTunnelPort(String(item.tunnel_port || 443))
    setTunnelUuid(item.tunnel_uuid || '')
    setTunnelPublicKey(item.tunnel_public_key || '')
    setTunnelPrivateKey(item.tunnel_private_key || '')
    setTunnelShortId(item.tunnel_short_id || '')
    setTunnelSni(item.tunnel_sni || '')
    setTunnelSsMethod(item.tunnel_ss_method || '2022-blake3-aes-128-gcm')
    setTunnelSsPassword(item.tunnel_ss_password || '')
    loadSshKeys()
    setDialog({ mode: 'edit', item })
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        name, ip_address: host, ssh_port: parseInt(port), ssh_user: sshUser,
        country_code: countryCode, status: serverStatus,
        is_exit_node: isExitNode,
        tunnel_protocol: tunnelProtocol,
        tunnel_port: parseInt(tunnelPort) || 443,
      }
      if (sshPassword) payload.ssh_password = sshPassword
      if (sshKeyId) payload.ssh_key_id = parseInt(sshKeyId)
      if (tunnelUuid) payload.tunnel_uuid = tunnelUuid
      if (tunnelPublicKey) payload.tunnel_public_key = tunnelPublicKey
      if (tunnelPrivateKey) payload.tunnel_private_key = tunnelPrivateKey
      if (tunnelShortId) payload.tunnel_short_id = tunnelShortId
      if (tunnelSni) payload.tunnel_sni = tunnelSni
      if (tunnelSsMethod) payload.tunnel_ss_method = tunnelSsMethod
      if (tunnelSsPassword) payload.tunnel_ss_password = tunnelSsPassword
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

  async function doAction(srv, actionDef) {
    if (actionDef.confirm) {
      setActionConfirm({ server: srv, action: actionDef })
      return
    }
    executeAction(srv, actionDef)
  }

  async function executeAction(srv, actionDef) {
    if (actionDef.async) {
      try {
        await runServerAction(srv.id, actionDef.key)
        setProgressModal({ id: srv.id, name: srv.name })
      } catch (err) {
        toast.error(err.message)
      }
    } else {
      try {
        toast.info(`${actionDef.label}...`)
        await runServerAction(srv.id, actionDef.key)
        toast.success(`${actionDef.label} OK`)
        paged.reload()
      } catch (err) {
        toast.error(err.message)
      }
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
      let entries = data.entries || []
      // Fallback: parse raw listing if entries not provided
      if (entries.length === 0 && data.listing) {
        entries = parseLsListing(data.listing)
      }
      setFilesModal({ server, path, entries })
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
    { key: 'setup_exit_node', label: t.serverSetupExitNode, desc: t.serverSetupExitNodeDesc, icon: <IconProvision />, exitOnly: true, async: true, confirm: true },
    { key: 'restart_agent', label: t.serverRestartAgent, desc: t.serverRestartAgentDesc, icon: <IconRestart />, confirm: true },
    { key: 'sync_metrics', label: t.serverSyncMetrics, desc: t.serverSyncMetricsDesc, icon: <IconSync /> },
    { key: 'sync_tunnel', label: t.serverSyncTunnel, desc: t.serverSyncTunnelDesc, icon: <IconTunnel /> },
    { key: 'fetch_tunnel_creds', label: t.serverFetchCreds, desc: t.serverFetchCredsDesc, icon: <IconTunnel />, exitOnly: true },
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
      <div className="bg-elevated rounded-xl border border-default p-4 flex flex-col">
        {/* Top row: name + status */}
        <div className="flex items-start justify-between mb-2">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="font-semibold truncate cursor-pointer hover:text-accent transition" onClick={() => navigate(`/servers/${srv.id}`)}>{srv.name || ip}</h3>
            </div>
            {/* IP address prominent */}
            <div className="flex items-center gap-1.5 mt-1">
              <span className="text-sm font-mono text-accent">{ip}</span>
              {ip && <CopyButton text={ip} toast={toast} />}
              <span className="text-xs text-secondary ml-1">:{srv.port || srv.ssh_port || 22}</span>
            </div>
          </div>
          <StatusBadge status={srv.status} />
        </div>

        {/* Details row */}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-secondary mb-2">
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

        {/* Tunnel status + protocol + lifecycle */}
        <div className="flex flex-wrap items-center gap-2 mb-2">
          <TunnelBadge status={srv.tunnel_status} />
          {srv.is_exit_node && srv.mtproto_status && srv.mtproto_status !== 'unknown' && (
            <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] ${
              srv.mtproto_status === 'healthy' ? 'bg-success/15 text-success' :
              srv.mtproto_status === 'unhealthy' ? 'bg-danger/15 text-danger' :
              'bg-info/15 text-info'
            }`} title={`MTProto: ${srv.mtproto_status}`}>
              MTP
            </span>
          )}
          {srv.is_exit_node && <ServerLifecycleBadge state={srv.lifecycle_state || 'online'} />}
          {srv.tunnel_protocol && (
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-accent/10 text-accent font-mono uppercase">{srv.tunnel_protocol}</span>
          )}
          {srv.tunnel_port > 0 && (
            <span className="text-[10px] text-secondary">:{srv.tunnel_port}</span>
          )}
        </div>
        {srv.tunnel_status === 'unhealthy' && srv.tunnel_error && (
          <p className="text-xs text-danger mb-2">{t.serverTunnelError}: {srv.tunnel_error}</p>
        )}

        {/* Tags */}
        {srv.tags && srv.tags.length > 0 && (
          <div className="flex flex-wrap gap-1 mb-3">
            {srv.tags.map(tag => <TagChip key={tag} tag={tag} />)}
          </div>
        )}

        {/* Metrics toggle */}
        <button onClick={() => toggleMetrics(srv.id)}
          className="flex items-center gap-1 text-xs text-secondary hover:text-accent transition mb-2 self-start">
          <IconChart />
          {detail ? (t.serverHideMetrics || 'Hide metrics') : (t.serverShowMetrics || 'Show metrics')}
        </button>
        {detail === 'loading' && <p className="text-xs text-secondary mt-2">{t.loading || 'Loading...'}</p>}
        {detail && detail !== 'loading' && <MetricsPanel metrics={{ ...(detail.load_metrics || detail.metrics || detail), synced_at: srv.metrics_synced_at || (detail.load_metrics?.synced_at) }} />}

        {/* Actions */}
        <div className="flex flex-wrap gap-1.5 mt-auto pt-3 border-t border-white/5">
          {can('servers.write') && actions.filter(a => !a.exitOnly || srv.is_exit_node).map(a => (
            <button key={a.key} onClick={() => doAction(srv, a)}
              className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-elevated text-secondary hover:text-primary hover:bg-elevated/80 transition"
              title={a.desc || a.label}>
              {a.icon}
              <span className="hidden sm:inline">{a.label}</span>
            </button>
          ))}
          <button onClick={() => showLogs(srv)}
            className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-elevated text-secondary hover:text-primary transition"
            title={t.serverLogs}>
            <IconLogs />
            <span className="hidden sm:inline">{t.serverLogs}</span>
          </button>
          <button onClick={() => showFiles(srv)}
            className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-elevated text-secondary hover:text-primary transition"
            title={t.serverFiles}>
            <IconFolder />
            <span className="hidden sm:inline">{t.serverFiles}</span>
          </button>
          <button onClick={() => setConfigServer(srv)}
            className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-elevated text-secondary hover:text-primary transition"
            title="Config Editor">
            <IconConfig />
            <span className="hidden sm:inline">Config</span>
          </button>
          {can('servers.write') && (
            <>
              <button onClick={() => openEdit(srv)}
                className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-accent/20 text-accent hover:bg-accent/30 transition"
                title={t.edit}>
                <IconEdit />
                <span className="hidden sm:inline">{t.edit}</span>
              </button>
              <button onClick={() => setConfirm(srv)}
                className="inline-flex items-center gap-1 text-xs px-2 py-1 rounded-lg bg-danger/20 text-danger hover:bg-danger/30 transition"
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
    const lifecycle = srv.lifecycle_state || 'online'
    const lifecycleBadge = (
      lifecycle === 'provisioning' ? <Badge variant="warning">провижн</Badge> :
      lifecycle === 'online' ? <Badge variant="success">online</Badge> :
      lifecycle === 'offline' ? <Badge variant="danger">offline</Badge> :
      lifecycle === 'deleted' ? <Badge variant="default">deleted</Badge> :
      <Badge variant="default">{lifecycle}</Badge>
    )

    return (
      <Tr>
        <Td>
          <div className="font-semibold text-sm cursor-pointer hover:text-accent transition" onClick={() => navigate(`/servers/${srv.id}`)}>{srv.name || ip}</div>
          {srv.location && <div className="text-xs text-secondary mt-0.5">{srv.location}</div>}
        </Td>
        <Td mono>
          <div className="flex items-center gap-1.5">
            <span className="text-accent">{ip}</span>
            {ip && <CopyButton text={ip} toast={toast} />}
          </div>
        </Td>
        <Td>{srv.country_code ? srv.country_code.toUpperCase() : '—'}</Td>
        <Td><StatusBadge status={srv.status} /></Td>
        <Td>{lifecycleBadge}</Td>
        <Td>
          <div className="flex items-center gap-1.5 flex-wrap">
            <TunnelBadge status={srv.tunnel_status} />
            {srv.is_exit_node && srv.mtproto_status && srv.mtproto_status !== 'unknown' && (
              <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] ${
                srv.mtproto_status === 'healthy' ? 'bg-success/15 text-success' :
                srv.mtproto_status === 'unhealthy' ? 'bg-danger/15 text-danger' :
                'bg-info/15 text-info'
              }`} title={`MTProto: ${srv.mtproto_status}`}>
                MTP
              </span>
            )}
            {srv.tunnel_protocol && (
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-accent/10 text-accent font-mono uppercase">{srv.tunnel_protocol}</span>
            )}
            {srv.tunnel_status === 'unhealthy' && srv.tunnel_error && (
              <span className="text-xs text-danger ml-1 truncate max-w-[200px]" title={srv.tunnel_error}>{srv.tunnel_error}</span>
            )}
          </div>
        </Td>
        <Td align="center" className="tabular-nums">{conns ?? '—'}</Td>
        <Td className="text-xs text-tertiary whitespace-nowrap">
          {healthTime ? formatRelativeTime(healthTime) : '—'}
        </Td>
        <Td align="right" className="tabular-nums">{srv.load_weight ?? '—'}</Td>
        <Td>
          <div className="flex items-center gap-1 flex-wrap">
            {can('servers.write') && actions.filter(a => !a.exitOnly || srv.is_exit_node).map(a => (
              <button key={a.key} onClick={() => doAction(srv, a)}
                aria-label={a.label}
                className="p-1 rounded hover:bg-elevated transition text-secondary hover:text-primary"
                title={a.desc || a.label}>{a.icon}</button>
            ))}
            <span className="w-px h-4 bg-default mx-0.5" />
            <button onClick={() => showLogs(srv)} aria-label={t.serverLogs} className="p-1 rounded hover:bg-elevated transition text-secondary hover:text-primary" title={t.serverLogs}><IconLogs /></button>
            <button onClick={() => showFiles(srv)} aria-label={t.serverFiles} className="p-1 rounded hover:bg-elevated transition text-secondary hover:text-primary" title={t.serverFiles}><IconFolder /></button>
            <button onClick={() => setConfigServer(srv)} aria-label="Config Editor" className="p-1 rounded hover:bg-elevated transition text-secondary hover:text-primary" title="Config Editor"><IconConfig /></button>
            <button onClick={() => toggleMetrics(srv.id)} aria-label={t.serverShowMetrics || 'Metrics'} className="p-1 rounded hover:bg-elevated transition text-secondary hover:text-primary" title={t.serverShowMetrics || 'Metrics'}><IconChart /></button>
            {can('servers.write') && (
              <>
                <button onClick={() => openEdit(srv)} aria-label={t.edit} className="p-1 rounded hover:bg-accent/20 transition text-accent" title={t.edit}><IconEdit /></button>
                <button onClick={() => setConfirm(srv)} aria-label={t.delete} className="p-1 rounded hover:bg-danger/20 transition text-danger" title={t.delete}><IconTrash /></button>
              </>
            )}
          </div>
        </Td>
      </Tr>
    )
  }

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.serversTitle + (total > 0 ? ` (${total})` : '')}
        subtitle={`${t.serverOnlineCount || 'Online'}: ${onlineCount} / ${total}`}
        actions={(
          <>
            <div className="flex items-center rounded-lg border border-default overflow-hidden">
              <button onClick={() => setViewMode('cards')}
                aria-label={t.serverViewCards || 'Cards'}
                className={`p-2 transition ${viewMode === 'cards' ? 'bg-accent text-white' : 'bg-elevated text-secondary hover:text-primary'}`}
                title={t.serverViewCards || 'Cards'}>
                <IconGrid />
              </button>
              <button onClick={() => setViewMode('list')}
                aria-label={t.serverViewList || 'List'}
                className={`p-2 transition ${viewMode === 'list' ? 'bg-accent text-white' : 'bg-elevated text-secondary hover:text-primary'}`}
                title={t.serverViewList || 'List'}>
                <IconList />
              </button>
            </div>
            {can('servers.write') && (
              <Button variant="primary" onClick={openCreate}>+ {t.createServer}</Button>
            )}
          </>
        )}
      />

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-2">
        <form onSubmit={onSearchSubmit} className="flex items-center gap-2 max-w-md flex-1">
          <Input
            type="text"
            placeholder={t.search}
            value={searchInput}
            onChange={e => setSearchInput(e.target.value)}
          />
          <Button type="submit" variant="secondary">{t.search}</Button>
        </form>
        <Select
          value={f.lifecycle_state}
          onChange={e => setF({ lifecycle_state: e.target.value, page: 1 })}
          aria-label="Lifecycle state"
        >
          {LIFECYCLE_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
        </Select>
        <Input
          type="text"
          placeholder="Страна (RU, NL, ...)"
          value={f.country}
          onChange={e => setF({ country: e.target.value.toUpperCase(), page: 1 })}
          className="w-40"
        />
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : filteredItems.length === 0 ? (
        <EmptyState
          icon={<Server className="w-12 h-12" />}
          title={t.noServers || 'Нет серверов'}
          description="Добавьте первый сервер через кнопку выше."
          action={can('servers.write') ? (
            <Button variant="primary" onClick={openCreate}>+ {t.createServer}</Button>
          ) : null}
        />
      ) : viewMode === 'cards' ? (
        /* ── Card grid ── */
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filteredItems.map(srv => <ServerCard key={srv.id} srv={srv} />)}
        </div>
      ) : (
        /* ── List table ── */
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="name" sortState={sortState} onSort={onSort}>{t.fieldServerName}</Th>
              <Th>IP</Th>
              <Th sortKey="country_code" sortState={sortState} onSort={onSort}>Country</Th>
              <Th sortKey="status" sortState={sortState} onSort={onSort}>{t.fieldStatus}</Th>
              <Th sortKey="lifecycle_state" sortState={sortState} onSort={onSort}>Lifecycle</Th>
              <Th>Tunnel</Th>
              <Th align="center">{t.serverActiveConns || 'Conn.'}</Th>
              <Th sortKey="last_check" sortState={sortState} onSort={onSort}>{t.serverLastHealth || 'Health'}</Th>
              <Th sortKey="load_weight" sortState={sortState} onSort={onSort}>Weight</Th>
              <Th>{t.serverActionsCol || 'Actions'}</Th>
            </Tr>
          </Thead>
          <Tbody>
            {filteredItems.map(srv => <ServerRow key={srv.id} srv={srv} />)}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
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
          <div className="bg-elevated rounded-xl border border-default w-full max-w-lg max-h-[70vh] m-4 flex flex-col" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-3 border-b border-default">
              <h3 className="font-semibold">{t.serverFilesTitle}</h3>
              <button onClick={() => setFilesModal(null)} className="text-secondary hover:text-primary">
                <IconClose />
              </button>
            </div>
            <div className="px-5 py-2 flex items-center gap-2 border-b border-white/5">
              {/* Breadcrumb: each segment is clickable to jump back */}
              <div className="flex-1 text-xs font-mono text-secondary flex flex-wrap items-center gap-1">
                {(() => {
                  const parts = filesModal.path.replace(/^\/+|\/+$/g, '').split('/')
                  return (
                    <>
                      <span
                        className="cursor-pointer hover:text-accent"
                        onClick={() => showFiles(filesModal.server, '/')}
                      >/</span>
                      {parts.map((seg, i) => {
                        const subPath = '/' + parts.slice(0, i + 1).join('/')
                        const isLast = i === parts.length - 1
                        return (
                          <span key={i}>
                            <span
                              className={isLast ? 'text-primary' : 'cursor-pointer hover:text-accent'}
                              onClick={isLast ? undefined : () => showFiles(filesModal.server, subPath)}
                            >{seg}</span>
                            {!isLast && <span className="mx-0.5">/</span>}
                          </span>
                        )
                      })}
                    </>
                  )
                })()}
              </div>
              {filesModal.path !== '/' && (
                <button
                  onClick={() => {
                    const parent = filesModal.path.replace(/\/+$/, '').split('/').slice(0, -1).join('/') || '/'
                    showFiles(filesModal.server, parent)
                  }}
                  className="text-xs px-2 py-1 rounded bg-white/5 hover:bg-white/10 text-secondary hover:text-primary"
                  title="Parent directory"
                >↑ ..</button>
              )}
            </div>
            <div className="flex-1 overflow-auto px-5 pb-4">
              {filesModal.entries.map((e, i) => (
                <div key={i}
                  className={`text-sm py-1 px-2 rounded cursor-pointer hover:bg-white/5 ${e.is_dir ? 'text-accent' : 'text-primary'}`}
                  onClick={() => e.is_dir && showFiles(filesModal.server, filesModal.path.replace(/\/+$/, '') + '/' + e.name)}
                >
                  {e.is_dir ? '\uD83D\uDCC1 ' : '\uD83D\uDCC4 '}{e.name}
                  {!e.is_dir && e.size != null && <span className="text-secondary text-xs ml-2">({formatSize(e.size)})</span>}
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
        <FormField label={t.fieldTags}><TextInput value={tags} onChange={setTags} placeholder="EU, premium, fast" /></FormField>
        <FormField label={t.fieldServerStatus}>
          <SelectInput value={serverStatus} onChange={setServerStatus} options={[
            { value: 'online', label: 'Онлайн' },
            { value: 'provisioning', label: 'Настройка' },
            { value: 'offline', label: 'Оффлайн' },
            { value: 'maintenance', label: 'Обслуживание' },
          ]} />
        </FormField>
        <div className="mt-3 mb-1 text-xs font-semibold text-secondary uppercase tracking-wider">Настройка туннеля</div>
        <FormField label="Протокол туннеля">
          <SelectInput value={tunnelProtocol} onChange={setTunnelProtocol} options={[
            { value: 'hysteria2', label: 'Hysteria2 (UDP, recommended)' },
            { value: 'vless', label: 'VLESS Reality' },
            { value: 'shadowsocks', label: 'Shadowsocks' },
            { value: 'socks5', label: 'SOCKS' },
            { value: 'http', label: 'HTTP Proxy' },
          ]} />
        </FormField>
        <FormField label="Порт туннеля"><TextInput value={tunnelPort} onChange={setTunnelPort} type="number" /></FormField>

        {(tunnelProtocol === 'vless') && (
          <>
            <FormField label="VLESS UUID"><TextInput value={tunnelUuid} onChange={setTunnelUuid} placeholder="Сгенерировано автоматически" /></FormField>
            <FormField label="Reality Public Key"><TextInput value={tunnelPublicKey} onChange={setTunnelPublicKey} placeholder="Сгенерировано автоматически" /></FormField>
            <FormField label="Reality Private Key"><TextInput value={tunnelPrivateKey} onChange={setTunnelPrivateKey} placeholder="Сгенерировано автоматически" /></FormField>
            <FormField label="Reality Short ID"><TextInput value={tunnelShortId} onChange={setTunnelShortId} placeholder="Сгенерировано автоматически" /></FormField>
            <FormField label="SNI"><TextInput value={tunnelSni} onChange={setTunnelSni} placeholder="например microsoft.com" /></FormField>
          </>
        )}

        {(tunnelProtocol === 'shadowsocks') && (
          <>
            <FormField label="SS Method"><TextInput value={tunnelSsMethod} onChange={setTunnelSsMethod} /></FormField>
            <FormField label="SS Password"><TextInput value={tunnelSsPassword} onChange={setTunnelSsPassword} placeholder="Сгенерировано автоматически" /></FormField>
          </>
        )}

        {(tunnelProtocol === 'socks5' || tunnelProtocol === 'http') && (
          <FormField label="Auth Password (опционально)"><TextInput value={tunnelUuid} onChange={setTunnelUuid} placeholder="Пароль для авторизации" /></FormField>
        )}
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={`Delete server "${confirm?.name}"?`}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />

      {/* Action confirm dialog */}
      <Dialog
        open={!!actionConfirm}
        onClose={() => setActionConfirm(null)}
        title={actionConfirm?.action?.label || ''}
        maxWidth="max-w-md"
      >
        {actionConfirm && (
          <>
            <p className="text-secondary text-sm mb-6">{actionConfirm.action.desc}<br/>Сервер: <b>{actionConfirm.server.name}</b></p>
            <div className="flex gap-3 justify-end">
              <Button variant="ghost" onClick={() => setActionConfirm(null)}>Отмена</Button>
              <Button variant="primary" onClick={() => { executeAction(actionConfirm.server, actionConfirm.action); setActionConfirm(null) }}>Выполнить</Button>
            </div>
          </>
        )}
      </Dialog>

      {/* Provision progress */}
      {progressModal && <ProvisionProgressDialog open={true} server={progressModal} onClose={() => { setProgressModal(null); paged.reload() }} />}

      {/* Config editor */}
      {configServer && <ConfigEditorDialog serverId={configServer.id} serverName={configServer.name || configServer.ip_address} onClose={() => setConfigServer(null)} />}
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

function parseLsListing(text) {
  const entries = []
  for (const line of (text || '').split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('total ')) continue
    const parts = trimmed.split(/\s+/)
    if (parts.length < 9) continue
    const perms = parts[0]
    const size = parseInt(parts[4]) || 0
    const name = parts.slice(8).join(' ')
    if (name === '.' || name === '..') continue
    entries.push({ name, is_dir: perms[0] === 'd', size, perms })
  }
  return entries
}


