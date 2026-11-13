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
      <p className="text-sm text-secondary mb-1">{label}</p>
      <p className="text-3xl font-bold">{value ?? '\u2014'}</p>
      {sub != null && (
        <p className={`text-sm mt-1 ${to ? 'text-accent' : 'text-secondary/70'}`}>{sub}</p>
      )}
    </>
  )
  if (to) {
    return (
      <Link
        to={to}
        className="bg-elevated rounded-lg border border-default p-5 block transition hover:border-accent/40 hover:bg-elevated/80"
      >{body}</Link>
    )
  }
  return (
    <div className="bg-elevated rounded-lg border border-default p-5">{body}</div>
  )
}
