export function Skeleton({ className = '' }) {
  return <div className={`animate-pulse bg-elevated rounded ${className}`} />
}

export function TableSkeleton({ rows = 10, cols = 5 }) {
  return (
    <div className="rounded border border-default overflow-hidden">
      <div className="bg-elevated h-9 border-b border-default" />
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-9 flex items-center gap-3 px-3 border-b border-default/60 last:border-b-0">
          {Array.from({ length: cols }).map((_, j) => (
            <Skeleton key={j} className="h-3 flex-1" />
          ))}
        </div>
      ))}
    </div>
  )
}
