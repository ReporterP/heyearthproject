import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchServerDetail, fetchServerLogs, runServerAction, fetchHysteria2Status } from '../api/admin.js'
import StatusBadge from '../components/StatusBadge.jsx'
import ConfigEditorDialog from '../components/ConfigEditorDialog.jsx'
import DeleteServerDialog from '../components/DeleteServerDialog.jsx'
import {
  Badge,
  Button,
  Card,
  PageHeader,
  Select,
  Spinner,
} from '../components/ui/index.js'

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
        <span className="text-[10px] text-tertiary">{label}</span>
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
    <Card className="p-4">
      <div className="flex items-center gap-2 mb-2">
        {icon}
        <span className="text-xs text-tertiary uppercase tracking-wider">{label}</span>
      </div>
      <div className="text-2xl font-semibold font-mono tabular-nums" style={{ color }}>
        {value != null ? `${Number(value).toFixed(1)}${unit}` : '—'}
      </div>
      {unit === '%' && (
        <div className="mt-2 h-1.5 rounded-full bg-elevated overflow-hidden border border-default/60">
          <div className="h-full rounded-full transition-all duration-500" style={{ width: `${pct}%`, backgroundColor: color }} />
        </div>
      )}
    </Card>
  )
}

export default function ServerDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [server, setServer] = useState(null)
  const [hysteria2, setHysteria2] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [logs, setLogs] = useState('')
  // Default logs service is chosen once after first server load:
  //   hub node          → heyearth-api (the API daemon)
  //   exit / other node → node-agent   (heyearth-api is not installed there)
  const [logsService, setLogsService] = useState(null)
  const [configOpen, setConfigOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  // In-memory history buffer for charts
  const cpuHistory = useRef([])
  const ramHistory = useRef([])
  const connHistory = useRef([])

  const [historyVersion, setHistoryVersion] = useState(0)

  const loadServer = useCallback(async () => {
    try {
      const data = await fetchServerDetail(id)
      setServer(data)
      setError(null)

      setLogsService(prev => prev ?? (data.is_exit_node ? 'node-agent' : 'heyearth-api'))

      // Fetch Hysteria2 (Express) snapshot for the Hub. Exit nodes don't run
      // a client-facing Hysteria2 server, so we skip the request there.
      if (!data.is_exit_node) {
        fetchHysteria2Status().then(setHysteria2).catch(() => setHysteria2(null))
      }

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

  useEffect(() => {
    loadServer()
    const interval = setInterval(() => {
      if (document.visibilityState === 'visible') loadServer()
    }, REFRESH_INTERVAL)
    return () => clearInterval(interval)
  }, [loadServer])

  async function loadLogs() {
    if (!logsService) return
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
      setTimeout(loadServer, 3000)
    } catch (err) {
      toast.error(err.message)
    }
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <Spinner size="md" />
    </div>
  )

  if (error && !server) return (
    <div className="text-center py-32">
      <p className="text-danger mb-4">{error}</p>
      <Button variant="secondary" onClick={() => navigate('/servers')}>
        <ArrowLeft className="w-4 h-4" /> Back to Servers
      </Button>
    </div>
  )

  const m = server?.load_metrics || {}
  const cpu = m.cpu_usage ?? m.cpu_percent
  const ram = m.memory_usage ?? m.memory_percent
  const conns = m.active_connections
  const uptime = m.uptime

  const tunnelVariant = server.tunnel_status === 'healthy'
    ? 'success'
    : server.tunnel_status === 'unhealthy' ? 'danger' : 'warning'

  const mtprotoVariant = server.mtproto_status === 'healthy'
    ? 'success'
    : server.mtproto_status === 'unhealthy' ? 'danger'
    : server.mtproto_status === 'installing' ? 'info' : 'default'

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back link */}
      <Link to="/servers" className="inline-flex items-center gap-1.5 text-sm text-tertiary hover:text-primary mb-4 transition-colors">
        <ArrowLeft className="w-4 h-4" />
        {t.serversTitle || 'Servers'}
      </Link>

      <PageHeader
        title={server.name || server.ip_address}
        subtitle={(
          <span className="inline-flex items-center gap-3">
            <span className="font-mono text-accent">{server.ip_address}</span>
            {server.country_code && <span>{server.country_code}</span>}
            <span>SSH :{server.ssh_port || 22}</span>
            {server.is_exit_node && <Badge variant="accent">exit node</Badge>}
          </span>
        )}
        actions={(
          <div className="flex items-center gap-2 flex-wrap">
            <StatusBadge status={server.status} />
            {server.is_exit_node && (
              <>
                <Badge variant={tunnelVariant}>
                  <span className="w-2 h-2 rounded-full bg-current" />
                  {server.tunnel_status}
                </Badge>
                <Badge variant={mtprotoVariant} className="ml-1">
                  <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
                  MTP: {server.mtproto_status === 'healthy' ? 'OK' :
                        server.mtproto_status === 'unhealthy' ? 'Down' :
                        server.mtproto_status === 'installing' ? 'Install…' : '—'}
                </Badge>
                {server.tunnel_protocol && (
                  <Badge variant="accent" className="font-mono uppercase">
                    {server.tunnel_protocol}:{server.tunnel_port}
                  </Badge>
                )}
              </>
            )}
            {server.tunnel_error && server.tunnel_status === 'unhealthy' && (
              <Badge variant="danger">{server.tunnel_error}</Badge>
            )}
          </div>
        )}
      />

      {uptime && (
        <p className="text-xs text-tertiary -mt-3 mb-4">
          Uptime: {typeof uptime === 'number' ? `${Math.floor(uptime / 3600)}h ${Math.floor((uptime % 3600) / 60)}m` : uptime}
        </p>
      )}

      {/* Metrics gauges */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <GaugeCard label="CPU" value={cpu} color="#6366f1" icon={<svg className="w-4 h-4 text-info" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25z" /></svg>} />
        <GaugeCard label="RAM" value={ram} color="#22d3ee" icon={<svg className="w-4 h-4 text-info" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6z" /></svg>} />
        <GaugeCard label="Connections" value={conns} unit="" color="#f59e0b" icon={<svg className="w-4 h-4 text-warning" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" /></svg>} />
        <Card className="p-4">
          <div className="flex items-center gap-2 mb-2">
            <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 3v17.25m0 0c-1.472 0-2.882.265-4.185.75M12 20.25c1.472 0 2.882.265 4.185.75" /></svg>
            <span className="text-xs text-tertiary uppercase tracking-wider">Weight (1-10)</span>
          </div>
          <div className="text-2xl font-semibold font-mono tabular-nums text-accent">
            {server.load_weight && server.load_weight > 0 ? Math.round(server.load_weight) : '—'}
          </div>
        </Card>
      </div>

      {/* Mini charts (in-memory history) */}
      {(cpuHistory.current.length > 1 || ramHistory.current.length > 1) && (
        <Card className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6 p-4" key={historyVersion}>
          <MiniBarChart data={cpuHistory.current} color="#6366f1" label="CPU History" />
          <MiniBarChart data={ramHistory.current} color="#22d3ee" label="RAM History" />
          <MiniBarChart data={connHistory.current} color="#f59e0b" label="Connections" unit="" max={Math.max(10, ...connHistory.current)} />
        </Card>
      )}

      {/* Hysteria2 (Express) — Hub only */}
      {!server.is_exit_node && hysteria2 && (
        <Card className="p-4 mb-6">
          <div className="flex items-center justify-between mb-3 gap-2 flex-wrap">
            <h3 className="text-sm font-semibold text-primary">Express (Hysteria2)</h3>
            <div className="flex items-center gap-2">
              {hysteria2.binary_installed && hysteria2.config_present && hysteria2.active_plans > 0 && !hysteria2.last_error ? (
                <Badge variant="success">
                  <span className="w-2 h-2 rounded-full bg-current" /> healthy
                </Badge>
              ) : hysteria2.active_plans === 0 ? (
                <Badge variant="default">disabled</Badge>
              ) : (
                <Badge variant="warning">
                  <span className="w-2 h-2 rounded-full bg-current" /> degraded
                </Badge>
              )}
              <Badge variant="default" className="font-mono">v{hysteria2.config_version}</Badge>
            </div>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3 text-sm">
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Binary</div>
              <div className={hysteria2.binary_installed ? 'text-success font-mono' : 'text-danger font-mono'}>
                {hysteria2.binary_installed ? 'installed' : 'missing'}
              </div>
            </div>
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Config</div>
              <div className={hysteria2.config_present ? 'text-success font-mono' : 'text-tertiary font-mono'}>
                {hysteria2.config_present ? 'present' : 'absent'}
              </div>
            </div>
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Active plans</div>
              <div className="font-mono tabular-nums text-primary">{hysteria2.active_plans}</div>
            </div>
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Up cap</div>
              <div className="font-mono tabular-nums text-primary">{hysteria2.max_up_mbps} Mbps</div>
            </div>
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Down cap</div>
              <div className="font-mono tabular-nums text-primary">{hysteria2.max_down_mbps} Mbps</div>
            </div>
            <div>
              <div className="text-[10px] text-tertiary uppercase tracking-wider mb-1">Last sync</div>
              <div className="font-mono tabular-nums text-primary">
                {hysteria2.last_sync_at > 0
                  ? new Date(hysteria2.last_sync_at * 1000).toLocaleTimeString('ru-RU')
                  : '—'}
              </div>
            </div>
          </div>
          {hysteria2.last_error && (
            <p className="mt-3 text-xs text-danger font-mono break-all">
              {hysteria2.last_error}
            </p>
          )}
          <p className="mt-3 text-[10px] text-tertiary">
            UDP 50000-50100 (port-hopping) → :50443 · Brutal congestion · Salamander obfuscation
          </p>
        </Card>
      )}

      {/* Actions */}
      <Card className="p-4 mb-6">
        <h3 className="text-sm font-semibold text-primary mb-3">{t.sdActions || 'Actions'}</h3>
        <div className="flex flex-wrap gap-2">
          {can('servers.write') && (
            <>
              <Button size="sm" variant="secondary" onClick={() => doAction('sync_metrics')}>{t.sdSyncMetrics || 'Sync Metrics'}</Button>
              <Button size="sm" variant="secondary" onClick={() => doAction('restart_agent')}>{t.sdRestartAgent || 'Restart Agent'}</Button>
              <Button size="sm" variant="secondary" onClick={() => doAction('sync_tunnel')}>{t.sdSyncTunnel || 'Sync Tunnel'}</Button>
              {server.is_exit_node && (
                <>
                  <Button size="sm" variant="secondary" onClick={() => doAction('fetch_tunnel_creds')}>{t.sdFetchCreds || 'Fetch Creds'}</Button>
                  <Button size="sm" variant="danger" onClick={() => { if (confirm(t.sdReprovisionConfirm || 'Re-provision this node? This will reinstall all services.')) doAction('setup_exit_node') }}>
                    {t.sdReprovision || 'Re-provision'}
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => { if (confirm('Переустановить mtproto-proxy на этой ноде?')) doAction('reinstall_mtproxy') }}>
                    Reinstall MTProxy
                  </Button>
                  <Button size="sm" variant="primary" onClick={() => doAction('hub_provision')}>{t.sdHubProvision || 'Hub Provision'}</Button>
                  <Button size="sm" variant="secondary" onClick={() => { if (confirm('Soft-disable: stop hub-side tunnel + archive + keep DB row?')) doAction('deprovision_soft') }}>
                    {t.sdDisable || 'Disable (soft)'}
                  </Button>
                  <Button size="sm" variant="danger" onClick={() => setConfirmDelete(true)}>{t.sdDeleteHard || 'Delete (hard)'}</Button>
                </>
              )}
            </>
          )}
          <Button size="sm" variant="ghost" onClick={() => setConfigOpen(true)}>{t.sdConfigEditor || 'Config Editor'}</Button>
          <Button size="sm" variant="ghost" onClick={loadServer}>{t.sdRefreshNow || 'Refresh Now'}</Button>
        </div>
        <p className="text-[10px] text-tertiary mt-2">{(t.sdAutoRefreshEvery || 'Auto-refreshes every {s}s').replace('{s}', REFRESH_INTERVAL / 1000)}</p>
      </Card>

      {/* Logs */}
      <Card className="p-4 mb-6">
        <div className="flex items-center justify-between mb-3 gap-2">
          <h3 className="text-sm font-semibold text-primary">{t.sdLogs || 'Logs'}</h3>
          <div className="flex items-center gap-2">
            <Select value={logsService || ''} onChange={e => setLogsService(e.target.value)} className="w-40">
              <option value="heyearth-api">heyearth-api</option>
              <option value="xray">xray</option>
              <option value="hysteria2">hysteria2</option>
              <option value="nginx">nginx</option>
              <option value="node-agent">node-agent</option>
            </Select>
            <Button size="sm" variant="ghost" onClick={loadLogs}>{t.sdRefresh || 'Refresh'}</Button>
          </div>
        </div>
        <pre className="text-[11px] font-mono text-secondary bg-canvas rounded p-3 max-h-[300px] overflow-auto whitespace-pre-wrap border border-default/60">
          {logs || (t.sdLoading || 'Loading...')}
        </pre>
      </Card>

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
