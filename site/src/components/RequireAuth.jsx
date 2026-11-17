import { useEffect } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { isMiniApp } from '../lib/telegram.js'

// useNoIndex keeps search engines out of a page while it is mounted (cleanup plan E5: the
// landing and the documents are indexed, the cabinet and personal links are not).
export function useNoIndex() {
  useEffect(() => {
    const meta = document.createElement('meta')
    meta.name = 'robots'
    meta.content = 'noindex, nofollow'
    document.head.appendChild(meta)
    return () => meta.remove()
  }, [])
}

export default function RequireAuth({ children }) {
  const location = useLocation()
  const { isLoggedIn } = useAuth()
  useNoIndex()
  if (!isLoggedIn && isMiniApp()) {
    // inside Telegram a lost session signs in again from Telegram (spec 12 §12.1)
    return <Navigate to={`/tg?p=${encodeURIComponent(location.pathname.slice(1))}`} replace />
  }
  if (!isLoggedIn) {
    return <Navigate to={`/login?redirect=${encodeURIComponent(location.pathname)}`} replace />
  }
  return children
}
