import { useState, useEffect, useCallback } from 'react'
import { Zap } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchAutoTunerStatus, fetchAutoTunerHistory, toggleAutoTuner, setAutoTunerInterval, runAutoTunerNow, cleanupAutoTunerHistory } from '../api/admin.js'
import BambooCard from '../components/protocols/BambooCard.jsx'
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Dialog,
  EmptyState,
  PageHeader,
  Select,
  Spinner,
  Table,
  Tbody,
  Td,
  Th,
  Thead,
  Tr,
} from '../components/ui/index.js'

const PAGE_SUBTITLE = 'Бамбук и автотюнер. Параметры маскировки, от которых зависят профили пользователей, меняются только с предупреждением.'

export default function AutoTunerPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const [status, setStatus] = useState(null)
  const [history, setHistory] = useState([])
  const [historyTotal, setHistoryTotal] = useState(0)
  const [historyPage, setHistoryPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [enableDialog, setEnableDialog] = useState(false)
  const [understood, setUnderstood] = useState(false)
  const canWrite = can('settings.write')

  const loadData = useCallback(() => {
    Promise.all([
      fetchAutoTunerStatus().catch(() => null),
      fetchAutoTunerHistory(historyPage, 30).catch(() => ({ items: [], total: 0 })),
    ]).then(([st, hist]) => {
      setStatus(st)
      setHistory(hist?.items || [])
      setHistoryTotal(hist?.total || 0)
    }).finally(() => setLoading(false))
  }, [historyPage])

  useEffect(() => {
    loadData()
    const interval = setInterval(() => {
      if (document.visibilityState === 'visible') loadData()
    }, 15000)
    return () => clearInterval(interval)
  }, [loadData])

  async function setEnabled(newVal) {
    try {
      const res = await toggleAutoTuner(newVal)
      const running = res?.loop_running ?? status?.loop_running
      setStatus(prev => ({ ...prev, enabled: newVal, loop_running: running }))
      const msg = res?.message || (newVal ? 'Автотюнер включён' : 'Автотюнер выключен')
      // Enabled but no tuner goroutine in this API process: it is not working yet, say so.
      if (newVal && !running) toast.warn(msg)
      else toast.success(msg)
    } catch (e) {
      toast.error(e.message)
    }
  }

  // Turning it off is safe; turning it on changes what users' profiles depend on: ask first.
  function handleToggle() {
    if (status?.enabled) { setEnabled(false); return }
    setUnderstood(false)
    setEnableDialog(true)
  }

  function runNow() {
    runAutoTunerNow()
      .then(r => { toast.success(r?.message || 'Подбор параметров запущен'); setTimeout(loadData, 2000) })
      .catch(e => toast.error(e.message))
  }

  // Why «Запустить сейчас» is off ('' = it is on). The API answers 409 in the same cases.
  const runNowBlocked = !canWrite ? 'Нужно право settings.write'
    : !status ? 'Статус автотюнера не загрузился'
    : !status.loop_running ? 'Тюнер не запущен в API: при старте API он был выключен'
    : !status.enabled ? 'Сначала включите автотюнер'
    : ''

  if (loading) return (
    <div>
      <PageHeader title="Протоколы и маскировка" subtitle={PAGE_SUBTITLE} />
      <div className="flex items-center justify-center py-32">
        <Spinner size="md" />
      </div>
    </div>
  )

  const appliedResults = history.filter(r => r.was_applied)
  const hubResults = history.filter(r => r.target_type === 'hub_inbound')
  const exitResults = history.filter(r => r.target_type?.startsWith('exit_'))

  const latencyClass = (ms) => ms > 500 ? 'text-danger font-semibold' : ms > 200 ? 'text-warning' : 'text-success font-semibold'

  const actions = (
    <>
      <Button
        variant="secondary"
        size="sm"
        icon={<Zap size={14} />}
        disabled={!!runNowBlocked}
        title={runNowBlocked || 'Запустить подбор параметров сейчас'}
        onClick={runNow}
      >Запустить сейчас</Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={!canWrite}
        onClick={() => { cleanupAutoTunerHistory(7).then(d => { toast.success(`Удалено: ${d.deleted}`); loadData() }).catch(e => toast.error(e.message)) }}
        title="Очистить историю старше 7 дней"
      >Очистить</Button>
      <Button variant="ghost" size="sm" onClick={loadData} aria-label="Обновить">↻</Button>
      <Button
        variant={status?.enabled ? 'secondary' : 'danger'}
        size="sm"
        disabled={!status || !canWrite}
        title={!canWrite ? 'Нужно право settings.write' : status?.enabled ? 'Выключить автотюнер' : 'Включить автотюнер (с предупреждением)'}
        onClick={handleToggle}
      >{status?.enabled ? '● Автотюнер включён' : '○ Автотюнер выключен'}</Button>
    </>
  )

  return (
    <div>
      <PageHeader
        title="Протоколы и маскировка"
        subtitle={PAGE_SUBTITLE}
        actions={actions}
      />

      {/* GET /hysteria2/status needs servers.read; the page itself needs settings.read. */}
      {can('servers.read') && <BambooCard />}

      {status && status.enabled && !status.loop_running && (
        <div role="status" className="mb-6 rounded border border-warning/30 bg-warning/10 px-3 py-2 text-sm text-warning">
          Автотюнер включён в настройках, но в работающем API не запущен (при старте API он был выключен).
          Он начнёт работать после следующего перезапуска API.
        </div>
      )}

      {/* Status cards */}
      {status && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
          <Card>
            <CardBody>
              <p className="text-[10px] uppercase tracking-wider text-tertiary mb-1">Статус</p>
              <p className={`text-lg font-semibold ${status.status === 'running' ? 'text-warning animate-pulse' : 'text-success'}`}>
                {status.status === 'running' ? 'Тестирование...' : 'Ожидание'}
              </p>
            </CardBody>
          </Card>
          <Card>
            <CardBody>
              <p className="text-[10px] uppercase tracking-wider text-tertiary mb-1">Интервал (часы)</p>
              <Select
                value={status.interval_hours}
                disabled={!canWrite}
                onChange={e => {
                  const h = parseInt(e.target.value)
                  setAutoTunerInterval(h).then(() => { toast.success(`Интервал: ${h}ч`); loadData() }).catch(e => toast.error(e.message))
                }}
              >
                {[1,2,4,6,8,12,24].map(h => <option key={h} value={h}>{h} ч</option>)}
              </Select>
            </CardBody>
          </Card>
          <Card>
            <CardBody>
              <p className="text-[10px] uppercase tracking-wider text-tertiary mb-1">Последний / Следующий</p>
              <p className="text-xs font-medium text-primary tabular-nums">{status.last_run ? new Date(status.last_run).toLocaleString('ru-RU') : '—'}</p>
              {status.next_run && (
                <p className="text-[10px] text-accent mt-0.5 tabular-nums">→ {new Date(status.next_run).toLocaleString('ru-RU')}</p>
              )}
            </CardBody>
          </Card>
          <Card>
            <CardBody>
              <p className="text-[10px] uppercase tracking-wider text-tertiary mb-1">Тестов сегодня</p>
              <p className="text-lg font-semibold text-primary tabular-nums">
                {status.today_tests || 0}
                <span className="text-sm font-normal text-tertiary"> ({status.today_stable || 0} стабильных)</span>
              </p>
            </CardBody>
          </Card>
        </div>
      )}

      {/* Active parameters */}
      {status && (
        <Card className="mb-6">
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Активные параметры подключения</h3>
          </CardHeader>
          <CardBody>
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">SNI домен</p>
                <p className="text-sm font-semibold font-mono text-accent">{status.current_sni || '—'}</p>
              </div>
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">TLS Fingerprint</p>
                <p className="text-sm font-semibold font-mono text-info">{status.current_fingerprint || '—'}</p>
              </div>
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">Transport</p>
                <p className="text-sm font-semibold font-mono text-primary">{status.current_transport || '—'}</p>
                <p className="text-[9px] text-tertiary">фиксирован для Reality</p>
              </div>
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">Flow</p>
                <p className="text-sm font-semibold font-mono text-primary">{status.current_flow || 'none'}</p>
                <p className="text-[9px] text-tertiary">фиксирован для Reality</p>
              </div>
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">DNS серверы</p>
                <p className="text-xs font-mono text-primary">{status.current_dns || '—'}</p>
              </div>
              <div className="p-3 rounded border border-default bg-canvas">
                <p className="text-[10px] uppercase tracking-wider text-tertiary">Mux Concurrency</p>
                <p className="text-sm font-semibold text-primary tabular-nums">{status.current_mux || '8'}</p>
              </div>
            </div>
            <p className="text-[10px] text-tertiary mt-3">
              Config version: <span className="tabular-nums">{status.config_version || '1'}</span> • Transport и Flow фиксированы (tcp + xtls-rprx-vision) — требование VLESS Reality
            </p>
          </CardBody>
        </Card>
      )}

      {/* Applied configurations */}
      {appliedResults.length > 0 && (
        <Card className="mb-6">
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Применённые конфигурации</h3>
          </CardHeader>
          <CardBody>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {appliedResults.slice(0, 6).map(r => (
                <div key={r.id} className="p-3 rounded border border-success/30 bg-success/5">
                  <div className="flex items-center gap-2 mb-1">
                    <Badge variant="success">Applied</Badge>
                    <span className="text-[10px] text-tertiary">{r.target_type}</span>
                  </div>
                  <p className="text-sm font-mono font-semibold text-primary">{r.sni_domain}</p>
                  <p className="text-xs text-tertiary mt-0.5">
                    Avg: <span className="text-success tabular-nums">{r.avg_latency_ms}ms</span> • <span className="tabular-nums">{r.success_count}/5</span> success
                  </p>
                </div>
              ))}
            </div>
          </CardBody>
        </Card>
      )}

      {/* Hub Inbound results */}
      {hubResults.length > 0 && (
        <Card className="mb-6">
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Hub Inbound — тест SNI доменов</h3>
            <p className="text-xs text-tertiary mt-1">Какие домены маскировки доступны из России (TLS handshake к домену:443)</p>
          </CardHeader>
          <CardBody>
            <Table>
              <Thead>
                <Tr>
                  <Th>SNI домен</Th>
                  <Th align="center">Avg пинг</Th>
                  <Th align="center">Успех</Th>
                  <Th align="center">Стабильный</Th>
                  <Th align="center">Применён</Th>
                  <Th>Ошибка</Th>
                </Tr>
              </Thead>
              <Tbody>
                {hubResults.map(r => (
                  <Tr key={r.id} className={r.was_applied ? 'bg-success/5' : ''}>
                    <Td mono>{r.sni_domain}</Td>
                    <Td align="center" mono>
                      <span className={latencyClass(r.avg_latency_ms)}>{r.avg_latency_ms}ms</span>
                    </Td>
                    <Td align="center" mono>{r.success_count}/5</Td>
                    <Td align="center">
                      {r.is_stable
                        ? <span className="text-success">✓</span>
                        : <span className="text-danger">✗</span>}
                    </Td>
                    <Td align="center">
                      {r.was_applied && <Badge variant="success">ACTIVE</Badge>}
                    </Td>
                    <Td>
                      <span className="text-danger/70 text-xs block truncate max-w-[200px]" title={r.error_msg}>{r.error_msg || ''}</span>
                    </Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          </CardBody>
        </Card>
      )}

      {/* Exit Node results */}
      {exitResults.length > 0 && (
        <Card className="mb-6">
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Exit Nodes — тест туннелей</h3>
          </CardHeader>
          <CardBody>
            <Table>
              <Thead>
                <Tr>
                  <Th>Target</Th>
                  <Th>SNI</Th>
                  <Th align="center">Avg пинг</Th>
                  <Th align="center">Успех</Th>
                  <Th align="center">Стабильный</Th>
                  <Th>Ошибка</Th>
                </Tr>
              </Thead>
              <Tbody>
                {exitResults.map(r => (
                  <Tr key={r.id} className={r.was_applied ? 'bg-success/5' : ''}>
                    <Td>
                      <span className="text-secondary text-xs">{r.target_type} {r.server_id > 0 ? `(#${r.server_id})` : ''}</span>
                    </Td>
                    <Td mono>{r.sni_domain}</Td>
                    <Td align="center" mono>
                      <span className={latencyClass(r.avg_latency_ms)}>{r.avg_latency_ms}ms</span>
                    </Td>
                    <Td align="center" mono>{r.success_count}/5</Td>
                    <Td align="center">
                      {r.is_stable
                        ? <span className="text-success">✓</span>
                        : <span className="text-danger">✗</span>}
                    </Td>
                    <Td>
                      <span className="text-danger/70 text-xs block truncate max-w-[200px]" title={r.error_msg}>{r.error_msg || ''}</span>
                    </Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          </CardBody>
        </Card>
      )}

      {/* Pagination */}
      {historyTotal > 30 && (
        <div className="flex items-center justify-center gap-3 mb-6">
          <Button variant="ghost" size="sm" disabled={historyPage <= 1} onClick={() => setHistoryPage(p => p - 1)} aria-label="Предыдущая">←</Button>
          <span className="text-sm text-tertiary tabular-nums">{historyPage} / {Math.ceil(historyTotal / 30)}</span>
          <Button variant="ghost" size="sm" disabled={historyPage >= Math.ceil(historyTotal / 30)} onClick={() => setHistoryPage(p => p + 1)} aria-label="Следующая">→</Button>
          <span className="text-xs text-tertiary tabular-nums">({historyTotal} всего)</span>
        </div>
      )}

      {history.length === 0 && (
        <EmptyState
          icon={<Zap size={32} />}
          title="Нет данных тестирования"
          description={`Автотюнер запускается каждые ${status?.interval_hours || 4} ч, если включён.`}
        />
      )}

      <Dialog open={enableDialog} onClose={() => setEnableDialog(false)} title="Включить автотюнер?" maxWidth="max-w-md">
        <div className="space-y-3 text-sm text-secondary">
          <p>Автотюнер сам меняет SNI, отпечаток TLS, DNS и примерно раз в неделю пароль obfs Бамбука.</p>
          <p className="text-warning">
            После смены SNI или obfs у пользователей перестают работать профили, пока они не обновят подписку в приложении.
          </p>
          {!status?.loop_running && (
            <p>Сейчас тюнер в API не запущен: включение начнёт действовать после следующего перезапуска API.</p>
          )}
          <label className="flex items-center gap-2 text-primary cursor-pointer">
            <input type="checkbox" className="accent-accent" checked={understood} onChange={e => setUnderstood(e.target.checked)} />
            Понимаю последствия
          </label>
        </div>
        <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 mt-6">
          <Button variant="ghost" onClick={() => setEnableDialog(false)}>Отмена</Button>
          <Button variant="danger" disabled={!understood} onClick={() => { setEnableDialog(false); setEnabled(true) }}>Включить</Button>
        </div>
      </Dialog>
    </div>
  )
}
