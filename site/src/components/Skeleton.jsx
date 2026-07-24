/**
 * Skeleton primitives — shimmer placeholders that mirror the real layout so
 * the page doesn't jump when data arrives (perceived performance, UX §3/§7).
 * `Skeleton` is the base block; `CardSkeleton` and `CabinetSkeleton` are
 * ready-made layouts for the data pages.
 */
export function Skeleton({ className = '', style }) {
  return <div className={`skeleton ${className}`} style={style} aria-hidden="true" />
}

export function CardSkeleton({ lines = 3, className = '' }) {
  return (
    <div className={`card rounded-[22px] p-6 ${className}`}>
      <div className="flex items-center gap-3 mb-4">
        <Skeleton className="w-11 h-11 !rounded-xl" />
        <div className="flex-1 space-y-2">
          <Skeleton className="h-4 w-1/3" />
          <Skeleton className="h-3 w-1/2" />
        </div>
      </div>
      <div className="space-y-2.5">
        {Array.from({ length: lines }).map((_, i) => (
          <Skeleton key={i} className="h-3.5" style={{ width: `${90 - i * 12}%` }} />
        ))}
      </div>
    </div>
  )
}

/** Full-page cabinet skeleton: header + prominent card + a grid of cards. */
export function CabinetSkeleton() {
  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8">
          <div className="space-y-2">
            <Skeleton className="h-7 w-56" />
            <Skeleton className="h-3.5 w-36" />
          </div>
          <Skeleton className="h-9 w-28 !rounded-full" />
        </div>
        <Skeleton className="h-32 w-full !rounded-[22px] mb-6" />
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6">
          <Skeleton className="h-24 !rounded-[22px]" />
          <Skeleton className="h-24 !rounded-[22px]" />
        </div>
        <CardSkeleton lines={4} />
      </div>
    </section>
  )
}

export default Skeleton
