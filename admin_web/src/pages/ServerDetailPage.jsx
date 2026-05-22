import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchServerDetail, fetchServerLogs, runServerAction } from '../api/admin.js'
import StatusBadge from '../components/StatusBadge.jsx'
import ConfigEditorDialog from '../components/ConfigEditorDialog.jsx'
import ServerLifecycleBadge from '../components/ServerLifecycleBadge.jsx'
import DeleteServerDialog from '../components/DeleteServerDialog.jsx'

const REFRESH_INTERVAL = 15000 // 15s
const MAX_HISTORY = 20

function MiniBarChart({ data, color = '#6366f1', label, unit = '%', max = 100 }) {
  if (!data || data.length === 0) return null
  return (
    <div>
      <div className="flex items-end gap-[2px] h-12">
        {data.map((v, i) => (
          <div key={i} className="flex-1 rounded-t" style={{
            height: `${Math.max(2, (v / max) * 100)}%`,
            backgroundColor: color,
            opacity: 0.3 + 0.7 * (i / data.length),
          }} title={`${v.toFixed(1)}${unit}`} />
        ))}
      </div>
      <div className="flex items-center justify-between mt-1">
        <span className="text-[10px] text-text-secondary">{label}</span>
        <span className="text-xs font-mono font-semibold" style={{ color }}>
          {data[data.length - 1]?.toFixed(1)}{unit}
        </span>
      </div>
    </div>
  )
}

function GaugeCard({ label, value, unit = '%', color = '#6366f1', icon }) {
  const pct = Math.min(100, Math.max(0, value || 0))
  return (
    <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
      <div className="flex items-center gap-2 mb-2">
        {icon}
        <span className="text-xs text-text-secondary uppercase tracking-wider">{label}</span>
      </div>
      <div className="text-2xl font-bold font-mono" style={{ color }}>
        {value != null ? `${Number(value).toFixed(1)}${unit}` : '—'}
      </div>
      {unit === '%' && (
        <div className="mt-2 h-1.5 rounded-full bg-white/5 overflow-hidden">
          <div className="h-full rounded-full transition-all duration-500" style={{ width: `${pct}%`, backgroundColor: color }} />
        </div>
      )}
    </div>
  )
}

export default function ServerDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [server, setServer] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [logs, setLogs] = useState('')
  // Default logs service is chosen once after the first server load:
  //   hub node          → heyearth-api (the API daemon)
  //   exit / other node → node-agent   (heyearth-api is not installed there)
  // Stays null until we know what kind of node this is.
  const [logsService, setLogsService] = useState(null)
  const [configOpen, setConfigOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  // In-memory history buffer for charts
  const cpuHistory = useRef([])
  const ramHistory = useRef([])
  const connHistory = useRef([])

  const [historyVersion, setHistoryVersion] = useState(0) // force re-render on history update

  const loadServer = useCallback(async () => {
    try {
      const data = await fetchServerDetail(id)
      setServer(data)
      setError(null)

      // Pick a sensible default log service on first load.
      setLogsService(prev => prev ?? (data.is_exit_node ? 'node-agent' : 'heyearth-api'))

      // Push to history
      const m = data.load_metrics || {}
      const cpu = m.cpu_usage ?? m.cpu_percent
      const ram = m.memory_usage ?? m.memory_percent
      const conns = m.active_connections

      if (cpu != null) {
        cpuHistory.current = [...cpuHistory.current.slice(-(MAX_HISTORY - 1)), Number(cpu)]
      }
      if (ram != null) {
        ramHistory.current = [...ramHistory.current.slice(-(MAX_HISTORY - 1)), Number(ram)]
      }
      if (conns != null) {
        connHistory.current = [...connHistory.current.slice(-(MAX_HISTORY - 1)), Number(conns)]
      }
      setHistoryVersion(v => v + 1)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [id])

  // Auto-refresh (pauses when tab hidden)
  useEffect(() => {
    loadServer()
    const interval = setInterval(() => {
      if (document.visibilityState === 'visible') loadServer()
    }, REFRESH_INTERVAL)
    return () => clearInterval(interval)
  }, [loadServer])

  async function loadLogs() {
    if (!logsService) return // service not chosen yet
    try {
      const text = await fetchServerLogs(id, { service: logsService, tail: 100 })
      setLogs(text)
    } catch (err) {
      toast.error(err.message)
    }
  }

  useEffect(() => { if (server && logsService) loadLogs() }, [logsService])

  async function doAction(actionKey) {
    try {
      await runServerAction(id, actionKey)
      toast.success(`Action ${actionKey} started`)
      setTimeout(loadServer, 3000) // refresh after action
    } catch (err) {
      toast.error(err.message)
    }
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-8 h-8 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
    </div>
  )

  if (error && !server) return (
    <div className="text-center py-32">
      <p className="text-red-400 mb-4">{error}</p>
      <button onClick={() => navigate('/servers')} className="px-4 py-2 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary">
        &larr; Back to Servers
      </button>
    </div>
  )

  const m = server?.load_metrics || {}
  const cpu = m.cpu_usage ?? m.cpu_percent
  const ram = m.memory_usage ?? m.memory_percent
  const conns = m.active_connections
  const uptime = m.uptime

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back link */}
      <Link to="/servers" className="inline-flex items-center gap-1.5 text-sm text-text-secondary hover:text-text-primary mb-4 transition">
        <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" /></svg>
        {t.serversTitle || 'Servers'}
      </Link>

      {/* Header */}
      <div className="bg-surface-card rounded-xl border border-white/10 p-6 mb-6">
        <div className="flex items-start justify-between flex-wrap gap-4">
          <div>
            <div className="flex items-center gap-3 mb-2">
              <h1 className="text-2xl font-bold">{server.name || server.ip_address}</h1>
              <StatusBadge status={server.status} />
            </div>
            <div className="flex items-center gap-4 text-sm text-text-secondary">
              <span className="font-mono text-brand">{server.ip_address}</span>
              {server.country_code && <span>{server.country_code}</span>}
              <span>SSH :{server.ssh_port || 22}</span>
              {server.is_exit_node && (
                <span className="px-2 py-0.5 rounded bg-accent/10 text-accent text-xs">exit node</span>
              )}
            </div>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            {server.is_exit_node && (
              <div className="flex items-center gap-2">
                <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium ${
                  server.tunnel_status === 'healthy' ? 'bg-green-500/10 text-green-400' :
                  server.tunnel_status === 'unhealthy' ? 'bg-red-500/10 text-red-400' :
                  'bg-yellow-500/10 text-yellow-400'
                }`}>
                  <span className={`w-2 h-2 rounded-full ${
                    server.tunnel_status === 'healthy' ? 'bg-green-400' :
                    server.tunnel_status === 'unhealthy' ? 'bg-red-400' : 'bg-yellow-400'
                  }`} />
                  {server.tunnel_status}
                </span>
                {server.tunnel_protocol && (
                  <span className="text-xs px-2 py-1 rounded bg-brand/10 text-brand font-mono uppercase">{server.tunnel_protocol}:{server.tunnel_port}</span>
                )}
              </div>
            )}
            {server.tunnel_error && server.tunnel_status === 'unhealthy' && (
              <span className="text-xs text-red-400 bg-red-500/10 px-2 py-1 rounded">{server.tunnel_error}</span>
            )}
          </div>
        </div>
        {uptime && (
          <p className="text-xs text-text-secondary mt-3">Uptime: {typeof uptime === 'number' ? `${Math.floor(uptime / 3600)}h ${Math.floor((uptime % 3600) / 60)}m` : uptime}</p>
        )}
      </div>

      {/* Metrics gauges + charts */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <GaugeCard label="CPU" value={cpu} color="#6366f1" icon={<svg className="w-4 h-4 text-indigo-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25z" /></svg>} />
        <GaugeCard label="RAM" value={ram} color="#22d3ee" icon={<svg className="w-4 h-4 text-cyan-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6z" /></svg>} />
        <GaugeCard label="Connections" value={conns} unit="" color="#f59e0b" icon={<svg className="w-4 h-4 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" /></svg>} />
        {/* Weight is an integer 1..10 — render without decimals, show — if unset */}
        <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
          <div className="flex items-center gap-2 mb-2">
            <svg className="w-4 h-4 text-purple-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 3v17.25m0 0c-1.472 0-2.882.265-4.185.75M12 20.25c1.472 0 2.882.265 4.185.75" /></svg>
            <span className="text-xs text-text-secondary uppercase tracking-wider">Weight (1-10)</span>
          </div>
          <div className="text-2xl font-bold font-mono" style={{ color: '#a855f7' }}>
            {server.load_weight && server.load_weight > 0 ? Math.round(server.load_weight) : '—'}
          </div>
        </div>
      </div>

      {/* Mini charts (in-memory history) */}
      {(cpuHistory.current.length > 1 || ramHistory.current.length > 1) && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6 bg-surface-card rounded-xl border border-white/10 p-4" key={historyVersion}>
          <MiniBarChart data={cpuHistory.current} color="#6366f1" label="CPU History" />
          <MiniBarChart data={ramHistory.current} color="#22d3ee" label="RAM History" />
          <MiniBarChart data={connHistory.current} color="#f59e0b" label="Connections" unit="" max={Math.max(10, ...connHistory.current)} />
        </div>
      )}

      {/* Actions */}
      <div className="bg-surface-card rounded-xl border border-white/10 p-4 mb-6">
        <h3 className="text-sm font-semibold mb-3">{t.sdActions || 'Actions'}</h3>
        <div className="flex flex-wrap gap-2">
          {can('servers.write') && (
            <>
              <button onClick={() => doAction('sync_metrics')} className="px-3 py-1.5 text-xs rounded-lg bg-brand/20 text-brand hover:bg-brand/30 transition">{t.sdSyncMetrics || 'Sync Metrics'}</button>
              <button onClick={() => doAction('restart_agent')} className="px-3 py-1.5 text-xs rounded-lg bg-amber-500/20 text-amber-400 hover:bg-amber-500/30 transition">{t.sdRestartAgent || 'Restart Agent'}</button>
              <button onClick={() => doAction('sync_tunnel')} className="px-3 py-1.5 text-xs rounded-lg bg-cyan-500/20 text-cyan-400 hover:bg-cyan-500/30 transition">{t.sdSyncTunnel || 'Sync Tunnel'}</button>
              {server.is_exit_node && (
                <>
                  <button onClick={() => doAction('fetch_tunnel_creds')} className="px-3 py-1.5 text-xs rounded-lg bg-purple-500/20 text-purple-400 hover:bg-purple-500/30 transition">{t.sdFetchCreds || 'Fetch Creds'}</button>
                  <button onClick={() => { if (confirm(t.sdReprovisionConfirm || 'Re-provision this node? This will reinstall all services.')) doAction('setup_exit_node') }}
                    className="px-3 py-1.5 text-xs rounded-lg bg-red-500/20 text-red-400 hover:bg-red-500/30 transition">{t.sdReprovision || 'Re-provision'}</button>
                  <button onClick={() => doAction('hub_provision')}
                    className="px-3 py-1.5 text-xs rounded-lg bg-emerald-500/20 text-emerald-400 hover:bg-emerald-500/30 transition">{t.sdHubProvision || 'Hub Provision'}</button>
                  <button onClick={() => { if (confirm('Soft-disable: stop hub-side tunnel + archive + keep DB row?')) doAction('deprovision_soft') }}
                    className="px-3 py-1.5 text-xs rounded-lg bg-amber-500/20 text-amber-400 hover:bg-amber-500/30 transition">{t.sdDisable || 'Disable (soft)'}</button>
                  <button onClick={() => setConfirmDelete(true)}
                    className="px-3 py-1.5 text-xs rounded-lg bg-red-600/20 text-red-300 hover:bg-red-600/30 transition border border-red-500/30">{t.sdDeleteHard || 'Delete (hard)'}</button>
                </>
              )}
            </>
          )}
          <button onClick={() => setConfigOpen(true)} className="px-3 py-1.5 text-xs rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary transition">{t.sdConfigEditor || 'Config Editor'}</button>
          <button onClick={loadServer} className="px-3 py-1.5 text-xs rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary transition">{t.sdRefreshNow || 'Refresh Now'}</button>
        </div>
        <p className="text-[10px] text-text-secondary mt-2">{(t.sdAutoRefreshEvery || 'Auto-refreshes every {s}s').replace('{s}', REFRESH_INTERVAL / 1000)}</p>
      </div>

      {/* Logs */}
      <div className="bg-surface-card rounded-xl border border-white/10 p-4 mb-6">
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold">{t.sdLogs || 'Logs'}</h3>
          <div className="flex items-center gap-2">
            <select value={logsService} onChange={e => setLogsService(e.target.value)}
              className="px-2 py-1 text-xs rounded bg-surface-alt border border-white/10 text-text-primary">
              <option value="heyearth-api">heyearth-api</option>
              <option value="xray">xray</option>
              <option value="hysteria2">hysteria2</option>
              <option value="nginx">nginx</option>
              <option value="node-agent">node-agent</option>
            </select>
            <button onClick={loadLogs} className="px-2 py-1 text-xs rounded bg-surface-alt text-text-secondary hover:text-text-primary transition">{t.sdRefresh || 'Refresh'}</button>
          </div>
        </div>
        <pre className="text-[11px] font-mono text-text-secondary bg-surface-bg rounded-lg p-3 max-h-[300px] overflow-auto whitespace-pre-wrap">
          {logs || (t.sdLoading || 'Loading...')}
        </pre>
      </div>

      {/* Config Editor */}
      {configOpen && <ConfigEditorDialog serverId={server.id} serverName={server.name || server.ip_address} onClose={() => setConfigOpen(false)} />}

      {/* Hard delete confirmation */}
      {confirmDelete && (
        <DeleteServerDialog
          serverId={server.id}
          serverName={server.name || server.ip_address}
          onConfirm={async () => {
            setConfirmDelete(false)
            try {
              const adminToken = localStorage.getItem('admin_token') || ''
              const res = await fetch(`/api/admin/servers/${server.id}`, {
                method: 'DELETE',
                headers: { Authorization: `Bearer ${adminToken}` },
              })
              if (!res.ok && res.status !== 202) {
                throw new Error(`HTTP ${res.status}`)
              }
              toast.success('Deprovisioning started — watch progress')
            } catch (e) {
              toast.error(`Delete failed: ${e.message}`)
            }
          }}
          onCancel={() => setConfirmDelete(false)}
        />
      )}
    </div>
  )
}
