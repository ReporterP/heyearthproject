import { createContext, useContext, useState, useCallback, useEffect } from 'react'

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

  // Validate token on mount
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
