import { useEffect, useState } from 'react'
import { CircleCheck, CircleX } from 'lucide-react'
import { previewOpsAction, runOpsAction } from '../../api/admin.js'
import { Badge, Button, Dialog, Input, Skeleton } from '../ui/index.js'
import { canRunNow, canSchedule, confirmView } from './actionsModel.js'
import ActionRun from './ActionRun.jsx'

/**
 * ActionConfirm — the one confirmation dialog of every console action: the class and what it
 * means, «Заденет сейчас: N» (unknown → «считать, что заденет всех»), the checks before (run
 * now, read-only) and the names of the checks after, «если проверка „после“ не пройдёт», the
 * best window; «В окно», «Сейчас» (for K3 only after the word is typed), «Отмена». After the
 * start it shows the run (ActionRun).
 * Props: request {id, target} | null, onClose().
 */
export default function ActionConfirm({ request, onClose }) {
  const [preview, setPreview] = useState(null)
  const [error, setError] = useState('')
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState('')
  const [runId, setRunId] = useState(0)

  useEffect(() => {
    setPreview(null); setError(''); setTyped(''); setRunId(0)
    if (!request) return
    previewOpsAction(request.id, request.target || '').then(setPreview, e => setError(e.message))
  }, [request])

  async function start(when) {
    setBusy(when); setError('')
    try {
      const r = await runOpsAction(request.id, { target: request.target || '', confirmToken: preview.confirm_token, typed, when })
      setRunId(r.run_id)
    } catch (e) { setError(e.message) } finally { setBusy('') }
  }

  const v = preview ? confirmView(preview) : null
  const title = v?.title || 'Действие'
  return (
    <Dialog open={!!request} onClose={onClose} title={runId ? `${title}: ход` : title} maxWidth="max-w-xl">
      {runId ? <ActionRun runId={runId} onClose={onClose} /> : (
        <div className="flex flex-col gap-3 text-sm">
          {!preview && !error && <Skeleton className="h-40 w-full" />}
          {error && <p role="alert" className="text-danger">{error}</p>}
          {v && (<>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={v.impactLabel === 'К3' || v.impactLabel === 'К2' ? 'danger' : v.impactLabel === 'К1' ? 'warning' : 'default'}>{v.impactLabel}</Badge>
              <span className="text-secondary">{v.impactNote}</span>
              {request?.target && <span className="font-mono text-xs text-tertiary">цель #{request.target}</span>}
            </div>
            {v.dryRun && <p className="text-warning">Пробный режим: действие запишет в ленту «сделал бы» и ничего не изменит. В боевой режим переводит владелец — «Автоматика».</p>}
            <p className="text-primary">{v.affected}</p>
            {v.pre.length > 0 && (
              <div>
                <p className="text-tertiary text-xs mb-1">Перед</p>
                <ul className="space-y-0.5">
                  {v.pre.map((c, i) => (
                    <li key={i} className="flex items-start gap-1.5">
                      {c.ok ? <CircleCheck aria-hidden="true" className="w-3.5 h-3.5 mt-0.5 text-success shrink-0" /> : <CircleX aria-hidden="true" className="w-3.5 h-3.5 mt-0.5 text-danger shrink-0" />}
                      <span className={c.ok ? 'text-secondary' : 'text-danger'}>{c.name}{c.detail ? ` — ${c.detail}` : ''}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {v.post.length > 0 && (
              <div>
                <p className="text-tertiary text-xs mb-1">После</p>
                <p className="text-secondary">{v.post.join(', ')}</p>
              </div>
            )}
            {v.ifFails && <p className="text-secondary"><span className="text-tertiary">Если проверка «после» не пройдёт: </span>{v.ifFails}</p>}
            <p className="text-tertiary text-xs">{v.window}{v.retryAfter > 0 ? ` · повторить можно через ${Math.ceil(v.retryAfter / 60)} мин` : ''}</p>
            {v.word && (
              <label className="flex flex-col gap-1">
                <span className="text-xs text-tertiary">Чтобы запустить или запланировать, введите <span className="font-mono text-primary">{v.word}</span></span>
                <Input value={typed} onChange={e => setTyped(e.target.value)} autoComplete="off" spellCheck={false} className="font-mono" aria-label="Слово подтверждения" />
              </label>
            )}
            {v.missing && <p className="text-warning">{v.missing}</p>}
            <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 pt-1">
              <Button variant="ghost" onClick={onClose}>Отмена</Button>
              <Button variant={v.defaultWhen === 'window' ? 'primary' : 'secondary'} disabled={!canSchedule(preview, typed) || !!busy}
                loading={busy === 'window'} onClick={() => start('window')}>
                В окно
              </Button>
              <Button variant={v.defaultWhen === 'now' ? 'primary' : 'danger'} disabled={!canRunNow(preview, typed) || !!busy}
                loading={busy === 'now'} onClick={() => start('now')}>
                Сейчас
              </Button>
            </div>
          </>)}
        </div>
      )}
    </Dialog>
  )
}
