import { useEffect, useState } from 'react'
import { CircleCheck, CircleHelp, CircleX } from 'lucide-react'
import { fetchOpsRun } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Spinner } from '../ui/index.js'

const STATUS = {
  scheduled: ['запланировано', 'default'],
  running: ['выполняется', 'info'],
  ok: ['готово', 'success'],
  failed: ['не удалось', 'danger'],
  post_failed: ['проверка «после» не прошла', 'danger'],
  cancelled: ['отменено', 'default'],
  interrupted: ['прервано перезапуском API', 'warning'],
}

/**
 * ActionRun — one run of a console action: its status, progress lines and the checks after;
 * polled every 3 s while it runs. Props: runId, onClose().
 */
export default function ActionRun({ runId, onClose }) {
  const [run, setRun] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let stop = false
    let timer
    async function poll() {
      try {
        const r = await fetchOpsRun(runId)
        if (stop) return
        setRun(r); setError('')
        if (r.status === 'running') timer = setTimeout(poll, 3000)
      } catch (e) { if (!stop) { setError(e.message); timer = setTimeout(poll, 5000) } }
    }
    poll()
    return () => { stop = true; clearTimeout(timer) }
  }, [runId])

  if (!run) return error ? <p role="alert" className="text-danger text-sm">{error}</p> : <div className="flex justify-center py-6"><Spinner size="sm" /></div>
  const [label, tone] = STATUS[run.status] || [run.status, 'default']
  return (
    <div className="flex flex-col gap-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={tone}>{label}</Badge>
        {run.dry_run && <Badge variant="warning">пробный режим</Badge>}
        {run.status === 'running' && <Spinner size="xs" />}
        {run.scheduled_for && run.status === 'scheduled' && <span className="text-secondary">на {formatDate(run.scheduled_for)}</span>}
        <span className="font-mono text-xs text-tertiary sm:ml-auto">#{run.id}</span>
      </div>
      {run.progress?.length > 0 && (
        <ul className="font-mono text-xs text-secondary space-y-0.5 border border-default rounded p-2 max-h-48 overflow-auto">
          {run.progress.map((l, i) => <li key={i} className="break-words">{l}</li>)}
        </ul>
      )}
      {run.error && <p className="text-danger break-words">{run.error}</p>}
      {run.post?.length > 0 && (
        <ul className="space-y-0.5">
          {run.post.map((c, i) => (
            <li key={i} className="flex items-start gap-1.5">
              {c.ok ? <CircleCheck aria-hidden="true" className="w-3.5 h-3.5 mt-0.5 text-success shrink-0" /> : c.blind ? <CircleHelp aria-hidden="true" className="w-3.5 h-3.5 mt-0.5 text-warning shrink-0" /> : <CircleX aria-hidden="true" className="w-3.5 h-3.5 mt-0.5 text-danger shrink-0" />}
              <span className={c.ok ? 'text-secondary' : c.blind ? 'text-warning' : 'text-danger'}>{c.name}{c.blind ? ' — не удалось проверить' : ''}{c.detail ? ` — ${c.detail}` : ''}</span>
            </li>
          ))}
        </ul>
      )}
      <div className="flex justify-end"><Button variant="secondary" onClick={onClose}>Закрыть</Button></div>
    </div>
  )
}
