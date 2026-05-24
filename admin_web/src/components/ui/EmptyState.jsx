export function EmptyState({ icon, title, description, action, className = '' }) {
  return (
    <div className={`flex flex-col items-center justify-center text-center py-16 px-4 ${className}`}>
      {icon && <div className="mb-3 text-tertiary">{icon}</div>}
      <div className="text-lg font-medium text-primary mb-1">{title}</div>
      {description && <div className="text-sm text-secondary max-w-md mb-4">{description}</div>}
      {action}
    </div>
  )
}
