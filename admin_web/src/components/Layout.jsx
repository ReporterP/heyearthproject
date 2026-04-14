import { useState } from 'react'
import { Outlet } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { useT } from '../i18n/index.jsx'
import Sidebar from './Sidebar.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'

export default function Layout() {
  const { admin, logout } = useAuth()
  const { t, locale, setLocale } = useT()
  const [collapsed, setCollapsed] = useState(false)

  return (
    <div className="min-h-screen flex bg-surface text-text-primary">
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed(c => !c)} />
      <div className="flex-1 flex flex-col min-w-0">
        {/* AppBar */}
        <header className="h-14 border-b border-white/10 bg-surface-card flex items-center justify-between px-4 shrink-0">
          <h1 className="text-lg font-semibold text-brand">{t.appTitle}</h1>
          <div className="flex items-center gap-3">
            <button
              onClick={() => setLocale(locale === 'ru' ? 'en' : 'ru')}
              className="text-xs px-2 py-1 rounded bg-surface-alt text-text-secondary hover:text-text-primary transition"
            >
              {locale === 'ru' ? 'EN' : 'RU'}
            </button>
            <ThemeToggle />
            <span className="text-sm text-text-secondary">{admin?.display_name || admin?.username}</span>
            <button
              onClick={logout}
              className="text-sm text-red-400 hover:text-red-300 transition"
            >
              {t.logout}
            </button>
          </div>
        </header>
        {/* Content */}
        <main className="flex-1 overflow-auto p-6">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>
    </div>
  )
}

function ThemeToggle() {
  const [theme, setTheme] = useState(() => localStorage.getItem('admin_theme_mode') || 'dark')

  function toggle() {
    const next = theme === 'dark' ? 'light' : 'dark'
    setTheme(next)
    localStorage.setItem('admin_theme_mode', next)
    document.documentElement.classList.toggle('light', next === 'light')
  }

  return (
    <button
      onClick={toggle}
      className="text-text-secondary hover:text-text-primary transition"
      title={theme === 'dark' ? 'Light mode' : 'Dark mode'}
    >
      {theme === 'dark' ? (
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" /></svg>
      ) : (
        <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" /></svg>
      )}
    </button>
  )
}
