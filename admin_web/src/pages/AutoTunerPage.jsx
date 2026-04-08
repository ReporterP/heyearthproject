import { useState, useEffect, useCallback } from 'react'
import { useT } from '../i18n/index.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchAutoTunerStatus, fetchAutoTunerHistory, toggleAutoTuner, setAutoTunerInterval, runAutoTunerNow, cleanupAutoTunerHistory } from '../api/admin.js'

export default function AutoTunerPage() {
  const { t } = useT()
  const toast = useToast()
  const [status, setStatus] = useState(null)
  const [history, setHistory] = useState([])
  const [historyTotal, setHistoryTotal] = useState(0)
  const [historyPage, setHistoryPage] = useState(1)
  const [loading, setLoading] = useState(true)

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
    const interval = setInterval(loadData, 15000) // auto-refresh every 15s
    return () => clearInterval(interval)
  }, [loadData])

  async function handleToggle() {
    const newVal = !(status?.enabled)
    try {
      await toggleAutoTuner(newVal)
      setStatus(prev => ({ ...prev, enabled: newVal }))
      toast.success(newVal ? 'Auto-tuner включён' : 'Auto-tuner выключен')
    } catch (e) {
      toast.error(e.message)
    }
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-8 h-8 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
    </div>
  )

  const stableResults = history.filter(r => r.is_stable)
  const appliedResults = history.filter(r => r.was_applied)
  const hubResults = history.filter(r => r.target_type === 'hub_inbound')
  const exitResults = history.filter(r => r.target_type?.startsWith('exit_'))

  return (
    <div>
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-2xl font-bold">Auto-Tuner</h2>
          <p className="text-sm text-text-secondary mt-1">
            Автоматический подбор оптимальных параметров прокси — SNI домены, fingerprint, транспорт
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => { runAutoTunerNow().then(() => { toast.success('Запуск!'); setTimeout(loadData, 2000) }).catch(e => toast.error(e.message)) }}
            className="px-4 py-2 rounded-lg bg-brand/20 text-brand hover:bg-brand/30 text-sm font-medium border border-brand/30 transition">
            ▶ Запустить сейчас
          </button>
          <button onClick={() => { cleanupAutoTunerHistory(7).then(d => { toast.success(`Удалено: ${d.deleted}`); loadData() }).catch(e => toast.error(e.message)) }}
            className="px-3 py-2 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary text-sm transition" title="Очистить историю старше 7 дней">
            🗑 Очистить
          </button>
          <button onClick={loadData}
            className="px-3 py-2 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary text-sm transition">
            ↻
          </button>
          <button onClick={handleToggle}
            className={`px-4 py-2 rounded-lg text-sm font-semibold transition ${
              status?.enabled
                ? 'bg-green-500/20 text-green-400 hover:bg-green-500/30 border border-green-500/30'
                : 'bg-red-500/20 text-red-400 hover:bg-red-500/30 border border-red-500/30'
            }`}>
            {status?.enabled ? '● Вкл' : '○ Выкл'}
          </button>
        </div>
      </div>

      {/* Status cards */}
      {status && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 mb-6">
          <div className="bg-surface-card rounded-xl border border-white/10 p-4">
            <p className="text-[10px] uppercase tracking-wider text-text-secondary mb-1">Статус</p>
            <p className={`text-lg font-bold ${status.status === 'running' ? 'text-amber-400 animate-pulse' : 'text-green-400'}`}>
              {status.status === 'running' ? 'Тестирование...' : 'Ожидание'}
            </p>
          </div>
          <div className="bg-surface-card rounded-xl border border-white/10 p-4">
            <p className="text-[10px] uppercase tracking-wider text-text-secondary mb-1">Интервал (часы)</p>
            <select value={status.interval_hours} onChange={e => {
              const h = parseInt(e.target.value)
              setAutoTunerInterval(h).then(() => { toast.success(`Интервал: ${h}ч`); loadData() }).catch(e => toast.error(e.message))
            }} className="text-lg font-bold bg-transparent border-none outline-none cursor-pointer text-text-primary">
              {[1,2,4,6,8,12,24].map(h => <option key={h} value={h}>{h} ч</option>)}
            </select>
          </div>
          <div className="bg-surface-card rounded-xl border border-white/10 p-4">
            <p className="text-[10px] uppercase tracking-wider text-text-secondary mb-1">Последний / Следующий</p>
            <p className="text-xs font-semibold">{status.last_run ? new Date(status.last_run).toLocaleString('ru-RU') : '—'}</p>
            {status.next_run && (
              <p className="text-[10px] text-brand mt-0.5">→ {new Date(status.next_run).toLocaleString('ru-RU')}</p>
            )}
          </div>
          <div className="bg-surface-card rounded-xl border border-white/10 p-4">
            <p className="text-[10px] uppercase tracking-wider text-text-secondary mb-1">Тестов сегодня</p>
            <p className="text-lg font-bold">{status.today_tests || 0} <span className="text-sm font-normal text-text-secondary">({status.today_stable || 0} стабильных)</span></p>
          </div>
        </div>
      )}

      {/* Active parameters — ALL tunable params */}
      {status && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5 mb-6">
          <h3 className="text-base font-semibold mb-3">Активные параметры подключения</h3>
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">SNI домен</p>
              <p className="text-sm font-bold font-mono text-brand">{status.current_sni || '—'}</p>
            </div>
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">TLS Fingerprint</p>
              <p className="text-sm font-bold font-mono text-violet-400">{status.current_fingerprint || '—'}</p>
            </div>
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">Transport</p>
              <p className="text-sm font-bold font-mono">{status.current_transport || '—'}</p>
              <p className="text-[9px] text-text-secondary">фиксирован для Reality</p>
            </div>
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">Flow</p>
              <p className="text-sm font-bold font-mono">{status.current_flow || 'none'}</p>
              <p className="text-[9px] text-text-secondary">фиксирован для Reality</p>
            </div>
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">DNS серверы</p>
              <p className="text-xs font-mono">{status.current_dns || '—'}</p>
            </div>
            <div className="p-3 rounded-lg bg-surface-alt/50">
              <p className="text-[10px] uppercase tracking-wider text-text-secondary">Mux Concurrency</p>
              <p className="text-sm font-bold">{status.current_mux || '8'}</p>
            </div>
          </div>
          <p className="text-[10px] text-text-secondary mt-2">Config version: {status.config_version || '1'} • Transport и Flow фиксированы (tcp + xtls-rprx-vision) — требование VLESS Reality</p>
        </div>
      )}

      {/* Summary: best results per target */}
      {appliedResults.length > 0 && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5 mb-6">
          <h3 className="text-base font-semibold mb-3">Применённые конфигурации</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            {appliedResults.slice(0, 6).map(r => (
              <div key={r.id} className="p-3 rounded-lg bg-green-500/5 border border-green-500/20">
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-green-400 text-xs font-bold">✓ Applied</span>
                  <span className="text-[10px] text-text-secondary">{r.target_type}</span>
                </div>
                <p className="text-sm font-mono font-semibold">{r.sni_domain}</p>
                <p className="text-xs text-text-secondary mt-0.5">
                  Avg: <span className="text-green-400">{r.avg_latency_ms}ms</span> • {r.success_count}/5 success
                </p>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Hub Inbound results */}
      {hubResults.length > 0 && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5 mb-6">
          <h3 className="text-base font-semibold mb-3">Hub Inbound — тест SNI доменов</h3>
          <p className="text-xs text-text-secondary mb-3">Какие домены маскировки доступны из России (TLS handshake к домену:443)</p>
          <div className="overflow-x-auto rounded-lg border border-white/5">
            <table className="w-full text-xs">
              <thead className="bg-surface-alt/30">
                <tr>
                  <th className="py-2 px-3 text-left">SNI домен</th>
                  <th className="py-2 px-3 text-center">Avg пинг</th>
                  <th className="py-2 px-3 text-center">Успех</th>
                  <th className="py-2 px-3 text-center">Стабильный</th>
                  <th className="py-2 px-3 text-center">Применён</th>
                  <th className="py-2 px-3 text-left">Ошибка</th>
                </tr>
              </thead>
              <tbody>
                {hubResults.map(r => (
                  <tr key={r.id} className={`border-t border-white/5 ${r.was_applied ? 'bg-green-500/5' : ''}`}>
                    <td className="py-1.5 px-3 font-mono font-medium">{r.sni_domain}</td>
                    <td className="py-1.5 px-3 text-center">
                      <span className={r.avg_latency_ms > 500 ? 'text-red-400 font-bold' : r.avg_latency_ms > 200 ? 'text-amber-400' : 'text-green-400 font-bold'}>
                        {r.avg_latency_ms}ms
                      </span>
                    </td>
                    <td className="py-1.5 px-3 text-center">{r.success_count}/5</td>
                    <td className="py-1.5 px-3 text-center">
                      {r.is_stable ? <span className="text-green-400">✓</span> : <span className="text-red-400">✗</span>}
                    </td>
                    <td className="py-1.5 px-3 text-center">
                      {r.was_applied && <span className="px-2 py-0.5 rounded bg-green-500/20 text-green-400 text-[10px] font-bold">ACTIVE</span>}
                    </td>
                    <td className="py-1.5 px-3 text-red-400/70 truncate max-w-[200px]" title={r.error_msg}>{r.error_msg || ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Exit Node results */}
      {exitResults.length > 0 && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5 mb-6">
          <h3 className="text-base font-semibold mb-3">Exit Nodes — тест туннелей</h3>
          <div className="overflow-x-auto rounded-lg border border-white/5">
            <table className="w-full text-xs">
              <thead className="bg-surface-alt/30">
                <tr>
                  <th className="py-2 px-3 text-left">Target</th>
                  <th className="py-2 px-3 text-left">SNI</th>
                  <th className="py-2 px-3 text-center">Avg пинг</th>
                  <th className="py-2 px-3 text-center">Успех</th>
                  <th className="py-2 px-3 text-center">Стабильный</th>
                  <th className="py-2 px-3 text-left">Ошибка</th>
                </tr>
              </thead>
              <tbody>
                {exitResults.map(r => (
                  <tr key={r.id} className={`border-t border-white/5 ${r.was_applied ? 'bg-green-500/5' : ''}`}>
                    <td className="py-1.5 px-3 text-text-secondary">{r.target_type} {r.server_id > 0 ? `(#${r.server_id})` : ''}</td>
                    <td className="py-1.5 px-3 font-mono">{r.sni_domain}</td>
                    <td className="py-1.5 px-3 text-center">
                      <span className={r.avg_latency_ms > 500 ? 'text-red-400' : r.avg_latency_ms > 200 ? 'text-amber-400' : 'text-green-400'}>
                        {r.avg_latency_ms}ms
                      </span>
                    </td>
                    <td className="py-1.5 px-3 text-center">{r.success_count}/5</td>
                    <td className="py-1.5 px-3 text-center">{r.is_stable ? '✓' : '✗'}</td>
                    <td className="py-1.5 px-3 text-red-400/70 truncate max-w-[200px]" title={r.error_msg}>{r.error_msg || ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Pagination */}
      {historyTotal > 30 && (
        <div className="flex items-center justify-center gap-3 mb-6">
          <button disabled={historyPage <= 1} onClick={() => setHistoryPage(p => p - 1)}
            className="px-3 py-1.5 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary text-sm disabled:opacity-30 transition">←</button>
          <span className="text-sm text-text-secondary">{historyPage} / {Math.ceil(historyTotal / 30)}</span>
          <button disabled={historyPage >= Math.ceil(historyTotal / 30)} onClick={() => setHistoryPage(p => p + 1)}
            className="px-3 py-1.5 rounded-lg bg-surface-alt text-text-secondary hover:text-text-primary text-sm disabled:opacity-30 transition">→</button>
          <span className="text-xs text-text-secondary">({historyTotal} всего)</span>
        </div>
      )}

      {history.length === 0 && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-8 text-center">
          <p className="text-text-secondary">Нет данных тестирования. Auto-tuner запускается каждые {status?.interval_hours || 4} часа.</p>
        </div>
      )}
    </div>
  )
}
