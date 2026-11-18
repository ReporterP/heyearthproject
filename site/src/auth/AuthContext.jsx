import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { UNAUTHORIZED_EVENT } from '../api/http.js'
import { isMiniApp } from '../lib/telegram.js'

// The Mini App signs in from Telegram on every launch (spec 12 §12.1) and keeps its session in
// sessionStorage, apart from the site's; the site keeps it in localStorage.
function store() {
  try { return isMiniApp() ? sessionStorage : localStorage } catch { return null }
}
function readToken() { try { return store()?.getItem('token') || '' } catch { return '' } }

const AuthContext = createContext()

export const SESSION_EXPIRED_KEY = 'he_session_expired'

export function AuthProvider({ children }) {
  const [token, setToken] = useState(readToken)

  const login = useCallback((newToken) => {
    // a registration left on its «ID выдан» step is over once someone signs in (RegisterPage)
    try { sessionStorage.removeItem('he_pending_registration') } catch { /* storage off */ }
    try { store()?.setItem('token', newToken) } catch { /* storage off */ }
    setToken(newToken)
  }, [])

  // logout forgets the token here and closes its session on the server (the app and other
  // devices keep theirs). Fire-and-forget: leaving must work offline too.
  const logout = useCallback(() => {
    const old = readToken()
    if (old) {
      fetch('/api/v1/auth/logout', { method: 'POST', headers: { Authorization: `Bearer ${old}` }, keepalive: true }).catch(() => {})
    }
    try { store()?.removeItem('token') } catch { /* storage off */ }
    setToken('')
  }, [])

  // A voluntary logout first goes home (`state.signOut`) and signs out there: signing out on a
  // cabinet page let the auth guard win the race against the navigation and land on
  // /login?redirect=<page> as if the session had expired (spec 08: «Выйти» → `/`). The state is
  // cleared at once, so Back/Forward to this entry never signs anyone out again.
  const location = useLocation()
  const navigate = useNavigate()
  useEffect(() => {
    if (!location.state?.signOut) return
    logout()
    navigate(location.pathname + location.search, { replace: true, state: null })
  }, [location, logout, navigate])

  // Any API call that got 401 (apiFetch) signs the site out; RequireAuth then sends
  // the client to /login?redirect=<where they were>. A 503 (database hiccup) does not.
  useEffect(() => {
    // remember why the person lands on /login: the login page says «Сессия закончилась» (03 §6)
    const onUnauthorized = () => {
      try { sessionStorage.setItem(SESSION_EXPIRED_KEY, '1') } catch { /* storage off */ }
      logout()
    }
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
