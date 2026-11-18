import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { ShieldOff } from 'lucide-react'
import { useAuth } from './AuthContext.jsx'
import { canOpen, pageGate } from './access.js'
import { EmptyState } from '../components/ui/EmptyState.jsx'
import { Button } from '../components/ui/Button.jsx'

// PageGuard refuses a page the admin's role may not open instead of rendering it: the page
// would ask the API, get 403 and show «нет данных» as if the list were empty (QA 2026-11-18).
export default function PageGuard() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { can, isTenantAdmin } = useAuth()
  const ctx = { can, isTenantAdmin }
  if (pageGate(pathname, ctx) === 'open') return <Outlet />
  // a role without the dashboard lands here after sign-in: send it to a page it may open
  const home = canOpen('/dashboard', ctx) ? { to: '/dashboard', label: 'На дашборд' } : { to: '/settings', label: 'В настройки' }
  return (
    <EmptyState
      icon={<ShieldOff size={28} strokeWidth={1.5} />}
      title="Нет доступа"
      description="У вашей роли нет права на этот раздел. Если он нужен для работы — попросите владельца добавить право."
      action={<Button variant="secondary" size="sm" onClick={() => navigate(home.to)}>{home.label}</Button>}
    />
  )
}
