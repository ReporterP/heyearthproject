import { useEffect } from 'react'

// usePageMeta(title, { noindex, description }) — the document title of a page, and robots noindex
// for personal pages (cabinet, personal links, 404).
export default function usePageMeta(title, { noindex = false, description } = {}) {
  useEffect(() => {
    const prev = document.title
    if (title) document.title = title
    let meta = null
    if (noindex) {
      meta = document.createElement('meta')
      meta.name = 'robots'
      meta.content = 'noindex, nofollow'
      document.head.appendChild(meta)
    }
    let desc = null
    let prevDesc = null
    if (description) {
      desc = document.querySelector('meta[name="description"]')
      prevDesc = desc?.getAttribute('content')
      desc?.setAttribute('content', description)
    }
    return () => {
      document.title = prev
      if (meta) meta.remove()
      if (desc && prevDesc != null) desc.setAttribute('content', prevDesc)
    }
  }, [title, noindex, description])
}
