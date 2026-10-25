import { useRef } from 'react'
import { rovingIndex, tabKeyTarget } from './a11y.js'

const tabId = (id, key) => `${id}-tab-${key}`
const panelId = (id, key) => `${id}-panel-${key}`

/**
 * Tabs — underline tabs (WAI-ARIA tabs, automatic activation).
 * Props: tabs [{key, label, count?}], value (selected key), onChange(key), className,
 *        id? (enables aria-controls ↔ <TabPanel tabsId={id} tabKey=…>), label? (aria-label of the list).
 * Keyboard: ←/→ move and select (wrapping), Home/End jump; only the selected tab is in the Tab order.
 */
export function Tabs({ tabs, value, onChange, className = '', id, label }) {
  const refs = useRef([])
  const roving = rovingIndex(tabs, value)

  function onKeyDown(e, i) {
    const next = tabKeyTarget(e.key, i, tabs.length)
    if (next < 0) return
    e.preventDefault()
    onChange(tabs[next].key)
    refs.current[next]?.focus()
  }

  return (
    <div role="tablist" aria-label={label} aria-orientation="horizontal"
      className={`flex gap-1 border-b border-default overflow-x-auto ${className}`}>
      {tabs.map((t, i) => {
        const active = t.key === value
        return (
          <button key={t.key} type="button" role="tab"
            ref={(el) => { refs.current[i] = el }}
            id={id ? tabId(id, t.key) : undefined}
            aria-controls={id ? panelId(id, t.key) : undefined}
            aria-selected={active} tabIndex={i === roving ? 0 : -1}
            onClick={() => onChange(t.key)} onKeyDown={(e) => onKeyDown(e, i)}
            className={`h-8 px-3 -mb-px text-sm whitespace-nowrap border-b-2 transition-colors
              focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent
              ${active ? 'border-accent text-primary' : 'border-transparent text-secondary hover:text-primary'}`}>
            {t.label}
            {t.count != null && <span className="ml-1.5 font-mono tabular-nums text-xs text-tertiary">{t.count}</span>}
          </button>
        )
      })}
    </div>
  )
}

/**
 * TabPanel — content of the selected tab, linked to its <Tabs id> for screen readers.
 * Props: tabsId (the Tabs id), tabKey (the selected key), className, children.
 */
export function TabPanel({ tabsId, tabKey, className = '', children }) {
  return (
    <div role="tabpanel" id={panelId(tabsId, tabKey)} aria-labelledby={tabId(tabsId, tabKey)} tabIndex={0}
      className={`focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent/40 ${className}`}>
      {children}
    </div>
  )
}
