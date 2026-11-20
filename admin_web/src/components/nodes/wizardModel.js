// «Добавить ноду» as a pure state machine (node --test): the component only renders the state and
// sends the requests built here. Steps advance only through wizardCanAdvance (nodeModel.js), the
// install step is entered only by starting the job and never left. The root password never enters
// the state: its field is uncontrolled (no value attribute in the DOM, nothing in React state), only
// passwordFlags() of it is dispatched, and the component reads it once — for createNode. A pasted
// fingerprint only selects a key: confirming it is always the owner's own switch.
import { wizardCanAdvance, defaultPlanIds } from './nodeModel.js'

// Step 0 is «Подготовьте сервер у хостера» (B2): the role, the Hub key, the ports, the country.
export const WIZARD_STEPS = ['Подготовка', 'Доступ и ключ', 'Предпроверка', 'Настройки', 'Установка']
// A reserve entry has no options: its step 3 is the phone check of its address on mobile networks.
export const RELAY_STEPS = ['Подготовка', 'Доступ и ключ', 'Предпроверка', 'Проверка с телефона', 'Установка']
export const wizardSteps = (role) => (role === 'relay' ? RELAY_STEPS : WIZARD_STEPS)
export const INSTALL_STEP = 4
export const SSH_USER = 'root'

export function initialWizard(initialRole = 'exit') {
  return {
    step: 0,
    role: initialRole === 'relay' ? 'relay' : 'exit',
    country: initialRole === 'relay' ? 'RU' : '', // a reserve entry lives in Russia
    name: '',
    ip: '',
    sshPort: '22',
    passwordEntered: false, // something typed (closing would lose it)
    passwordValid: false, // not blank
    probe: null, // { ip, port, keys: [{ type, fingerprint }] } — for the address it was made for
    fingerprint: '', // the key the owner compares with the provider's console
    fingerprintConfirmed: false,
    pasted: '', // ssh-keygen output pasted from the provider's console (optional)
    serverId: null,
    preflight: null,
    plans: [],
    plansState: 'loading', // loading | ready | failed
    plansError: '',
    planIds: null, // null until the plans load: then all active paid ones
    mtproxy: false, // off by default (B2): it needs a masking domain's A record on the node
    loadWeight: 1,
    fallback: true,
    fallbackStored: true, // servers.tunnel_fallback_enabled of a new node (DEFAULT true)
    tunnel: false, // an entry's «Туннель до Hub» (servers.relay_tunnel): off by default
    jobId: null,
  }
}

// parsePort: 1..65535; an empty field is the default 22; anything else is null.
export function parsePort(text) {
  const t = String(text ?? '').trim()
  if (t === '') return 22
  if (!/^\d{1,5}$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 65535 ? n : null
}

export const countryValid = (c) => /^[A-Za-z]{2}$/.test(String(c ?? '').trim())

// passwordFlags: all the state may know of the typed password (action «passwordFlags»).
export function passwordFlags(text) {
  const t = String(text ?? '')
  return { entered: t !== '', valid: t.trim() !== '' }
}

const FP_TOKEN = /(?<![A-Za-z0-9+/])(?:SHA256:)?([A-Za-z0-9+/]{43})(?![A-Za-z0-9+/=])/g

// matchPasted: the probed key whose SHA256 fingerprint appears in the pasted text (the whole line
// of `ssh-keygen -lf …` or the bare fingerprint, with or without «SHA256:»); null if none does.
export function matchPasted(text, keys) {
  const list = keys || []
  for (const m of String(text ?? '').matchAll(FP_TOKEN)) {
    const k = list.find((key) => key.fingerprint === `SHA256:${m[1]}`)
    if (k) return k
  }
  return null
}

// pasteVerdict: none (nothing pasted) | match (the pasted fingerprint is the chosen key's) |
// mismatch (another key's, or none of the keys the server presented — never confirmable).
export function pasteVerdict(s) {
  if (!String(s.pasted ?? '').trim()) return 'none'
  return matchPasted(s.pasted, s.probe?.keys)?.fingerprint === s.fingerprint ? 'match' : 'mismatch'
}

const addressOf = (s) => ({ ip: String(s.ip ?? '').trim(), port: parsePort(s.sshPort) })

function probeIsCurrent(s) {
  const a = addressOf(s)
  return !!s.probe && s.probe.ip === a.ip && s.probe.port === a.port
}

const hasKey = (s, fp) => !!fp && !!s.probe?.keys.some((k) => k.fingerprint === fp)

export const canConfirmKey = (s) => !s.serverId && probeIsCurrent(s) && hasKey(s, s.fingerprint) && pasteVerdict(s) !== 'mismatch'

// canCreate: the password is optional — without one the Hub's key (put on the server at the
// hoster) must work, the server checks it; a typed blank one is still refused.
export function canCreate(s) {
  const roleOK = (s.role === 'exit' && countryValid(s.country)) || (s.role === 'relay' && s.country === 'RU')
  return !s.serverId && roleOK && canConfirmKey(s) && s.fingerprintConfirmed === true &&
    (s.passwordEntered !== true || s.passwordValid === true)
}

// canStart: «Установить» — the options are valid and the plans are no longer loading (without
// them plan_ids would silently be left out: the node would get the common pool only).
export const canStart = (s) => !!s.serverId && s.plansState !== 'loading' && wizardCanAdvance(3, s)

// canGo: back to any earlier step until the install starts; forward only through finished steps;
// the install step itself only by starting the job (action «started»).
export function canGo(s, target) {
  if (!Number.isInteger(target) || target < 0 || target >= INSTALL_STEP || s.step === INSTALL_STEP) return false
  if (target < s.step) return true
  if (target === s.step) return false
  for (let i = s.step; i < target; i++) {
    if (!wizardCanAdvance(i, s)) return false
  }
  return true
}

export function normalizePlans(p) {
  if (Array.isArray(p)) return p
  if (Array.isArray(p?.plans)) return p.plans
  if (Array.isArray(p?.items)) return p.items
  return []
}

const EDITABLE = ['role', 'country', 'name', 'ip', 'sshPort', 'mtproxy', 'loadWeight', 'fallback']
const LOCKED_AFTER_SAVE = ['role', 'country', 'name', 'ip', 'sshPort']

function applySet(s, patch) {
  const next = { ...s }
  for (const [k, v] of Object.entries(patch || {})) {
    if (!EDITABLE.includes(k) || (s.serverId && LOCKED_AFTER_SAVE.includes(k))) continue
    switch (k) {
      case 'role':
        if (v === 'relay') Object.assign(next, { role: 'relay', country: 'RU' })
        else if (v === 'exit') Object.assign(next, { role: 'exit', country: s.role === 'relay' ? '' : next.country })
        break
      case 'country': if (next.role !== 'relay') next.country = String(v ?? '').toUpperCase(); break
      case 'loadWeight': next.loadWeight = typeof v === 'number' ? v : parseInt(v, 10); break
      case 'mtproxy': case 'fallback': next[k] = v === true; break
      default: next[k] = String(v ?? '')
    }
  }
  // Another address: the probe, the chosen key and its confirmation belonged to the old one.
  const a = addressOf(s)
  const b = addressOf(next)
  if (a.ip !== b.ip || a.port !== b.port) {
    Object.assign(next, { probe: null, fingerprint: '', fingerprintConfirmed: false, pasted: '' })
  }
  return next
}

function applyProbe(s, { ip, port, result }) {
  const a = addressOf(s)
  if (s.serverId || ip !== a.ip || port !== a.port) return s // a late answer for an address since changed
  const valid = (fp) => /^SHA256:[A-Za-z0-9+/]{43}$/.test(fp || '')
  let keys = (result?.keys || []).filter((k) => valid(k?.fingerprint)).map((k) => ({ type: k.type || '', fingerprint: k.fingerprint }))
  if (keys.length === 0 && valid(result?.fingerprint)) keys = [{ type: result.key_type || '', fingerprint: result.fingerprint }]
  const preferred = keys.find((k) => k.fingerprint === result?.fingerprint) || keys[0]
  return { ...s, probe: { ip, port, keys }, fingerprint: preferred?.fingerprint || '', fingerprintConfirmed: false, pasted: '' }
}

export function wizardReducer(s, action) {
  switch (action?.type) {
    case 'set':
      return applySet(s, action.patch)
    case 'probed':
      return applyProbe(s, action)
    case 'passwordFlags':
      if (s.serverId) return s
      return { ...s, passwordEntered: action.entered === true, passwordValid: action.entered === true && action.valid === true }
    case 'selectKey':
      if (s.serverId || !hasKey(s, action.fingerprint) || action.fingerprint === s.fingerprint) return s
      return { ...s, fingerprint: action.fingerprint, fingerprintConfirmed: false }
    case 'paste': {
      // Selects the matching key only: a fingerprint copied from the wizard itself and pasted back
      // must not confirm anything. A confirmation survives only if the paste keeps the same key.
      if (s.serverId || !s.probe) return s
      const text = String(action.text ?? '')
      if (!text.trim()) return { ...s, pasted: '' }
      const k = matchPasted(text, s.probe.keys)
      if (!k) return { ...s, pasted: text, fingerprintConfirmed: false }
      return { ...s, pasted: text, fingerprint: k.fingerprint, fingerprintConfirmed: k.fingerprint === s.fingerprint && s.fingerprintConfirmed }
    }
    case 'confirmKey':
      if (s.serverId) return s
      if (action.value === true && !canConfirmKey(s)) return s
      return { ...s, fingerprintConfirmed: action.value === true }
    case 'created':
      if (s.serverId || !(Number.isInteger(action.id) && action.id > 0)) return s
      return { ...s, serverId: action.id, passwordEntered: false, passwordValid: false, preflight: null, step: 2 }
    case 'preflightStarted':
      return { ...s, preflight: null }
    case 'preflight':
      return { ...s, preflight: { ok: action.result?.ok === true, checks: Array.isArray(action.result?.checks) ? action.result.checks : [] } }
    case 'plans': {
      const plans = normalizePlans(action.plans)
      return { ...s, plans, plansState: 'ready', plansError: '', planIds: s.planIds ?? defaultPlanIds(plans) }
    }
    case 'plansFailed':
      return { ...s, plansState: 'failed', plansError: String(action.error || '') }
    case 'togglePlan': {
      const ids = (s.planIds || []).filter((x) => x !== action.id)
      return { ...s, planIds: action.on ? [...ids, action.id] : ids }
    }
    case 'fallbackStored':
      return { ...s, fallbackStored: action.value === true }
    case 'started':
      if (!s.serverId || action.jobId == null) return s
      return { ...s, jobId: action.jobId, step: INSTALL_STEP }
    case 'go': {
      if (!canGo(s, action.step)) return s
      // Off the access step before saving: its password field unmounts empty — so is the flag.
      const leavesPassword = s.step === 1 && !s.serverId
      return leavesPassword ? { ...s, step: action.step, passwordEntered: false, passwordValid: false } : { ...s, step: action.step }
    }
    default:
      return s
  }
}

// ── requests (internal/admin/provision_handlers.go) ──

// probeRequest: handshake-only key scan — the password is never part of it.
export function probeRequest(s) {
  const { ip, port } = addressOf(s)
  return ip && port ? { ip_address: ip, ssh_port: port } : null
}

// createRequest: password — read from its (uncontrolled) field at the moment of saving.
export function createRequest(s, password) {
  const { ip, port } = addressOf(s)
  return {
    name: String(s.name ?? '').trim(), role: s.role, country_code: String(s.country ?? '').trim().toUpperCase(),
    ip_address: ip, ssh_port: port, ssh_user: SSH_USER, ssh_password: String(password ?? ''), ssh_host_key_fp: s.fingerprint,
  }
}

// provisionRequest: plan_ids only when the plans were read — without them (no settings.read) the
// node keeps no bindings (common pool) instead of being sent an empty list by mistake.
export function provisionRequest(s) {
  // An entry has no plans, MTProxy or priority (the server refuses them): only its tunnel choice.
  if (s.role === 'relay') return { relay_tunnel: !!s.tunnel }
  const out = { mtproxy: s.mtproxy, load_weight: s.loadWeight }
  if (s.plansState === 'ready') out.plan_ids = s.planIds || []
  return out
}

// routingRequest: the TCP fallback switch is not a provision option — PUT /servers/{id}/routing
// stores it (the recipe reads it when the job loads the node). Only the switch (every routing
// field is optional; plans and priority go with provisionRequest) and only when it differs from
// what is stored; null otherwise.
export function routingRequest(s) {
  if (s.role === 'relay' || s.fallback === s.fallbackStored) return null
  return { tunnel_fallback_enabled: s.fallback }
}

// closeNeedsConfirm: closing would lose a typed password, a probe or leave a saved node half way;
// never during the install (it goes on without the window).
export function closeNeedsConfirm(s) {
  return s.step !== INSTALL_STEP && (s.serverId != null || !!s.probe || s.passwordEntered === true)
}
