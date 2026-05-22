import React from 'react'

const palette = {
  pending:        { bg: 'bg-gray-500/15',    fg: 'text-gray-300',    label: 'Pending'         },
  provisioning:   { bg: 'bg-blue-500/15',    fg: 'text-blue-300',    label: 'Provisioning…'   },
  online:         { bg: 'bg-emerald-500/15', fg: 'text-emerald-300', label: 'Online'          },
  failed:         { bg: 'bg-red-500/15',     fg: 'text-red-300',     label: 'Failed'          },
  deprovisioning: { bg: 'bg-amber-500/15',   fg: 'text-amber-300',   label: 'Deprovisioning…' },
  offline:        { bg: 'bg-slate-500/15',   fg: 'text-slate-300',   label: 'Offline'         },
}

export default function ServerLifecycleBadge({ state }) {
  const p = palette[state] || palette.pending
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium ${p.bg} ${p.fg}`}>
      <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
      {p.label}
    </span>
  )
}
