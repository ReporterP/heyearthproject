export default function StatCard({ label, value, sub }) {
  return (
    <div className="bg-surface-card rounded-xl border border-white/10 p-5">
      <p className="text-sm text-text-secondary mb-1">{label}</p>
      <p className="text-3xl font-bold">{value ?? '\u2014'}</p>
      {sub != null && <p className="text-sm text-brand mt-1">{sub}</p>}
    </div>
  )
}
