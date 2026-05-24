function SortIcon({ direction }) {
  if (direction === 'asc')  return <span className="text-accent">▲</span>
  if (direction === 'desc') return <span className="text-accent">▼</span>
  return <span className="text-tertiary opacity-40">⇅</span>
}

export function Table({ children, className = '' }) {
  return (
    <div className={`rounded border border-default overflow-x-auto ${className}`}>
      <table className="w-full text-sm">{children}</table>
    </div>
  )
}

export function Thead({ children }) {
  return <thead className="bg-elevated text-tertiary text-[11px] uppercase tracking-wider">{children}</thead>
}

export function Tbody({ children }) { return <tbody>{children}</tbody> }

export function Tr({ children, className = '', ...props }) {
  return (
    <tr className={`hover:bg-hover border-b border-default/60 last:border-b-0 transition-colors ${className}`} {...props}>
      {children}
    </tr>
  )
}

export function Th({ sortKey, sortState, onSort, align = 'left', children, className = '' }) {
  const sortable = !!(sortKey && onSort)
  const active = !!(sortState && sortState.column === sortKey)
  const direction = active ? (sortState?.direction ?? null) : null
  const handleClick = () => sortable && onSort(sortKey)
  const alignCls = align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left'
  return (
    <th onClick={handleClick}
        className={`h-9 px-3 font-medium ${alignCls} ${sortable ? 'cursor-pointer hover:text-primary select-none' : ''} ${className}`}>
      <span className={`inline-flex items-center gap-1 ${align === 'right' ? 'justify-end' : ''}`}>
        {children}
        {sortable && <SortIcon direction={direction} />}
      </span>
    </th>
  )
}

export function Td({ children, align = 'left', mono = false, className = '' }) {
  const alignCls = align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left'
  const monoCls = mono ? 'font-mono tabular-nums' : ''
  return <td className={`h-9 px-3 align-middle ${alignCls} ${monoCls} ${className}`}>{children}</td>
}
