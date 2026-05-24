const variants = {
  default:  'bg-elevated text-secondary border-default',
  accent:   'bg-accent/15 text-accent border-accent/30',
  success:  'bg-success/15 text-success border-success/30',
  warning:  'bg-warning/15 text-warning border-warning/30',
  danger:   'bg-danger/15 text-danger border-danger/30',
  info:     'bg-info/15 text-info border-info/30',
}

export function Badge({ variant = 'default', children, className = '' }) {
  return (
    <span className={`inline-flex items-center gap-1 px-2 h-5 rounded-[var(--radius-default,0.25rem)] border text-[11px] font-medium leading-none ${variants[variant]} ${className}`}>
      {children}
    </span>
  )
}
