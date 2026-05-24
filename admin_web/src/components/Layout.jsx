import { useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import CommandPalette from './CommandPalette.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { useHotkey } from '../hooks/useHotkey.js'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)
  useHotkey({ key: 'k', meta: true }, () => setCmdkOpen((v) => !v))

  return (
    <div className="min-h-screen flex bg-canvas text-primary">
      <Sidebar onCmdK={() => setCmdkOpen(true)} />
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
