import { Link } from 'react-router-dom'

/**
 * Dashboard statistic card.
 *
 * Props:
 *   label       — top label
 *   value       — the primary number / text
 *   sub         — optional sub-text shown below the value
 *   to          — optional route; when present the entire card becomes a Link
 *                 and `sub` stays in brand-blue to signal it's clickable.
 *                 When absent, `sub` is muted so it doesn't masquerade as a link.
 */
export default function StatCard({ label, value, sub, to }) {
  const body = (
    <>
      <p className="text-sm text-text-secondary mb-1">{label}</p>
      <p className="text-3xl font-bold">{value ?? '\u2014'}</p>
      {sub != null && (
        <p className={`text-sm mt-1 ${to ? 'text-brand' : 'text-text-secondary/70'}`}>{sub}</p>
      )}
    </>
  )
  if (to) {
    return (
      <Link
        to={to}
        className="bg-surface-card rounded-xl border border-white/10 p-5 block transition hover:border-brand/40 hover:bg-surface-card/80"
      >{body}</Link>
    )
  }
  return (
    <div className="bg-surface-card rounded-xl border border-white/10 p-5">{body}</div>
  )
}
