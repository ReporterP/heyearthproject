import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import MobileTopbar from './MobileTopbar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

const COLLAPSED_KEY = 'hiearth-sidebar-collapsed'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === 'true')
  const [mobileOpen, setMobileOpen] = useState(false)

  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))

  useEffect(() => {
    document.documentElement.style.setProperty('--sidebar-width', collapsed ? '60px' : '240px')
  }, [collapsed])

  // Lock body scroll while mobile drawer is open
  useEffect(() => {
    if (mobileOpen) {
      const prev = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      return () => { document.body.style.overflow = prev }
    }
  }, [mobileOpen])

  const onToggleCollapse = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(COLLAPSED_KEY, String(next))
  }

  return (
    <div className="min-h-screen bg-canvas text-primary">
      <MobileTopbar
        onOpenMenu={() => setMobileOpen(true)}
        onOpenCmdK={() => setCmdkOpen(true)}
      />

      <div className="md:flex">
        {/* Desktop sidebar */}
        <div className="hidden md:block">
          <Sidebar
            collapsed={collapsed}
            onToggleCollapse={onToggleCollapse}
            onCmdK={() => setCmdkOpen(true)}
          />
        </div>

        {/* Mobile drawer overlay */}
        {mobileOpen && (
          <div
            className="md:hidden fixed inset-0 bg-black/60 z-40"
            onClick={() => setMobileOpen(false)}
          />
        )}

        {/* Mobile sidebar drawer */}
        <div
          className={`md:hidden fixed inset-y-0 left-0 z-50 transform transition-transform duration-200 ${
            mobileOpen ? 'translate-x-0' : '-translate-x-full'
          }`}
        >
          <Sidebar
            collapsed={false}
            onToggleCollapse={() => setMobileOpen(false)}
            onCmdK={() => { setCmdkOpen(true); setMobileOpen(false) }}
            mobile
            onMobileNav={() => setMobileOpen(false)}
          />
        </div>

        <main className="flex-1 overflow-x-hidden min-w-0">
          <div className="max-w-screen-2xl mx-auto px-4 sm:px-6 py-4">
            <ErrorBoundary>
              <Outlet />
            </ErrorBoundary>
          </div>
        </main>
      </div>

      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  )
}
