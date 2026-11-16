import { useEffect, useState } from 'react'

// Table of contents (spec 02 §33 `toc`, 06 §6.2–6.3): sticky list; the current section is the one in
// the top third of the screen (aria-current="true"). A click scrolls smoothly and records #id in the
// history; manual scrolling only replaces the address.
export function useScrollSpy(ids) {
  const [current, setCurrent] = useState(ids[0])
  const key = ids.join(',')
  useEffect(() => {
    const list = key.split(',')
    let raf = 0
    const on = () => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => {
        const line = window.innerHeight / 3
        let cur = list[0]
        for (const id of list) {
          const el = document.getElementById(id)
          if (el && el.getBoundingClientRect().top <= line) cur = id
        }
        // at the very bottom the last section is current even if it is short
        if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) cur = list[list.length - 1]
        setCurrent((c) => {
          if (c !== cur && window.location.hash && window.location.hash !== `#${cur}`) {
            window.history.replaceState(window.history.state, '', `#${cur}`)
          }
          return cur
        })
      })
    }
    on()
    window.addEventListener('scroll', on, { passive: true })
    window.addEventListener('resize', on)
    return () => { cancelAnimationFrame(raf); window.removeEventListener('scroll', on); window.removeEventListener('resize', on) }
  }, [key])
  return [current, setCurrent]
}

export default function Toc({ items, current, onPick, label = 'Содержание', numbered = false, className = '', style }) {
  return (
    <nav className={`toc stack gap-1 ${className}`} aria-label={label} style={style}>
      {items.map((it, i) => (
        <a
          key={it.id}
          href={`#${it.id}`}
          aria-current={current === it.id ? 'true' : undefined}
          onClick={(e) => {
            e.preventDefault()
            const el = document.getElementById(it.id)
            if (el) el.scrollIntoView({ block: 'start' })
            window.history.pushState(window.history.state, '', `#${it.id}`)
            onPick?.(it.id)
          }}
        >
          {numbered && <span>{i + 1}</span>}
          {it.label}
        </a>
      ))}
    </nav>
  )
}
