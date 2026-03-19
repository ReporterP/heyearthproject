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

  return (
    <AuthContext.Provider value={{ token, admin, permissions, can, isTenantAdmin, login: loginAction, logout: logoutAction, isLoggedIn: !!token }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}
