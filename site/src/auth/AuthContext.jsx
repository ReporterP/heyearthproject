import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { UNAUTHORIZED_EVENT } from '../api/http.js'

const AuthContext = createContext()

export function AuthProvider({ children }) {
  const [token, setToken] = useState(() => localStorage.getItem('token') || '')

  const login = useCallback((newToken) => {
    localStorage.setItem('token', newToken)
    setToken(newToken)
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem('token')
    setToken('')
  }, [])

  // Any API call that got 401 (apiFetch) signs the site out; RequireAuth then sends
  // the client to /login?redirect=<where they were>. A 503 (database hiccup) does not.
  useEffect(() => {
    const onUnauthorized = () => logout()
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [logout])

  // Validate the stored token on mount: only a 401 signs out.
  useEffect(() => {
    if (!token) return
    fetch('/api/v1/user/profile', {
      headers: { 'Authorization': `Bearer ${token}` },
    }).then(res => {
      if (res.status === 401) logout()
    }).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <AuthContext.Provider value={{ token, isLoggedIn: !!token, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  return useContext(AuthContext)
}
