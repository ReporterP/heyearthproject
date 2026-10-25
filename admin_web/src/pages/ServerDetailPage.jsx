import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchServerDetail, fetchServerLogs, runServerAction } from '../api/admin.js'
import StatusBadge from '../components/StatusBadge.jsx'
import TunnelCheckDialog from '../components/servers/TunnelCheckDialog.jsx'
import ServerLifecycleBadge from '../components/ServerLifecycleBadge.jsx'
import { isMonitored, pathView, tunnelView } from '../utils/serverState.js'
import { nodeHref } from '../components/nodes/nodeModel.js'
import {
  Badge,
  Button,
  Card,
  Dialog,
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

function PathRow({ name, status, rtt, error }) {
  const v = pathView(status)
  return (
    <div className="border border-default rounded p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-primary">{name}</span>
        <span className="flex items-center gap-2">
          {rtt > 0 && <span className="font-mono tabular-nums text-xs text-secondary">{rtt} мс</span>}
          <Badge variant={v.variant}>{v.label}</Badge>
        </span>
      </div>
      {error && <p className="mt-1 text-xs font-mono text-tertiary break-all">{error}</p>}
    </div>
  )
}

// The one node action left here, behind a confirmation that says what happens. Setting up,
// checking, re-installing, switching off and deleting a node are jobs of the node card (/nodes).
const ACTIONS = {
  restart_agent: { label: 'Перезапустить агент', confirm: 'Перезапустить node-agent на ноде? Трафик пользователей это не затрагивает.' },
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
  // Default logs service is chosen once after first server load:
  //   hub node          → heyearth-api (the API daemon)
  //   exit / other node → node-agent   (heyearth-api is not installed there)
  const [logsService, setLogsService] = useState(null)
  const [tunnelCheck, setTunnelCheck] = useState(false)
  const [confirmAction, setConfirmAction] = useState(null)

  // In-memory history buffer for charts
  const cpuHistory = useRef([])
  const ramHistory = useRef([])
  const connHistory = useRef([])

  const [historyVersion, setHistoryVersion] = useState(0)

  // Returns 'ok', 'gone' (404: the record no longer exists) or 'error'.
  const loadServer = useCallback(async () => {
    try {
      const data = await fetchServerDetail(id)
      setServer(data)
      setError(null)

      setLogsService(prev => prev ?? (data.is_exit_node ? 'node-agent' : 'heyearth-api'))

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
      return 'ok'
    } catch (err) {
      setError(err.message)
      return err.status === 404 ? 'gone' : 'error'
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

  async function doAction(key) {
    const a = ACTIONS[key]
    try {
      await runServerAction(id, key)
      toast.success(`${a.label}: выполнено`)
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
      <Button variant="secondary" onClick={() => navigate('/nodes')}>
        <ArrowLeft className="w-4 h-4" /> К списку нод
      </Button>
    </div>
  )

  const m = server?.load_metrics || {}
  const cpu = m.cpu_usage ?? m.cpu_percent
  const ram = m.memory_usage ?? m.memory_percent
  const conns = m.active_connections
  const uptime = m.uptime

  const tunnel = tunnelView(server) // null: not an online exit, the monitor does not look at it

  const mtprotoVariant = server.mtproto_status === 'healthy'
    ? 'success'
    : server.mtproto_status === 'unhealthy' ? 'danger'
    : server.mtproto_status === 'installing' ? 'info' : 'default'

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back link */}
      <Link to="/nodes" className="inline-flex items-center gap-1.5 text-sm text-tertiary hover:text-primary mb-4 transition-colors">
        <ArrowLeft className="w-4 h-4" />
        {t.navNodes || 'Nodes'}
      </Link>

      <PageHeader
        title={server.name || server.ip_address}
        subtitle={(
          <span className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="font-mono text-accent">{server.ip_address}</span>
            {server.country_code && <span>{server.country_code}</span>}
            <span>SSH :{server.ssh_port || 22}</span>
            {server.is_exit_node && <Badge variant="accent">выход за рубежом</Badge>}
          </span>
        )}
        actions={(
          <div className="flex items-center gap-2 flex-wrap">
            <Button variant="secondary" size="sm" onClick={() => navigate(nodeHref(id, server.role))}>Управление нодой</Button>
            <ServerLifecycleBadge state={server.lifecycle_state} />
            <StatusBadge status={server.status} />
            {server.is_exit_node && (
              <>
                {tunnel ? (
                  <Badge variant={tunnel.variant}>
                    <span className="w-2 h-2 rounded-full bg-current" />
                    туннель: {tunnel.label}
                  </Badge>
                ) : (
                  <Badge variant="default">туннель: —</Badge>
                )}
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
            {isMonitored(server) && server.tunnel_error && server.tunnel_status === 'unhealthy' && (
              <Badge variant="danger">{server.tunnel_error}</Badge>
            )}
          </div>
        )}
      />

      {/* A failed refresh (or «Сервер не найден» after a delete) must not hide behind stale data */}
      {error && <p className="text-sm text-danger mb-4">Не удалось обновить: {error}</p>}

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
        <Card className="p-4" title="Выходы с одинаковым приоритетом делят нагрузку; выход с меньшим приоритетом подхватывает её, если остальные недоступны или заметно медленнее">
          <div className="flex items-center gap-2 mb-2">
            <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 3v17.25m0 0c-1.472 0-2.882.265-4.185.75M12 20.25c1.472 0 2.882.265 4.185.75" /></svg>
            <span className="text-xs text-tertiary uppercase tracking-wider">Приоритет (1–10)</span>
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

      {/* Paths: the tunnel monitor's view, every 30 s */}
      {server.is_exit_node && (
        <Card className="p-4 mb-6">
          <div className="flex items-center justify-between gap-2 mb-3 flex-wrap">
            <h3 className="text-sm font-semibold text-primary">Пути до exit</h3>
            <Button size="sm" variant="secondary" onClick={() => setTunnelCheck(true)}>Проверить туннель</Button>
          </div>
          {!isMonitored(server) ? (
            <p className="text-xs text-tertiary">Пути проверяются только у нод в работе.</p>
          ) : server.paths ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-sm">
              <PathRow name="Hysteria (UDP)" status={server.paths.hy} rtt={server.paths.hy_rtt_ms} error={server.paths.hy_error} />
              <PathRow name="TCP (VLESS Reality)" status={server.paths.vr} rtt={server.paths.vr_rtt_ms} error={server.paths.vr_error} />
            </div>
          ) : (
            <p className="text-xs text-tertiary">Монитор ещё не проверял эту ноду после запуска API (проверка каждые 30 секунд).</p>
          )}
          <p className="text-[11px] text-tertiary mt-3">Exit отключается от трафика, только если не работают оба пути. Пока работает только TCP, Бамбук через этот exit не ходит.</p>
        </Card>
      )}

      {/* Actions */}
      <Card className="p-4 mb-6">
        <h3 className="text-sm font-semibold text-primary mb-3">Действия</h3>
        <div className="flex flex-wrap gap-2">
          {can('server_ops.execute') && Object.entries(ACTIONS).map(([key, a]) => (
            <Button key={key} size="sm" variant="secondary" onClick={() => setConfirmAction(key)}>{a.label}</Button>
          ))}
          <Button size="sm" variant="ghost" onClick={loadServer}>Обновить</Button>
        </div>
        <p className="text-[11px] text-tertiary mt-2">
          Настройка, проверка, переустановка, отключение и удаление ноды — в карточке ноды («Управление нодой»). Страница обновляется каждые {REFRESH_INTERVAL / 1000} с.
        </p>
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

      {confirmAction && (
        <Dialog open onClose={() => setConfirmAction(null)} title={ACTIONS[confirmAction].label} maxWidth="max-w-md">
          <p className="text-sm text-secondary mb-6">{ACTIONS[confirmAction].confirm}</p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmAction(null)}>Отмена</Button>
            <Button variant="primary"
              onClick={() => { const k = confirmAction; setConfirmAction(null); doAction(k) }}>Выполнить</Button>
          </div>
        </Dialog>
      )}

      {tunnelCheck && <TunnelCheckDialog server={server} onClose={() => setTunnelCheck(false)} />}
    </div>
  )
}
