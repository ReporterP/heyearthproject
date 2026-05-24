export function Kbd({ children, className = '' }) {
  return (
    <kbd className={`inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded border border-default bg-elevated text-[10px] font-mono text-tertiary leading-none ${className}`}>
      {children}
    </kbd>
  )
}
