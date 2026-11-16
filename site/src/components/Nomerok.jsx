import { useToast } from './Toast.jsx'
import { copyText } from '../lib/clipboard.js'

// HiEarth ID as a cloakroom tag («номерок», spec 02 §34.3).
//   variant 'swing' — drops onto the hook and swings once (ONLY on «Аккаунт создан»),
//           'idle'  — gentle sway (landing, login, settings),
//           'chip'  — compact capsule in the cabinet header / side menu; a click copies the full ID.
// Copying the ID is otherwise a separate button next to the tag.
// The real HiEarth ID is the account's UUID («3f2b8c1e-7a4d-4e9b-8c21-5d6e7f8a9b0c»); it is shown and
// copied exactly as issued (login matches it verbatim). Older all-digit IDs are grouped by 4.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
export const groupId = (id) => {
  const raw = String(id ?? '').replace(/\s+/g, '')
  return /^\d+$/.test(raw) ? raw.replace(/(.{4})(?=.)/g, '$1 ') : raw
}
// two lines for the tag
export const idLines = (id) => {
  const raw = String(id ?? '').replace(/\s+/g, '')
  if (UUID.test(raw)) return [raw.slice(0, 18), raw.slice(19)]
  const g = groupId(raw)
  if (/^\d+$/.test(raw)) { const p = g.split(' '); return [p.slice(0, 2).join(' '), p.slice(2).join(' ')] }
  const h = Math.ceil(raw.length / 2)
  return [raw.slice(0, h), raw.slice(h)]
}
export const shortId = (id, phone = false) => {
  const raw = String(id ?? '').replace(/[\s-]+/g, '')
  if (raw.length <= 8) return raw
  return phone ? `…${raw.slice(-4)}` : `${raw.slice(0, 4)}…${raw.slice(-4)}`
}
// normalise what a person types or pastes into «HiEarth ID или имя пользователя»
export const normalizeLogin = (v) => {
  const s = String(v ?? '').trim()
  const compact = s.replace(/\s+/g, '')
  return UUID.test(compact) ? compact.toLowerCase() : s
}

export function IdChip({ id, phone = false, className = '' }) {
  const toast = useToast()
  const raw = String(id ?? '').replace(/\s+/g, '')
  if (!raw) return null
  async function copy() {
    const ok = await copyText(raw)
    if (ok) toast.ok('HiEarth ID скопирован')
    else toast.bad('Не удалось скопировать — выделите ID в настройках')
  }
  return (
    <button
      type="button"
      className={`tag tag--chip ${className}`}
      title="Скопировать HiEarth ID"
      aria-label={`HiEarth ID ${groupId(raw)} — скопировать`}
      onClick={copy}
      style={{ border: 0, cursor: 'pointer' }}
    >
      {shortId(raw, phone)}
    </button>
  )
}

export default function Nomerok({ id, variant = 'idle', label = 'ваш номерок', withRail = true, masked = false, railWidth = 300 }) {
  const raw = String(id ?? '').replace(/\s+/g, '')
  const lines = masked ? ['•••• ••••'] : idLines(raw)
  const tag = (
    <div
      className={`tag tag--${variant}`}
      style={withRail ? { marginTop: 22 } : undefined}
      role="img"
      aria-label={masked ? 'Номерок HiEarth ID' : `HiEarth ID ${groupId(raw)}`}
    >
      <span className="tag__hook" aria-hidden="true" />
      <span className="tag__k" aria-hidden="true">{label}</span>
      <span className="tag__id" aria-hidden="true">
        {lines[0]}
        {lines[1] ? <><br />{lines[1]}</> : null}
      </span>
    </div>
  )
  if (!withRail) return tag
  return (
    <div style={{ width: railWidth, maxWidth: '100%' }}>
      <div className="rail" aria-hidden="true" />
      <div style={{ display: 'flex', justifyContent: 'center' }}>{tag}</div>
    </div>
  )
}
