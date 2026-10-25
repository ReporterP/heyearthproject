// One provisioning job in words (pure; node --test). Input: GET /api/admin/provision-jobs/{id}
// (provision.JobView: status running|succeeded|failed|interrupted, steps with Russian titles).
import { STEP_STATUS, jobFinished, pathView } from './nodeModel.js'

export const POLL_MS = 2000
const MAX_POLL_MS = 15000

const BAD_STEP = ['failed', 'rolled_back', 'drift', 'interrupted']
const FINISHED_STEP = ['ok', 'done', 'skipped', 'drift']

// path_hy «skipped»: the main tunnel did not answer while the TCP fallback is on — path_tcp decides.
const MAIN_PATH_SKIPPED = {
  label: 'не отвечает', variant: 'warning',
  hint: 'Основной путь не отвечает (похоже, UDP «Россия → заграница» задушен) — решает проверка запасного TCP',
}

export function stepView(step) {
  const st = step?.status
  if (step?.name === 'path_hy' && st === 'skipped') return { ...MAIN_PATH_SKIPPED }
  if (Object.hasOwn(STEP_STATUS, st)) return { ...STEP_STATUS[st] }
  return { label: st || '—', variant: 'warning' }
}

export function stepTitle(step) {
  return step?.title || step?.name || `Шаг ${Number.isInteger(step?.idx) ? step.idx + 1 : '?'}`
}

// autoOpenStep: the step to show expanded unless the owner chose: the first problem, else the one running.
export function autoOpenStep(job) {
  const steps = job?.steps || []
  const pick = steps.find((s) => BAD_STEP.includes(s.status)) || steps.find((s) => s.status === 'running')
  return pick ? pick.idx : null
}

// openStep: choice undefined = automatic; null = the owner collapsed everything; idx = the owner's step.
export const openStep = (choice, job) => (choice === undefined ? autoOpenStep(job) : choice)

const RUNNING = {
  provision: 'Идёт настройка', reprovision: 'Идёт переустановка', verify: 'Идёт проверка',
  deprovision: 'Идёт отключение', mtproxy: 'Идёт установка Telegram-прокси',
}

export function jobHeadline(job) {
  const st = job?.status
  switch (st) {
    case 'running': case 'queued':
      return { label: Object.hasOwn(RUNNING, job.kind) ? RUNNING[job.kind] : 'Задача выполняется', variant: 'info' }
    case 'succeeded':
      if ((job.steps || []).some((s) => s.status === 'drift')) {
        return { label: 'Готово, есть расхождения', variant: 'warning', hint: 'Нода отличается от ожидаемой настройки — откройте отмеченные шаги; исправит «Переустановить»' }
      }
      return { label: 'Готово', variant: 'success' }
    case 'failed':
      return { label: 'Ошибка', variant: 'danger' }
    case 'interrupted':
      return { label: 'Прервано', variant: 'warning', hint: 'API перезапустился во время задачи — её можно довести до конца кнопкой «Продолжить»' }
    default:
      return { label: `Неизвестно: ${st || '—'}`, variant: 'warning' }
  }
}

export function jobProgress(job) {
  const steps = job?.steps || []
  return { done: steps.filter((s) => FINISHED_STEP.includes(s.status)).length, total: steps.length }
}

// pathVerdict: the final check of both tunnel paths of a finished job (path_hy / path_tcp steps),
// in pathView's words (nodeModel.js); null while the job runs or for jobs without path steps.
export function pathVerdict(job, { tcpEnabled = true } = {}) {
  if (!jobFinished(job)) return null
  const steps = job.steps || []
  const hyStep = steps.find((s) => s.name === 'path_hy')
  const tcpStep = steps.find((s) => s.name === 'path_tcp')
  if (!hyStep && !tcpStep) return null
  const hy = pathView({ status: hyStep?.status, source: 'job' })
  const tcp = pathView({ status: tcpStep?.status, source: 'job' }, { enabled: tcpEnabled })
  const hyOK = hy.variant === 'success'
  const tcpOK = tcp.variant === 'success'
  const verdict = (variant, text) => ({ variant, text, hy, tcp })
  if (hyOK) {
    if (tcpOK) return verdict('success', 'Оба пути туннеля работают')
    if (tcp.off) return verdict('success', 'Основной туннель работает; запасной TCP-путь выключен в настройках ноды')
    return verdict('warning', 'Основной туннель работает, запасной TCP-путь не подтверждён')
  }
  if (hy.variant === 'default') return verdict('default', 'Пути туннеля не проверялись: задача остановилась раньше')
  if (tcpOK) {
    return verdict('warning', 'Основной туннель (UDP) не отвечает — нода работает по запасному TCP. Основной путь вернётся сам, когда UDP «Россия → заграница» восстановится')
  }
  return verdict('danger', tcp.off
    ? 'Основной туннель не отвечает, а запасной TCP-путь выключен — выход не включён в маршрутизацию'
    : 'Ни один путь туннеля не ответил — выход не включён в маршрутизацию')
}

// nextPollDelay: 2 s; after failed requests back off up to 15 s; null = stop (session gone, no
// permission, job deleted with its node).
export function nextPollDelay(failures, status) {
  if (status === 401 || status === 403 || status === 404) return null
  if (!failures) return POLL_MS
  return Math.min(POLL_MS * 2 ** failures, MAX_POLL_MS)
}
