import { forwardRef } from 'react'
import { Spinner } from './Spinner'

const variants = {
  primary:   'bg-accent text-white hover:bg-accent-hover border-transparent',
  secondary: 'bg-elevated text-primary hover:bg-hover border-default',
  ghost:     'bg-transparent text-secondary hover:bg-hover hover:text-primary border-transparent',
  danger:    'bg-danger text-white hover:opacity-90 border-transparent',
}
const sizes = { sm: 'h-7 px-2.5 text-xs', md: 'h-8 px-3 text-sm', lg: 'h-9 px-4 text-sm' }

export const Button = forwardRef(function Button(
  { variant = 'primary', size = 'md', loading = false, icon, disabled, className = '', children, ...props }, ref
) {
  return (
    <button ref={ref}
      disabled={disabled || loading}
      className={`inline-flex items-center justify-center gap-1.5 rounded-[var(--radius-default,0.25rem)] [border-width:var(--border-width-hairline,1px)] border-solid font-medium
        transition-colors disabled:opacity-40 disabled:cursor-not-allowed
        focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent
        ${variants[variant]} ${sizes[size]} ${className}`}
      {...props}>
      {loading && <Spinner size="xs" />}
      {!loading && icon}
      {children}
    </button>
  )
})
