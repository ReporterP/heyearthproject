import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import SiteHeader from './SiteHeader.jsx'
import SiteFooter from './SiteFooter.jsx'
import Toc, { useScrollSpy } from './Toc.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'

// One template for the documents (spec 06 §6.3): a segmented switch between them (each is its own
// route), «Содержание» built from the document's own h2 headings, the text in `doc` typography.
// The texts themselves are the current editions — this file only lays them out.
let refocusTab = false // a keyboard switch between documents keeps the focus on the tabs

const DOCS = [
  { to: '/offer', label: 'Договор-оферта' },
  { to: '/privacy', label: 'Конфиденциальность' },
  { to: '/refund', label: 'Условия возврата' },
]

export default function LegalLayout({ title, updated, children }) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const narrow = useMedia('(max-width: 1024px)')
  const doc = useRef(null)
  const [items, setItems] = useState([])
  usePageMeta(`${title} — HiEarth`)
  const tabs = useRef(null)
  useEffect(() => {
    if (!refocusTab) return
    refocusTab = false
    tabs.current?.querySelector('[aria-selected="true"]')?.focus()
  }, [pathname])

  useEffect(() => {
    const heads = [...(doc.current?.querySelectorAll('h2') || [])]
    setItems(heads.map((h, i) => {
      if (!h.id) h.id = `sec-${i + 1}`
      return { id: h.id, label: h.textContent.trim().replace(/^\d+\.\s*/, '') }
    }))
  }, [pathname])

  const [current] = useScrollSpy(items.map((i) => i.id))

  return (
    <>
      <SiteHeader />
      <main id="main" className="container" style={{ paddingTop: narrow ? 28 : 48, paddingBottom: 96 }}>
        <div ref={tabs} className="seg" role="tablist" aria-label="Документы" style={{ maxWidth: '100%', overflowX: 'auto' }}>
          {DOCS.map((d, i) => {
            const on = pathname === d.to
            const go = (j) => { refocusTab = true; navigate(DOCS[(j + DOCS.length) % DOCS.length].to) }
            return (
              // the seg look is styled on <label>; each tab switches the route (06 §6.3)
              // eslint-disable-next-line jsx-a11y/label-has-associated-control
              <label
                key={d.to}
                role="tab"
                tabIndex={on ? 0 : -1}
                aria-selected={on}
                className={on ? 'is-active' : ''}
                style={{ whiteSpace: 'nowrap' }}
                onClick={() => navigate(d.to)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); refocusTab = true; navigate(d.to) }
                  if (e.key === 'Home') { e.preventDefault(); go(0) }
                  if (e.key === 'End') { e.preventDefault(); go(DOCS.length - 1) }
                  if (e.key === 'ArrowRight') go(i + 1)
                  if (e.key === 'ArrowLeft') go(i - 1)
                }}
              >
                {d.label}
              </label>
            )
          })}
        </div>
        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : '260px minmax(0, 1fr)', gap: narrow ? 0 : 56, marginTop: 32, alignItems: 'start' }}>
          {!narrow && items.length > 2 && (
            <div style={{ position: 'sticky', top: 92 }}>
              <p className="t-label" style={{ padding: '0 10px 8px' }}>Содержание</p>
              <Toc items={items} current={current} numbered />
            </div>
          )}
          <article className="doc" ref={doc}>
            <h1 className="t-h1">{title}</h1>
            {updated && <p className="subtle t-sm" style={{ marginTop: 10 }}>{updated}</p>}
            {children}
          </article>
        </div>
      </main>
      <SiteFooter compact />
    </>
  )
}
