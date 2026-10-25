// The node card (NodeDrawer) as pure rules (node --test): which tabs and actions an admin gets and
// why an action is unavailable, the routing requests, the host-key check and the consequences of
// the dangerous actions. Input: one item of GET /api/admin/nodes (provision_handlers.go nodeItem).
import { pathView, pathsStale, keyTypeLabel, hostKeyFile } from './nodeModel.js'
import { matchPasted } from './wizardModel.js'

export const PRIORITIES = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
export const PLANS_HINT = 'Без привязки нода получает только общий пул; тарифы с привязкой ходят только через свои ноды'

// Units whose journal the backend serves (servers_handlers.go nodeLogServices).
export const LOG_SERVICES = [
  { key: 'node-agent', label: 'node-agent' },
  { key: 'xray', label: 'xray' },
  { key: 'hysteria2', label: 'hysteria2' },
  { key: 'mtproxy', label: 'mtproxy (Telegram-прокси)' },
]

const isExit = (n) => n?.role !== 'relay'

export const jobActive = (n) => ['running', 'queued'].includes(n?.last_job?.status)

// drawerTabs: perms = { manage: nodes.provision, ops: server_ops.execute, logs: servers.logs }.
export function drawerTabs(node, { manage = false, logs = false } = {}) {
  return [
    { key: 'overview', label: 'Обзор' },
    isExit(node) && { key: 'routing', label: 'Тарифы и приоритет' },
    { key: 'jobs', label: 'Задачи' },
    logs && { key: 'logs', label: 'Журналы' },
    manage && { key: 'danger', label: 'Опасная зона' },
  ].filter(Boolean)
}

// initialTab: an unconfirmed key comes first (the «Ноды» notice opens the card to confirm it),
// then a job at work or interrupted («Продолжить» lives with it).
export function initialTab(node) {
  if (!node?.host_key?.confirmed) return 'overview'
  return ['running', 'queued', 'interrupted'].includes(node?.last_job?.status) ? 'jobs' : 'overview'
}

// initialJobId: the last job worth showing at once — at work, interrupted or failed.
export function initialJobId(node) {
  const j = node?.last_job
  return j && ['running', 'queued', 'interrupted', 'failed'].includes(j.status) ? j.id : null
}

const HIDDEN = { show: false, reason: '' }
const JOB_BUSY = 'Идёт задача — дождитесь её окончания (вкладка «Задачи»)'
const KEY_FIRST = 'Сначала подтвердите отпечаток ключа SSH: без него Hub не подключается к серверу'
const NO_NAME = 'У ноды нет названия, а это действие подтверждается вводом названия'
const DELETE_FAILED = 'Сначала «Отключить»: после неудачной настройки на Hub мог остаться клиент туннеля, а на сервере — запущенные сервисы'
const DELETE_LIVE = 'Удалить можно только отключённую или ещё не настроенную ноду — сначала «Отключить»'
const INTERRUPTED = 'Предыдущая задача не закончилась — продолжите её во вкладке «Задачи»'

const VERIFY_STATE = {
  pending: 'Нода ещё не настроена — проверять нечего',
  offline: 'Нода отключена — проверять нечего',
  provisioning: INTERRUPTED,
  deprovisioning: INTERRUPTED,
}

function setupAction(n, gate) {
  const life = n.lifecycle_state
  switch (life) {
    case 'online':
      return { show: true, kind: 'reprovision', label: 'Переустановить', confirm: true, reason: gate(n.name?.trim() ? '' : NO_NAME) }
    case 'pending':
      return { show: true, kind: 'provision', label: 'Настроить', confirm: false, reason: gate() }
    case 'failed': case 'offline': case 'provisioning':
      return { show: true, kind: 'provision', label: 'Настроить заново', confirm: false, reason: gate() }
    case 'deprovisioning':
      return { show: true, kind: 'provision', label: 'Настроить заново', confirm: false, reason: 'Нода выводится из работы — сначала доведите отключение до конца (вкладка «Задачи»)' }
    default:
      return { show: true, kind: 'provision', label: 'Настроить', confirm: false, reason: `Неизвестное состояние ноды «${life || '—'}»` }
  }
}

// nodeActions: every action of the card → { show, reason } (reason '' = available). Starting a
// job needs nodes.provision; actions over SSH also a confirmed host key (deprovision does not: the
// node leaves routing first, its node-side steps wait for the key — see deprovisionConsequences).
export function nodeActions(node, { manage = false, ops = false, logs = false } = {}) {
  const n = node || {}
  const exit = isExit(n)
  const life = n.lifecycle_state
  const busy = jobActive(n)
  const keyOK = !!n.host_key?.confirmed
  const named = !!n.name?.trim()
  const gate = (extra = '') => (busy ? JOB_BUSY : !keyOK ? KEY_FIRST : extra)
  const out = {
    tunnelCheck: { show: exit && life === 'online', reason: '' },
    logs: logs ? { show: true, reason: keyOK ? '' : KEY_FIRST } : HIDDEN,
    verify: HIDDEN, setup: HIDDEN, mtproxy: HIDDEN, deprovision: HIDDEN, remove: HIDDEN, fallback: HIDDEN, routing: HIDDEN,
  }
  if (!manage) return out
  if (exit) {
    out.routing = { show: true, reason: '' }
    out.fallback = { show: true, reason: '' }
    out.verify = { show: true, reason: gate(Object.hasOwn(VERIFY_STATE, life) ? VERIFY_STATE[life] : '') }
    out.setup = setupAction(n, gate)
    if (ops && life === 'online') {
      out.mtproxy = { show: true, reason: gate(n.mtproto_status === 'disabled' ? 'Telegram-прокси для этой ноды выключен' : '') }
    }
  }
  out.deprovision = {
    show: true,
    reason: busy ? JOB_BUSY : !named ? NO_NAME : life === 'pending' ? 'Нода ещё не настраивалась — её запись можно просто удалить' : '',
  }
  let remove = ''
  if (busy) remove = JOB_BUSY
  else if (life === 'failed') remove = DELETE_FAILED
  else if (life !== 'pending' && life !== 'offline') remove = DELETE_LIVE
  else if (!named) remove = NO_NAME
  out.remove = { show: true, reason: remove }
  return out
}

// ── routing: PUT /servers/{id}/routing {plan_ids, load_weight, tunnel_fallback_enabled?} ──

export function parsePriority(v) {
  const t = String(v ?? '').trim()
  if (!/^\d{1,2}$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 10 ? n : null
}

export function clampPriority(v) {
  const n = Math.round(Number(v))
  return Number.isFinite(n) ? Math.min(10, Math.max(1, n)) : 1
}

const cleanIds = (ids) => [...new Set((ids || []).map(Number).filter((x) => Number.isInteger(x) && x > 0))].sort((a, b) => a - b)
export const sortedPlanIds = cleanIds

// routingBody: the «Тарифы и приоритет» save. Without the plan list (no settings.read, a failed
// request) the stored binding is sent back as it is. The TCP switch is left out: the backend keeps it.
export function routingBody(node, { planIds, weight, plansLoaded }) {
  const w = parsePriority(weight)
  if (w === null) return { error: 'Приоритет — целое число от 1 до 10' }
  return { body: { plan_ids: cleanIds(plansLoaded ? planIds : node?.plan_ids), load_weight: w } }
}

// fallbackBody: the TCP switch saves only itself — the stored plans and priority, never edits
// still unsaved on the other tab.
export function fallbackBody(node, enabled) {
  return { plan_ids: cleanIds(node?.plan_ids), load_weight: clampPriority(node?.load_weight), tunnel_fallback_enabled: enabled === true }
}

export function routingDirty(node, { planIds, weight, plansLoaded }) {
  if (parsePriority(weight) !== clampPriority(node?.load_weight)) return true
  return !!plansLoaded && cleanIds(planIds).join(',') !== cleanIds(node?.plan_ids).join(',')
}

// fallbackOffRisky: switching the TCP path off on a working exit whose main path is not confirmed
// working takes the exit out of traffic. Not for a legacy vless exit (monitor: hy «absent» — TCP
// is its only path and the switch does not act on it).
export function fallbackOffRisky(node) {
  if (!isExit(node) || node?.lifecycle_state !== 'online' || node?.tunnel_fallback_enabled === false) return false
  const hy = node?.paths?.hy
  if (hy?.source === 'monitor' && hy?.status === 'absent') return false
  return pathView(hy).variant !== 'success'
}

// pathRow: one tunnel path for the overview. A switched-off path has no RTT or check time; an
// offline node's paths are history (pathsStale) and not shown as state.
export function pathRow(node, which) {
  if (pathsStale(node)) {
    return { stale: true, view: { label: '—', variant: 'default', hint: 'Нода отключена — пути не проверяются' }, rttMs: 0, checkedAt: null, error: '' }
  }
  const p = node?.paths?.[which]
  const view = which === 'tcp' ? pathView(p, { enabled: node?.tunnel_fallback_enabled !== false }) : pathView(p)
  const live = !view.off
  return {
    stale: false, view,
    rttMs: live && p?.rtt_ms > 0 ? p.rtt_ms : 0,
    checkedAt: live ? p?.checked_at || null : null,
    error: live ? p?.error || '' : '',
  }
}

const MTPROTO = {
  healthy: { label: 'работает', variant: 'success' },
  unhealthy: { label: 'не работает', variant: 'danger', hint: 'Повтор установки — автоматически каждые 30 минут или кнопкой «Переустановить Telegram-прокси»' },
  installing: { label: 'устанавливается', variant: 'info' },
  unknown: { label: 'не проверялся', variant: 'default' },
  disabled: { label: 'выключен', variant: 'default', hint: 'Telegram-прокси выключен для этой ноды при настройке' },
}

export function mtprotoView(status) {
  return Object.hasOwn(MTPROTO, status) ? { ...MTPROTO[status] } : { label: status ? `Неизвестно: ${status}` : 'нет данных', variant: 'warning' }
}

const JOB_LIST = {
  running: { label: 'идёт', variant: 'info' }, queued: { label: 'в очереди', variant: 'info' },
  succeeded: { label: 'готово', variant: 'success' }, failed: { label: 'ошибка', variant: 'danger' },
  interrupted: { label: 'прервано', variant: 'warning' },
}

export function jobListStatus(j) {
  return Object.hasOwn(JOB_LIST, j?.status) ? { ...JOB_LIST[j.status] } : { label: j?.status || '—', variant: 'warning' }
}

// ── host key: POST /servers/probe {server_id} → {fingerprint, key_type, keys[], stored} ──

const FP_RE = /^SHA256:[A-Za-z0-9+/]{43}$/

export function probeKeyList(result) {
  let keys = (result?.keys || []).filter((k) => FP_RE.test(k?.fingerprint || '')).map((k) => ({ type: k.type || '', fingerprint: k.fingerprint }))
  if (keys.length === 0 && FP_RE.test(result?.fingerprint || '')) keys = [{ type: result.key_type || '', fingerprint: result.fingerprint }]
  return keys
}

// keyRows: what the owner compares in the provider's console — the key's own file for every type
// (exit 2's migrated key is ECDSA: ssh_host_ecdsa_key.pub, not ed25519).
export function keyRows(keys, storedFp) {
  return (keys || []).map((k) => {
    const file = hostKeyFile(k.type)
    return { ...k, label: keyTypeLabel(k.type) || 'Ключ', file, cmd: file ? `ssh-keygen -lf ${file}` : '', stored: !!storedFp && k.fingerprint === storedFp }
  })
}

function defaultProbeKey(keys, result) {
  const has = (fp) => !!fp && keys.some((k) => k.fingerprint === fp)
  if (has(result?.stored?.fingerprint)) return result.stored.fingerprint
  if (has(result?.fingerprint)) return result.fingerprint
  return keys[0]?.fingerprint || ''
}

export const initialKeyCheck = { probe: null, keys: [], selected: '', pasted: '', ownerConfirmed: false }

export function keyPasteVerdict(s) {
  if (!String(s?.pasted ?? '').trim()) return 'none'
  return matchPasted(s.pasted, s.keys)?.fingerprint === s.selected ? 'match' : 'mismatch'
}

const selectable = (s) => !!s.selected && s.keys.some((k) => k.fingerprint === s.selected) && keyPasteVerdict(s) !== 'mismatch'

export const canConfirmProbeKey = (s) => selectable(s) && s.ownerConfirmed === true

// keyCheckReducer: the card's key check. As in the add-node wizard, a pasted ssh-keygen line only
// selects the key; confirming it is always the owner's own switch, and choosing another key drops it.
export function keyCheckReducer(s, a) {
  switch (a?.type) {
    case 'probed': {
      const keys = probeKeyList(a.result)
      return { probe: a.result || null, keys, selected: defaultProbeKey(keys, a.result), pasted: '', ownerConfirmed: false }
    }
    case 'select':
      if (a.fingerprint === s.selected || !s.keys.some((k) => k.fingerprint === a.fingerprint)) return s
      return { ...s, selected: a.fingerprint, ownerConfirmed: false }
    case 'paste': {
      const text = String(a.text ?? '')
      if (!text.trim()) return { ...s, pasted: '' }
      const k = matchPasted(text, s.keys)
      if (!k) return { ...s, pasted: text, ownerConfirmed: false }
      return { ...s, pasted: text, selected: k.fingerprint, ownerConfirmed: k.fingerprint === s.selected && s.ownerConfirmed }
    }
    case 'confirm':
      if (a.value === true && !selectable(s)) return s
      return { ...s, ownerConfirmed: a.value === true }
    case 'reset':
      return initialKeyCheck
    default:
      return s
  }
}

export function storedKeyNotice(result) {
  const st = result?.stored
  if (!st?.fingerprint || st.presented) return ''
  return st.confirmed
    ? 'Сервер больше не предъявляет подтверждённый ключ: он переустановлен или это не ваш сервер. Hub к нему не подключится, пока вы не сверите ключ с консолью хостера и не подтвердите его заново.'
    : 'Сервер не предъявил сохранённый ключ — сверьте с консолью хостера ключи ниже.'
}

export function keyReplaceWarning(hostKey, selected) {
  if (!hostKey?.confirmed || !hostKey.fingerprint || !selected || selected === hostKey.fingerprint) return ''
  return 'Подтверждённый ключ будет заменён: дальше Hub будет подключаться к серверу только с выбранным ключом.'
}

// ── consequences (ConfirmDanger) ──

export function reprovisionConsequences() {
  return [
    'Каждый шаг сначала проверяется; меняется только то, что отличается от эталона.',
    'Сервис с изменённой конфигурацией перезапустится: на несколько секунд его путь пропадёт, второй путь ноды продолжит работать — у пользователей возможны короткие обрывы соединений.',
    'Пока идёт задача, другие действия с нодой недоступны.',
  ]
}

export function deprovisionConsequences(node) {
  const out = []
  if (node?.lifecycle_state === 'offline') {
    out.push('Нода уже отключена: повтор уберёт с сервера то, что не удалось убрать в прошлый раз (например, если сервер был недоступен).')
  } else {
    out.push('Нода сразу исключается из маршрутизации: пользователи переедут на другие выходы, текущие соединения через неё оборвутся и переподключатся.')
    out.push('Если это последний выход тарифа, у тарифа останется только общий пул.')
  }
  if (isExit(node)) {
    out.push('На Hub удаляется клиент туннеля к ноде; на сервере останавливаются hysteria2, xray, node-agent и Telegram-прокси, снимаются защита и прыжки портов, пароль туннеля меняется.')
  }
  if (!node?.host_key?.confirmed) {
    out.push('Отпечаток ключа не подтверждён: нода выйдет из маршрутизации, но шаги на сервере выполнятся, только когда вы подтвердите ключ и нажмёте «Продолжить».')
  }
  out.push('Запись останется в состоянии «Отключена»: её можно удалить или настроить ноду заново.')
  return out
}

export function deleteConsequences() {
  return [
    'Запись ноды и история её задач удаляются безвозвратно.',
    'Сервер у хостера не удаляется и не меняется — отмените его аренду сами.',
  ]
}

export function fallbackOffConsequences() {
  return [
    'Основной туннель (UDP) этой ноды сейчас не подтверждён рабочим.',
    'Без запасного TCP-пути выход перестанет принимать трафик, пока основной туннель не заработает: пользователи переедут на другие выходы.',
  ]
}
