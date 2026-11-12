// Owner-facing truth about nodes (pure; node --test). Input: one item of GET /api/admin/nodes
// (internal/admin/provision_handlers.go nodeItem).
import { PATH } from '../../utils/serverState.js' // phase 1: path statuses of the tunnel monitor

export const ROLE_TABS = [
  { key: 'exit', label: 'Выходы за рубежом' },
  { key: 'relay', label: 'Резервные входы в РФ' },
]

export const JOB_KIND = {
  provision: 'Настройка', reprovision: 'Переустановка', verify: 'Проверка', deprovision: 'Отключение',
  mtproxy: 'Telegram-прокси',
}

// The wording of «Приоритет» (load_weight) everywhere in the admin panel.
export const PRIORITY_HINT = 'Выходы с одинаковым приоритетом делят нагрузку; выход с меньшим приоритетом подхватывает её, если остальные недоступны или заметно медленнее'

const LIFECYCLE = {
  pending: { label: 'Ждёт настройки', variant: 'default' },
  provisioning: { label: 'Настраивается', variant: 'info' },
  online: { label: 'Работает', variant: 'success' },
  failed: { label: 'Ошибка настройки', variant: 'danger' },
  deprovisioning: { label: 'Отключается', variant: 'warning' },
  offline: { label: 'Отключена', variant: 'default' },
}

function lifecycleState(s) {
  return Object.hasOwn(LIFECYCLE, s)
    ? { ...LIFECYCLE[s] }
    : { label: s ? `Неизвестно: ${s}` : 'Нет данных', variant: 'warning' }
}

const NOT_CHECKED = { label: 'не проверялся', variant: 'default' }

// pathView: one tunnel path in words. Statuses: the live monitor's (phase 1:
// healthy|unhealthy|unknown|absent — one wording with ServerDetailPage, utils/serverState.js; GET
// /nodes prefers them once the monitor saw the exit) or the last job's path step
// (ok|done|failed|drift|rolled_back, and for the main path «skipped»: it did not answer while the
// TCP path decided — a warning, not an error). enabled=false: the owner switched the TCP path off
// (off: true). The switch does not act on a legacy vless exit — TCP is its only path, so the
// monitor still reports it: a monitor status other than «absent» is shown as it is.
export function pathView(path, { enabled = true } = {}) {
  const s = path?.status
  if (!enabled) {
    if (path?.source === 'monitor' && s !== 'absent' && Object.hasOwn(PATH, s)) {
      return { ...PATH[s], hint: 'Единственный путь этого выхода — выключатель запасного TCP на него не действует' }
    }
    return { label: 'выключен', variant: 'default', off: true, hint: 'Запасной TCP-путь отключён в настройках ноды' }
  }
  if (Object.hasOwn(PATH, s)) return { ...PATH[s] }
  switch (s) {
    case 'ok': case 'done': return { ...PATH.healthy }
    case 'failed': case 'rolled_back': case 'drift': return { ...PATH.unhealthy }
    case 'skipped':
      return { label: 'не отвечает', variant: 'warning', hint: 'Основной путь не отвечает: при последней проверке нода заработала по запасному TCP' }
    default: return { ...NOT_CHECKED }
  }
}

// mainPathDown: the main (UDP) path is known not to work — dead for the monitor or a job, or
// skipped by the last job's check because it did not answer.
function mainPathDown(path) {
  return path?.status === 'skipped' || pathView(path).variant === 'danger'
}

// A verify job is worded apart from the monitor's «Проверяется» (an exit not judged yet).
const RUNNING = { deprovision: 'Отключается…', verify: 'Идёт проверка (задача)' }
const INTERRUPTED = { deprovision: 'Отключение прервано', verify: 'Проверка прервана' }

// nodeState: one badge that never says «Работает» when it does not. Order: a job at work, an
// interrupted job, a dead exit, the unconfirmed host key, an exit not judged yet, a dead main
// path (working over TCP, or TCP not confirmed), then the stored lifecycle.
export function nodeState(n) {
  const job = n.last_job
  if (job?.status === 'running') {
    return { label: RUNNING[job.kind] || 'Настраивается…', variant: 'info' }
  }
  if (job?.status === 'interrupted') {
    return { label: INTERRUPTED[job.kind] || 'Настройка прервана', variant: 'warning', hint: 'API перезапустился во время задачи — откройте ноду и нажмите «Продолжить»' }
  }
  // Exits only: a relay carries no Hub→exit tunnel, its tunnel_status means nothing.
  const exitOnline = n.role !== 'relay' && n.lifecycle_state === 'online'
  if (exitOnline && n.tunnel_status === 'unhealthy') {
    return { label: 'Выход недоступен', variant: 'danger', hint: 'Монитор не получает ответ ни по одному пути — трафик идёт через другие выходы' }
  }
  if (!n.host_key?.confirmed) {
    return { label: 'Подтвердите ключ', variant: 'warning', hint: 'Отпечаток ключа сервера не сверен — Hub не подключается к нему по SSH: настройка, проверка и отключение недоступны. Трафик пользователей это не останавливает' }
  }
  if (exitOnline) {
    if (n.tunnel_status !== 'healthy') {
      return { label: 'Проверяется', variant: 'info', hint: 'Монитор ещё не признал туннель рабочим — трафик через этот выход пока не идёт' }
    }
    if (mainPathDown(n.paths?.hy)) {
      const tcp = pathView(n.paths?.tcp, { enabled: n.tunnel_fallback_enabled !== false })
      if (tcp.variant === 'success') {
        return { label: 'Работает по запасному TCP', variant: 'warning', hint: 'Основной туннель (UDP) при последней проверке не ответил — трафик идёт по TCP' }
      }
      return { label: 'Основной туннель не работает', variant: 'warning', hint: tcp.off
        ? 'Основной туннель (UDP) не отвечает, запасной TCP-путь отключён'
        : 'Основной туннель (UDP) не отвечает, запасной TCP-путь ещё не подтверждён' }
    }
  }
  return lifecycleState(n.lifecycle_state)
}

// pathsStale: a switched-off node is not checked; its last path results are history, not state.
export const pathsStale = (n) => n.lifecycle_state === 'offline'

// nodeHref: the node card on «Ноды», on the tab of its role (exit unless it is known to be a relay).
export const nodeHref = (id, role) => `/nodes?role=${role === 'relay' ? 'relay' : 'exit'}&node=${encodeURIComponent(id)}`

export const STEP_STATUS = {
  pending: { label: 'ждёт', variant: 'default' }, running: { label: 'выполняется', variant: 'info' },
  ok: { label: 'в порядке', variant: 'success' }, done: { label: 'сделано', variant: 'success' },
  skipped: { label: 'пропущено', variant: 'default' }, drift: { label: 'расхождение', variant: 'warning' },
  failed: { label: 'ошибка', variant: 'danger' }, rolled_back: { label: 'откачено', variant: 'danger' },
  interrupted: { label: 'прервано', variant: 'warning' },
}

export const jobFinished = (j) => !!j && ['succeeded', 'failed', 'interrupted'].includes(j.status)
export const canResume = (j) => !!j && (j.status === 'failed' || j.status === 'interrupted')
export const defaultPlanIds = (plans) => (plans || []).filter((p) => p.is_active && p.price_rub > 0).map((p) => p.id)

// Host keys: the type as the provider console shows it and the file whose fingerprint the owner
// compares there (ssh-keygen -lf <file>). host_key.key_type is the ssh wire type.
const KEY_FAMILIES = [
  { re: /^ssh-ed25519$/, label: 'Ed25519', file: '/etc/ssh/ssh_host_ed25519_key.pub' },
  { re: /^ecdsa-sha2-/, label: 'ECDSA', file: '/etc/ssh/ssh_host_ecdsa_key.pub' },
  { re: /^(ssh-rsa|rsa-sha2-)/, label: 'RSA', file: '/etc/ssh/ssh_host_rsa_key.pub' },
]
const keyFamily = (t) => KEY_FAMILIES.find((f) => f.re.test(t || ''))
export const keyTypeLabel = (t) => keyFamily(t)?.label || t || ''
export const hostKeyFile = (t) => keyFamily(t)?.file || ''

// unconfirmedKeys: nodes the Hub will not SSH into until the owner confirms the fingerprint.
export const unconfirmedKeys = (items) => (items || []).filter((n) => !n.host_key?.confirmed)

const FP_RE = /^SHA256:[A-Za-z0-9+/]{43}$/

export function wizardCanAdvance(step, d) {
  switch (step) {
    case 0: return (d.role === 'exit' && /^[A-Za-z]{2}$/.test(d.country || '')) || (d.role === 'relay' && d.country === 'RU')
    case 1: return !!d.serverId && FP_RE.test(d.fingerprint || '') && d.fingerprintConfirmed === true
    case 2: return d.preflight?.ok === true
    // A relay's step 3 is «Проверка с телефона»: advisory, the owner decides (no options to check).
    case 3: return d.role === 'relay' || (Number.isInteger(d.loadWeight) && d.loadWeight >= 1 && d.loadWeight <= 10)
    default: return false
  }
}
