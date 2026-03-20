import { useState } from 'react'
import { Link } from 'react-router-dom'

export default function SiteHeader() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const isLoggedIn = !!localStorage.getItem('token')

  return (
    <header className="sticky top-0 z-50 bg-surface/80 backdrop-blur-xl border-b border-white/5">
      <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
        <Link to="/" className="text-xl font-bold tracking-tight">
          <span className="text-brand-light">Hey</span><span>Earth</span>
        </Link>

        <nav className="hidden md:flex items-center gap-8 text-sm text-text-secondary">
          <Link to="/" className="hover:text-text-primary transition">Для пользователей</Link>
          <Link to="/business" className="hover:text-text-primary transition">Для бизнеса</Link>
        </nav>

        <div className="hidden md:flex items-center gap-3">
          {isLoggedIn ? (
            <Link to="/dashboard" className="px-4 py-2 rounded-lg bg-brand hover:bg-brand-dark text-white text-sm font-medium transition">
              Кабинет
            </Link>
          ) : (
            <Link to="/login" className="px-4 py-2 rounded-lg bg-brand hover:bg-brand-dark text-white text-sm font-medium transition">
              Войти
            </Link>
          )}
        </div>

        <button
          className="md:hidden p-2 text-text-secondary hover:text-text-primary"
          onClick={() => setMobileOpen(!mobileOpen)}
        >
          <svg xmlns="http://www.w3.org/2000/svg" className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            {!mobileOpen ? (
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            ) : (
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            )}
          </svg>
        </button>
      </div>

      {mobileOpen && (
        <div className="md:hidden border-t border-white/5 bg-surface px-6 py-4 space-y-3">
          <Link to="/" className="block text-text-secondary hover:text-text-primary" onClick={() => setMobileOpen(false)}>Для пользователей</Link>
          <Link to="/business" className="block text-text-secondary hover:text-text-primary" onClick={() => setMobileOpen(false)}>Для бизнеса</Link>
          {isLoggedIn ? (
            <Link to="/dashboard" className="block px-4 py-2 rounded-lg bg-brand text-white text-center text-sm font-medium" onClick={() => setMobileOpen(false)}>
              Кабинет
            </Link>
          ) : (
            <Link to="/login" className="block px-4 py-2 rounded-lg bg-brand text-white text-center text-sm font-medium" onClick={() => setMobileOpen(false)}>
              Войти
            </Link>
          )}
        </div>
      )}
    </header>
  )
}
