import { useEffect, useState } from 'react'
import { Activity } from 'lucide-react'
import { checkServerTunnel } from '../../api/admin.js'
import { Badge, Button, Dialog, Spinner } from '../ui/index.js'

const STATUS = {
  ok: { variant: 'success', label: 'работает' },
  fail: { variant: 'danger', label: 'не работает' },
  absent: { variant: 'default', label: 'нет' },
}

// Read-only «Проверить туннель»: the backend fetches generate_204 through each path of the exit
// and changes nothing (no status, no config, no restart).
export default function TunnelCheckDialog({ server, onClose }) {
  const [state, setState] = useState({ loading: true, result: null, error: '' })

  async function run() {
    setState({ loading: true, result: null, error: '' })
    try {
      setState({ loading: false, result: await checkServerTunnel(server.id), error: '' })
    } catch (e) {
      setState({ loading: false, result: null, error: e.message })
    }
  }

  useEffect(() => { run() }, [server.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const { loading, result, error } = state
  return (
    <Dialog open onClose={onClose} title={`Проверка туннеля — ${server.name || server.ip_address}`}
      icon={<Activity className="w-4 h-4" />} maxWidth="max-w-lg">
      <p className="text-xs text-tertiary mb-4">Запрос generate_204 через каждый путь до exit. Только чтение: статусы и конфиги не меняются.</p>
      {loading ? (
        <div className="flex items-center gap-2 text-sm text-secondary"><Spinner size="sm" /> Проверяем оба пути (до 10 секунд)…</div>
      ) : error ? (
        <p className="text-sm text-danger">{error}</p>
      ) : (
        <div className="space-y-3">
          {(result.paths || []).map((p) => {
            const s = STATUS[p.status] || STATUS.absent
            return (
              <div key={p.path} className="flex flex-col gap-1 border-b border-default pb-3">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm text-primary">{p.name}</span>
                  <span className="flex items-center gap-2">
                    {p.rtt_ms > 0 && <span className="font-mono tabular-nums text-xs text-secondary">{p.rtt_ms} мс</span>}
                    <Badge variant={s.variant}>{s.label}</Badge>
                  </span>
                </div>
                {p.error && <p className="text-xs font-mono text-tertiary break-all">{p.error}</p>}
              </div>
            )
          })}
          <p className="text-sm text-primary">{result.summary}</p>
        </div>
      )}
      <div className="flex justify-end gap-2 mt-6">
        <Button variant="ghost" onClick={run} disabled={loading}>Повторить</Button>
        <Button variant="primary" onClick={onClose}>Закрыть</Button>
      </div>
    </Dialog>
  )
}
