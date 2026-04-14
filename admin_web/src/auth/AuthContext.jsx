import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { setLogoutCallback } from '../api/client.js'

const AuthContext = createContext()

export function AuthProvider({ children }) {
  const [session, setSession] = useState(() => {
    try {
      const stored = localStorage.getItem('admin_login_result')
      if (stored) {
        const parsed = JSON.parse(stored)
        return parsed
      }
    } catch {}
    return null
  })

  const token = session?.token || ''
  const admin = session?.admin || null
  const permissions = session?.permissions || admin?.permissions || []

  const can = useCallback((perm) => permissions.includes(perm), [permissions])
  const isTenantAdmin = admin?.tenant_id != null

  const loginAction = useCallback((result) => {
    localStorage.setItem('admin_token', result.token)
    localStorage.setItem('admin_login_result', JSON.stringify(result))
    setSession(result)
  }, [])

  const logoutAction = useCallback(() => {
    localStorage.removeItem('admin_token')
    localStorage.removeItem('admin_login_result')
    setSession(null)
  }, [])

  useEffect(() => {
    setLogoutCallback(logoutAction)
  }, [logoutAction])

  // Validate token on mount — auto-logout if expired/revoked
  useEffect(() => {
    if (!token) return
    const API_BASE = ''
    fetch(`${API_BASE}/api/admin/overview`, {
      headers: { 'Authorization': `Bearer ${token}` },
    }).then(res => {
      if (res.status === 401) logoutAction()
    }).catch(() => { /* network error — keep session, will fail on next API call */ })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <AuthContext.Provider value={{ token, admin, permissions, can, isTenantAdmin, login: loginAction, logout: logoutAction, isLoggedIn: !!token }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}
