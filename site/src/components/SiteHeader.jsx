import { useState, useEffect } from 'react'
import { Link, useLocation } from 'react-router-dom'

export default function SiteHeader() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)
  const isLoggedIn = !!localStorage.getItem('token')
  const location = useLocation()

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    setMobileOpen(false)
  }, [location.pathname])

  const navLinks = [
    { to: '/', label: 'Для пользователей' },
    { to: '/business', label: 'Для бизнеса' },
  ]

  return (
    <header
      className={`sticky top-0 z-50 transition-all duration-500 ${
        scrolled
          ? 'glass-strong shadow-lg shadow-black/10'
          : 'bg-transparent'
      }`}
    >
      <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
        {/* Logo */}
        <Link to="/" className="group flex items-center gap-2">
          <div className="relative w-8 h-8 rounded-lg bg-gradient-to-br from-brand-light to-accent flex items-center justify-center overflow-hidden">
            <span className="text-sm font-black text-white relative z-10">H</span>
            <div className="absolute inset-0 bg-gradient-to-br from-brand to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-300" />
          </div>
          <span className="text-xl font-bold tracking-tight">
            <span className="text-brand-light group-hover:text-accent transition-colors duration-300">Hey</span>
            <span className="text-text-primary">Earth</span>
          </span>
        </Link>

        {/* Desktop nav */}
        <nav className="hidden md:flex items-center gap-1">
          {navLinks.map(link => {
            const isActive = location.pathname === link.to
            return (
              <Link
                key={link.to}
                to={link.to}
                className={`relative px-4 py-2 rounded-lg text-sm font-medium transition-all duration-300 ${
                  isActive
                    ? 'text-text-primary'
                    : 'text-text-secondary hover:text-text-primary'
                }`}
              >
                {link.label}
                {isActive && (
                  <span className="absolute bottom-0.5 left-1/2 -translate-x-1/2 w-4 h-0.5 rounded-full bg-brand-light" />
                )}
              </Link>
            )
          })}
        </nav>

        {/* Desktop CTA */}
        <div className="hidden md:flex items-center gap-3">
          {isLoggedIn ? (
            <Link
              to="/dashboard"
              className="group relative px-5 py-2 rounded-xl font-medium text-sm text-white overflow-hidden transition-all duration-300"
            >
              <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark transition-all duration-300 group-hover:from-brand-dark group-hover:to-brand" />
              <span className="absolute inset-0 opacity-0 group-hover:opacity-100 transition-opacity duration-300 bg-gradient-to-r from-brand-light/20 to-accent/20" />
              <span className="relative z-10">Кабинет</span>
            </Link>
          ) : (
            <Link
              to="/login"
              className="group relative px-5 py-2 rounded-xl font-medium text-sm text-white overflow-hidden transition-all duration-300"
            >
              <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark transition-all duration-300 group-hover:from-brand-dark group-hover:to-brand" />
              <span className="absolute inset-0 opacity-0 group-hover:opacity-100 transition-opacity duration-300 bg-gradient-to-r from-brand-light/20 to-accent/20" />
              <span className="relative z-10">Войти</span>
            </Link>
          )}
        </div>

        {/* Mobile burger */}
        <button
          className="md:hidden relative w-10 h-10 flex items-center justify-center rounded-lg hover:bg-white/5 transition-colors"
          onClick={() => setMobileOpen(!mobileOpen)}
          aria-label="Меню"
        >
          <div className="w-5 h-4 relative flex flex-col justify-between">
            <span
              className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 origin-center ${
                mobileOpen ? 'rotate-45 translate-y-[7px]' : ''
              }`}
            />
            <span
              className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 ${
                mobileOpen ? 'opacity-0 scale-x-0' : ''
              }`}
            />
            <span
              className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 origin-center ${
                mobileOpen ? '-rotate-45 -translate-y-[7px]' : ''
              }`}
            />
          </div>
        </button>
      </div>

      {/* Mobile menu */}
      <div
        className={`md:hidden overflow-hidden transition-all duration-300 ease-in-out ${
          mobileOpen ? 'max-h-64 opacity-100' : 'max-h-0 opacity-0'
        }`}
      >
        <div className="glass-strong border-t border-white/5 px-6 py-4 space-y-1">
          {navLinks.map(link => {
            const isActive = location.pathname === link.to
            return (
              <Link
                key={link.to}
                to={link.to}
                className={`block px-4 py-3 rounded-xl text-sm font-medium transition-all duration-200 ${
                  isActive
                    ? 'bg-brand/10 text-brand-light'
                    : 'text-text-secondary hover:text-text-primary hover:bg-white/5'
                }`}
                onClick={() => setMobileOpen(false)}
              >
                {link.label}
              </Link>
            )
          })}
          <div className="pt-2">
            {isLoggedIn ? (
              <Link
                to="/dashboard"
                className="block px-4 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-center text-sm font-semibold"
                onClick={() => setMobileOpen(false)}
              >
                Кабинет
              </Link>
            ) : (
              <Link
                to="/login"
                className="block px-4 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-center text-sm font-semibold"
                onClick={() => setMobileOpen(false)}
              >
                Войти
              </Link>
            )}
          </div>
        </div>
      </div>
    </header>
  )
}
