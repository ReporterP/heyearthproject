import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

const COLLAPSED_KEY = 'hiearth-sidebar-collapsed'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === 'true')

  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))

  useEffect(() => {
    document.documentElement.style.setProperty('--sidebar-width', collapsed ? '60px' : '240px')
  }, [collapsed])

  const onToggleCollapse = () => {
    const next = !collapsed
    setCollapsed(next)
    localStorage.setItem(COLLAPSED_KEY, String(next))
  }

  return (
    <div className="min-h-screen flex bg-canvas text-primary">
      <Sidebar
        collapsed={collapsed}
        onToggleCollapse={onToggleCollapse}
        onCmdK={() => setCmdkOpen(true)}
      />
      <main className="flex-1 overflow-x-hidden">
        <div className="max-w-screen-2xl mx-auto px-6 py-4">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </div>
      </main>
      <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} />
    </div>
  )
}
