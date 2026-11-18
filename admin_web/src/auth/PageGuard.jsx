import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { ShieldOff } from 'lucide-react'
import { useAuth } from './AuthContext.jsx'
import { pageGate } from './access.js'
import { EmptyState } from '../components/ui/EmptyState.jsx'
import { Button } from '../components/ui/Button.jsx'

// PageGuard refuses a page the admin's role may not open instead of rendering it: the page
// would ask the API, get 403 and show «нет данных» as if the list were empty (QA 2026-11-18).
export default function PageGuard() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { can, isTenantAdmin } = useAuth()
  if (pageGate(pathname, { can, isTenantAdmin }) === 'open') return <Outlet />
  return (
    <EmptyState
      icon={<ShieldOff size={28} strokeWidth={1.5} />}
      title="Нет доступа"
      description="У вашей роли нет права на этот раздел. Если он нужен для работы — попросите владельца добавить право."
      action={<Button variant="secondary" size="sm" onClick={() => navigate('/dashboard')}>На дашборд</Button>}
    />
  )
}
