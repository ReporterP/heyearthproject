const sizes = { xs: 'h-3 w-3', sm: 'h-4 w-4', md: 'h-5 w-5' }

export function Spinner({ size = 'md', className = '' }) {
  return (
    <svg className={`animate-spin text-current ${sizes[size]} ${className}`} viewBox="0 0 24 24" fill="none">
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.25" strokeWidth="4"/>
      <path d="M22 12a10 10 0 0 1-10 10" stroke="currentColor" strokeWidth="4" strokeLinecap="round"/>
    </svg>
  )
}
