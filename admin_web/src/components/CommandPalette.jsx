import { useEffect, useMemo } from 'react'
import { Command } from 'cmdk'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { useRecents } from '../hooks/useRecents.js'
import { Kbd } from './ui/index.js'
import { commands as ALL_COMMANDS } from './commands.js'
import { canOpen, visibleItems } from '../auth/access.js'

export default function CommandPalette({ open, onClose }) {
  const navigate = useNavigate()
  const { logout, can, isTenantAdmin } = useAuth()
  const { recents } = useRecents()

  // Escape closes (cmdk handles arrows + enter)
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const handleSelect = (cmd) => {
    onClose()
    if (cmd.kind === 'page' && cmd.href) {
      navigate(cmd.href)
    } else if (cmd.kind === 'action') {
      if (cmd.perform === 'logout') logout()
    }
  }

  // Only what this admin may open (the same rule as the sidebar).
  const grouped = useMemo(() => {
    const out = {}
    for (const c of visibleItems(ALL_COMMANDS, { can, isTenantAdmin })) {
      const g = c.group || 'Другое'
      if (!out[g]) out[g] = []
      out[g].push(c)
    }
    return out
  }, [can, isTenantAdmin])
  const shownRecents = recents.filter((r) => canOpen(r.href, { can, isTenantAdmin }))

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[60] flex items-start justify-center pt-24 bg-black/60" onClick={onClose}>
      <div
        className="w-full max-w-xl rounded border border-default bg-overlay shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <Command label="Поиск" loop>
          <Command.Input
            className="w-full h-12 bg-transparent px-4 text-sm text-primary placeholder:text-tertiary outline-none border-b border-default"
            placeholder="Поиск страниц, действий..."
            autoFocus
          />
          <Command.List className="max-h-96 overflow-y-auto p-2">
            <Command.Empty className="text-sm text-tertiary p-4">
              Ничего не найдено
            </Command.Empty>

            {shownRecents.length > 0 && (
              <Command.Group heading="Последние">
                {shownRecents.slice(0, 5).map((r) => (
                  <Command.Item
                    key={`recent-${r.id}`}
                    value={`recent ${r.label}`}
                    onSelect={() => { onClose(); navigate(r.href) }}
                  >
                    <span className="text-secondary">{r.label}</span>
                  </Command.Item>
                ))}
              </Command.Group>
            )}

            {Object.entries(grouped).map(([groupLabel, items]) => (
              <Command.Group key={groupLabel} heading={groupLabel}>
                {items.map((c) => {
                  const Icon = c.icon
                  return (
                    <Command.Item
                      key={c.id}
                      value={`${c.id} ${c.label} ${c.group || ''}`}
                      onSelect={() => handleSelect(c)}
                    >
                      {Icon && <Icon className="w-4 h-4" />}
                      <span className="flex-1">{c.label}</span>
                      {c.shortcut && (
                        <span className="ml-auto flex gap-0.5">
                          {c.shortcut.map((k, i) => <Kbd key={i}>{k}</Kbd>)}
                        </span>
                      )}
                    </Command.Item>
                  )
                })}
              </Command.Group>
            ))}
          </Command.List>
          <div className="px-3 py-2 border-t border-default text-[11px] text-tertiary flex gap-3">
            <span><Kbd>↑↓</Kbd> навигация</span>
            <span><Kbd>↵</Kbd> выбрать</span>
            <span><Kbd>esc</Kbd> закрыть</span>
          </div>
        </Command>
      </div>
    </div>
  )
}
