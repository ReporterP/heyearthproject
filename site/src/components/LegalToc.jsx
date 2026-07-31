import { useEffect, useState } from 'react'

/**
 * LegalToc — builds a table of contents from the <h2> headings inside
 * `#legal-doc`, assigns anchor ids, and highlights the section currently in
 * view. Long legal documents are unscannable without one; this adds navigation
 * without duplicating the text.
 */
export default function LegalToc() {
  const [items, setItems] = useState([])
  const [active, setActive] = useState('')

  useEffect(() => {
    const root = document.getElementById('legal-doc')
    if (!root) return
    const heads = Array.from(root.querySelectorAll('h2'))
    const list = heads.map((h, i) => {
      const id = h.id || `sec-${i + 1}`
      h.id = id
      h.style.scrollMarginTop = '96px'
      return { id, text: h.textContent.trim() }
    })
    setItems(list)
    if (list.length) setActive(list[0].id)

    const onScroll = () => {
      let current = list[0]?.id
      for (const it of list) {
        const el = document.getElementById(it.id)
        if (el && el.getBoundingClientRect().top <= 120) current = it.id
      }
      setActive(current)
    }
    window.addEventListener('scroll', onScroll, { passive: true })
    onScroll()
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  if (items.length < 3) return null

  return (
    <nav aria-label="Содержание" className="card rounded-[22px] p-5 mb-6 lg:sticky lg:top-24">
      <p className="text-[11px] uppercase tracking-wider text-faint mb-3">Содержание</p>
      <ol className="space-y-1.5">
        {items.map((it, i) => (
          <li key={it.id}>
            <a
              href={`#${it.id}`}
              onClick={(e) => {
                e.preventDefault()
                document.getElementById(it.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
                history.replaceState(null, '', `#${it.id}`)
              }}
              className={`flex gap-2 text-sm leading-snug transition-colors ${
                active === it.id ? 'text-brand font-medium' : 'text-text-secondary hover:text-text-primary'
              }`}
            >
              <span className="text-faint tabular-nums flex-shrink-0">{i + 1}.</span>
              <span>{it.text.replace(/^\d+\.\s*/, '')}</span>
            </a>
          </li>
        ))}
      </ol>
    </nav>
  )
}
