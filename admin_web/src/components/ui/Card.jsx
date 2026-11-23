export function Card({ className = '', children, ...props }) {
  return <div className={`rounded-[var(--radius-default,0.25rem)] [border-width:var(--border-width-hairline,1px)] border-solid border-default bg-elevated ${className}`} {...props}>{children}</div>
}

export function CardHeader({ className = '', children }) {
  return <div className={`px-4 py-3 border-b border-default ${className}`}>{children}</div>
}

export function CardBody({ className = '', children }) {
  return <div className={`p-4 ${className}`}>{children}</div>
}
