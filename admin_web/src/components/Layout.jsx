import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
// CommandPalette to be created in T8 — placeholder import kept commented to avoid build break:
// import CommandPalette from './CommandPalette.jsx'

export default function Layout() {
  const [cmdkOpen, setCmdkOpen] = useState(false)

  // Global Cmd+K / Ctrl+K hotkey. CommandPalette mount lands in T8;
  // for now the state flip is observable via the console.log effect below.
  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setCmdkOpen(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (cmdkOpen) console.log('[Layout] Cmd+K triggered — CommandPalette mount in T8')
  }, [cmdkOpen])

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
      {/* CommandPalette mount placeholder; T8 will replace with real component */}
      {/* <CommandPalette open={cmdkOpen} onClose={() => setCmdkOpen(false)} /> */}
    </div>
  )
}
