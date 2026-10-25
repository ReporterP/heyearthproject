import { useEffect, useId, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { fetchJob, resumeJob } from '../../api/admin.js'
import { Badge, Button, Skeleton, Spinner } from '../ui/index.js'
import { JOB_KIND, jobFinished, canResume } from './nodeModel.js'
import { POLL_MS, stepView, stepTitle, openStep, jobHeadline, jobProgress, pathVerdict, nextPollDelay } from './jobModel.js'

const TONE = { success: 'text-success', warning: 'text-warning', danger: 'text-danger', info: 'text-info', default: 'text-tertiary' }

// StepOutput keeps the newest lines of a running step in view.
function StepOutput({ id, text }) {
  const ref = useRef(null)
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight
  }, [text])
  if (!text) return <p id={id} className="px-3 pb-3 pl-11 text-xs text-tertiary">Вывода нет.</p>
  return (
    <pre id={id} ref={ref}
      className="mx-3 mb-3 p-2 max-h-64 overflow-y-auto rounded border border-default bg-canvas whitespace-pre-wrap break-words font-mono text-xs text-secondary">
      {text}
    </pre>
  )
}

/**
 * JobProgress — live steps of one provisioning job: GET /provision-jobs/{id} every 2 s until it ends
 * (backs off on errors, stops on 401/403/404, pauses while the tab is hidden; polling ends on unmount).
 * The problem step (else the running one) is expanded; failed|interrupted → «Продолжить» (POST …/resume).
 * Props: jobId; canProvision (nodes.provision — the resume button); tcpEnabled (default true — the node's
 * TCP fallback switch, for the final path check); onFinished(job) — once per finish seen (also when the
 * job had already finished at mount; again after «Продолжить» finishes it anew).
 * Reused by the add-node wizard, the node card (2.15) and the reserve-entry wizard (3.9).
 */
export default function JobProgress({ jobId, canProvision = false, tcpEnabled = true, onFinished }) {
  const [job, setJob] = useState(null)
  const [error, setError] = useState('')
  const [stopped, setStopped] = useState(false)
  const [choice, setChoice] = useState(undefined) // undefined = automatic (see openStep)
  const [busy, setBusy] = useState(false)
  const [run, setRun] = useState(0) // bumped after «Продолжить»: polling starts again
  const uid = useId()
  const busyRef = useRef(false)
  const finishedRef = useRef(onFinished)
  const reported = useRef('')
  useEffect(() => { finishedRef.current = onFinished })

  // Another job: nothing of the previous one is shown as this one's.
  useEffect(() => { setJob(null); setChoice(undefined); setError('') }, [jobId])

  useEffect(() => {
    let alive = true
    let timer
    let failures = 0
    setStopped(false)
    async function poll() {
      if (document.visibilityState === 'hidden') { timer = setTimeout(poll, POLL_MS); return }
      let delay
      try {
        const j = await fetchJob(jobId)
        if (!alive) return
        failures = 0
        setJob(j)
        setError('')
        if (jobFinished(j)) {
          const key = `${j.id}:${j.status}:${j.finished_at || ''}`
          if (reported.current !== key) { reported.current = key; finishedRef.current?.(j) }
          return
        }
        delay = nextPollDelay(0)
      } catch (e) {
        if (!alive) return
        failures += 1
        setError(e.message)
        delay = nextPollDelay(failures, e.status)
        if (delay == null) { setStopped(true); return }
      }
      timer = setTimeout(poll, delay)
    }
    poll()
    return () => { alive = false; clearTimeout(timer) }
  }, [jobId, run])

  async function resume() {
    if (busyRef.current) return
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      await resumeJob(jobId)
      setChoice(undefined)
    } catch (e) {
      setError(e.message)
    } finally {
      busyRef.current = false
      setBusy(false)
      setRun((r) => r + 1) // re-read the job either way: it may have been resumed elsewhere
    }
  }

  if (!job) {
    return error
      ? <p role="alert" className="text-sm text-danger break-words">Не удалось загрузить задачу: {error}</p>
      : <Skeleton className="h-40 w-full" />
  }

  const head = jobHeadline(job)
  const progress = jobProgress(job)
  const running = !jobFinished(job)
  const open = openStep(choice, job)
  const verdict = pathVerdict(job, { tcpEnabled })
  const toggle = (idx) => setChoice(open === idx ? null : idx)

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mb-2" aria-live="polite">
        <Badge variant={head.variant}>
          {running && <Spinner size="xs" />}
          {head.label}
        </Badge>
        <span className="text-xs text-tertiary">
          {JOB_KIND[job.kind] || 'Задача'} · <span className="font-mono tabular-nums">#{job.id}</span>
        </span>
        {progress.total > 0 && (
          <span className="ml-auto font-mono tabular-nums text-xs text-secondary">
            {progress.done}/{progress.total}<span className="sr-only"> шагов выполнено</span>
          </span>
        )}
      </div>
      {head.hint && <p className="text-xs text-secondary mb-2">{head.hint}</p>}

      <ol className="border border-default rounded">
        {(job.steps || []).map((s) => {
          const v = stepView(s)
          const expanded = open === s.idx
          const outId = `${uid}-step-${s.idx}`
          return (
            <li key={s.idx} className="border-b border-default last:border-b-0">
              <button type="button" aria-expanded={expanded} aria-controls={expanded ? outId : undefined} onClick={() => toggle(s.idx)}
                className="w-full flex items-center gap-2 min-h-9 px-3 py-1.5 text-left hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent">
                <ChevronRight aria-hidden="true" className={`w-3.5 h-3.5 shrink-0 text-tertiary transition-transform ${expanded ? 'rotate-90' : ''}`} />
                <span className="w-5 shrink-0 font-mono tabular-nums text-xs text-tertiary">{s.idx + 1}</span>
                <span className="flex-1 min-w-0 text-sm text-primary break-words">{stepTitle(s)}</span>
                <Badge variant={v.variant} className="shrink-0">{v.label}</Badge>
              </button>
              {v.hint && <p className="px-3 pb-2 pl-11 text-xs text-warning">{v.hint}</p>}
              {expanded && <StepOutput id={outId} text={s.output_tail} />}
            </li>
          )
        })}
      </ol>

      {error && (
        <p role="alert" className="mt-2 text-xs text-danger break-words">
          {stopped ? 'Обновление остановлено: ' : 'Не удалось обновить: '}{error}
        </p>
      )}
      {job.error && <p className="mt-3 text-sm text-danger break-words">{job.error}</p>}

      {verdict && (
        <div className="mt-3 rounded border border-default bg-elevated p-3">
          <div className="text-xs text-secondary mb-2">Итоговая проверка путей</div>
          <div className="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-6 text-sm text-primary">
            <span className="flex items-center gap-2">Основной туннель <Badge variant={verdict.hy.variant}>{verdict.hy.label}</Badge></span>
            <span className="flex items-center gap-2">Запасной TCP <Badge variant={verdict.tcp.variant}>{verdict.tcp.label}</Badge></span>
          </div>
          <p className={`mt-2 text-xs ${TONE[verdict.variant] || TONE.default}`}>{verdict.text}</p>
        </div>
      )}

      {canResume(job) && (canProvision ? (
        <div className="mt-3 flex flex-col sm:flex-row sm:items-center gap-2">
          <Button onClick={resume} loading={busy} className="w-full sm:w-auto">Продолжить</Button>
          <span className="text-xs text-tertiary">Задача начнётся с первого шага: выполненные шаги только проверятся, без изменений.</span>
        </div>
      ) : (
        <p className="mt-3 text-xs text-tertiary">Продолжить задачу может администратор с правом настройки нод.</p>
      ))}
    </div>
  )
}
