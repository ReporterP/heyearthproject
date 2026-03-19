import { Navigate, Outlet } from 'react-router-dom'
import { useAuth } from './AuthContext.jsx'

export default function RequireAuth() {
  const { isLoggedIn } = useAuth()
  if (!isLoggedIn) return <Navigate to="/login" replace />
  return <Outlet />
}
