import { useEffect, useState } from 'react'
import { fetchHysteria2Status } from '../../api/admin.js'
import { ageText, bambooVerdict } from '../../utils/bamboo.js'
import { Badge, Card, CardBody, CardHeader, Skeleton } from '../ui/index.js'

function Fact({ label, children, title }) {
  return (
    <div className="min-w-0">
      <p className="text-[10px] uppercase tracking-wider text-tertiary mb-1">{label}</p>
      <p className="text-sm text-primary truncate" title={title}>{children}</p>
    </div>
  )
}

const fmtDateTime = (iso) => (iso ? new Date(iso).toLocaleString('ru-RU') : '—')

// «Бамбук» (Hysteria2 on the Hub): does it answer from outside, which exit it leaves through,
// when the obfs password was rotated. Read-only (GET /hysteria2/status carries no secrets);
// refreshes every 30 s while the tab is visible. Needs servers.read — the page mounts it only then.
export default function BambooCard() {
  const [st, setSt] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let alive = true
    const load = () => fetchHysteria2Status()
      .then((s) => { if (alive) { setSt(s); setError('') } })
      .catch((e) => { if (alive) setError(e.message) })
    load()
    const timer = setInterval(() => { if (document.visibilityState === 'visible') load() }, 30000)
    return () => { alive = false; clearInterval(timer) }
  }, [])

  const v = bambooVerdict(st)
  return (
    <Card>
      <CardHeader className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-primary">Бамбук на Hub</h3>
        <Badge variant={v.variant}>{v.label}</Badge>
      </CardHeader>
      <CardBody>
        {error && <p className="text-sm text-danger mb-3">Не удалось обновить статус: {error}</p>}
        {!st ? (!error && <Skeleton className="h-16" />) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <Fact label="Выход за рубежом" title={st.bound_exit_id ? `${st.bound_exit_name || 'выход'} #${st.bound_exit_id}` : undefined}>
              {st.bound_exit_id
                ? <>{st.bound_exit_name || 'выход'} <span className="font-mono tabular-nums text-tertiary">#{st.bound_exit_id}</span></>
                : <span className="text-tertiary">ещё не выбран</span>}
            </Fact>
            <Fact label="Выбран">
              <span className="font-mono tabular-nums">{fmtDateTime(st.bound_exit_since)}</span>
            </Fact>
            <Fact label="Задержка пути (сглаженная)">
              <span className="font-mono tabular-nums">{st.bound_exit_rtt_ms ? `${st.bound_exit_rtt_ms} мс` : '—'}</span>
            </Fact>
            <Fact label="Ротация пароля obfs" title={st.obfs_rotated_at ? fmtDateTime(st.obfs_rotated_at) : undefined}>
              {ageText(st.obfs_rotated_at)}
            </Fact>
          </div>
        )}
        {st?.probe && (
          <p className={`mt-3 text-xs break-words ${st.probe.ok ? 'text-tertiary' : 'text-danger'}`}>
            Проверка снаружи <span className="font-mono tabular-nums">{fmtDateTime(st.probe.at)}</span>: {st.probe.detail || (st.probe.ok ? 'отвечает' : 'не отвечает')}
          </p>
        )}
        {st?.last_error && <p className="mt-2 text-xs font-mono text-danger break-all">{st.last_error}</p>}
        <p className="mt-3 text-[11px] text-tertiary">
          Выход выбирается по задержке пути Hysteria: ради скорости меняется не чаще раза в 10 минут и только на выход быстрее
          хотя бы на 20 %; если у текущего выхода путь Hysteria пропал — сразу.
        </p>
      </CardBody>
    </Card>
  )
}
