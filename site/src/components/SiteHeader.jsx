import { useState, useEffect } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useSiteInfo from '../hooks/useSiteInfo.js'

const LeafMark = ({ className = 'w-5 h-5' }) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 21c-4.5-1-8.5-4.5-8.5-10.5C9 10.5 12 13.5 12 17M12 21c4.5-1 8.5-4.5 8.5-10.5C15 10.5 12 13.5 12 17M12 21V8" />
  </svg>
)

export default function SiteHeader() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)
  const { isLoggedIn } = useAuth()
  const location = useLocation()

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => { setMobileOpen(false) }, [location.pathname])

  const { info } = useSiteInfo()
  const navLinks = [
    { to: '/', label: 'Для пользователей' },
    ...(info.b2b_enabled ? [{ to: '/business', label: 'Для бизнеса' }] : []),
    { to: '/help', label: 'Помощь' },
  ]

  return (
    <header
      className={`sticky top-0 z-50 transition-all duration-500 ${
        scrolled ? 'glass-strong' : 'bg-transparent'
      }`}
    >
      <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
        {/* Logo */}
        <Link to="/" className="group flex items-center gap-2.5">
          <span className="relative w-9 h-9 rounded-xl bg-brand/12 border border-brand/25 text-brand flex items-center justify-center transition-colors duration-300 group-hover:bg-brand/18">
            <LeafMark className="w-5 h-5" />
          </span>
          <span className="serif text-xl font-semibold tracking-tight text-text-primary">HiEarth</span>
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
                  isActive ? 'text-text-primary' : 'text-text-secondary hover:text-text-primary'
                }`}
              >
                {link.label}
                {isActive && <span className="absolute bottom-0.5 left-1/2 -translate-x-1/2 w-4 h-0.5 rounded-full bg-brand" />}
              </Link>
            )
          })}
        </nav>

        {/* Desktop CTA */}
        <div className="hidden md:flex items-center gap-3">
          <Link to={isLoggedIn ? '/dashboard' : '/login'} className="btn-primary !py-2 !px-5 !text-sm">
            {isLoggedIn ? 'Кабинет' : 'Войти'}
          </Link>
        </div>

        {/* Mobile burger */}
        <button
          className="md:hidden relative w-10 h-10 flex items-center justify-center rounded-lg hover:bg-white/5 transition-colors"
          onClick={() => setMobileOpen(!mobileOpen)}
          aria-label="Меню"
          aria-expanded={mobileOpen}
        >
          <div className="w-5 h-4 relative flex flex-col justify-between">
            <span className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 origin-center ${mobileOpen ? 'rotate-45 translate-y-[7px]' : ''}`} />
            <span className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 ${mobileOpen ? 'opacity-0 scale-x-0' : ''}`} />
            <span className={`w-full h-0.5 bg-text-primary rounded-full transition-all duration-300 origin-center ${mobileOpen ? '-rotate-45 -translate-y-[7px]' : ''}`} />
          </div>
        </button>
      </div>

      {/* Mobile menu */}
      <div className={`md:hidden overflow-hidden transition-all duration-300 ease-in-out ${mobileOpen ? 'max-h-64 opacity-100' : 'max-h-0 opacity-0'}`}>
        <div className="glass-strong border-t border-brand/10 px-6 py-4 space-y-1">
          {navLinks.map(link => {
            const isActive = location.pathname === link.to
            return (
              <Link
                key={link.to}
                to={link.to}
                className={`block px-4 py-3 rounded-xl text-sm font-medium transition-all duration-200 ${
                  isActive ? 'bg-brand/10 text-brand' : 'text-text-secondary hover:text-text-primary hover:bg-white/5'
                }`}
                onClick={() => setMobileOpen(false)}
              >
                {link.label}
              </Link>
            )
          })}
          <div className="pt-2">
            <Link
              to={isLoggedIn ? '/dashboard' : '/login'}
              className="btn-primary w-full !py-3"
              onClick={() => setMobileOpen(false)}
            >
              {isLoggedIn ? 'Кабинет' : 'Войти'}
            </Link>
          </div>
        </div>
      </div>
    </header>
  )
}
