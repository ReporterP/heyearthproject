const colors = {
  active: 'bg-green-500/20 text-green-400',
  healthy: 'bg-green-500/20 text-green-400',
  online: 'bg-green-500/20 text-green-400',
  paid: 'bg-green-500/20 text-green-400',
  succeeded: 'bg-green-500/20 text-green-400',
  inactive: 'bg-gray-500/20 text-gray-400',
  offline: 'bg-gray-500/20 text-gray-400',
  blocked: 'bg-red-500/20 text-red-400',
  unhealthy: 'bg-red-500/20 text-red-400',
  failed: 'bg-red-500/20 text-red-400',
  pending: 'bg-yellow-500/20 text-yellow-400',
  configuring: 'bg-yellow-500/20 text-yellow-400',
  waiting_for_capture: 'bg-yellow-500/20 text-yellow-400',
}

export default function StatusBadge({ status }) {
  const key = (status || '').toLowerCase().replace(/\s+/g, '_')
  const cls = colors[key] || 'bg-gray-500/20 text-gray-400'
  return (
    <span className={`inline-block px-2 py-0.5 rounded-full text-xs font-medium ${cls}`}>
      {status}
    </span>
  )
}
