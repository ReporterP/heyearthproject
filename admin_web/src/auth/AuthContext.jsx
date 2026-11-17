import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { setLogoutCallback, apiFetch } from '../api/client.js'
import { withLivePermissions } from './liveSession.js'

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

  // On mount: check the token (apiFetch logs out on 401 by itself) and take the rights the server
  // has now — the stored login answer keeps the rights of the moment of login (a permission added
  // by a deploy stayed hidden until a new login). A network error or an older API without /me
  // keeps the session as it is.
  useEffect(() => {
    if (!token) return
    apiFetch('/me').then((live) => {
      setSession((cur) => {
        const next = withLivePermissions(cur, live)
        if (next !== cur) {
          try { localStorage.setItem('admin_login_result', JSON.stringify(next)) } catch {}
        }
        return next
      })
    }).catch(() => {})
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
