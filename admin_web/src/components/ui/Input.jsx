import { forwardRef } from 'react'

const baseField = "rounded-[var(--radius-default,0.25rem)] [border-width:var(--border-width-default,1px)] border-solid px-3 text-sm bg-elevated text-primary placeholder:text-tertiary transition-colors focus:outline-none focus:border-strong focus:ring-1 focus:ring-accent/40 disabled:opacity-40 disabled:cursor-not-allowed"

export const Input = forwardRef(function Input({ className = '', error, ...props }, ref) {
  return <input ref={ref} className={`${baseField} h-8 w-full ${error ? 'border-danger' : 'border-default'} ${className}`} {...props} />
})

export const Textarea = forwardRef(function Textarea({ className = '', error, rows = 4, ...props }, ref) {
  return <textarea ref={ref} rows={rows} className={`${baseField} min-h-20 w-full py-1.5 ${error ? 'border-danger' : 'border-default'} ${className}`} {...props} />
})

export const Select = forwardRef(function Select({ className = '', error, children, ...props }, ref) {
  return (
    <select ref={ref} className={`${baseField} h-8 w-full ${error ? 'border-danger' : 'border-default'} ${className}`} {...props}>
      {children}
    </select>
  )
})
