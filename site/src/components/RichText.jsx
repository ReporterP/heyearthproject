import { Fragment, useMemo } from 'react'

// RichText renders a news text written in Telegram's HTML subset (spec 08 §8.7). The elements are
// built here from an allow-list; anything else becomes plain text. No dangerouslySetInnerHTML.
const TAGS = {
  b: 'b', strong: 'b', i: 'i', em: 'i', u: 'u', ins: 'u', s: 's', strike: 's', del: 's',
  code: 'code', pre: 'pre', blockquote: 'blockquote', 'tg-spoiler': 'span', a: 'a',
}

function safeHref(href) {
  const h = String(href || '').trim()
  return /^https?:\/\//i.test(h) || /^tg:\/\//i.test(h) ? h : ''
}

function convert(node, key) {
  if (node.nodeType === 3) return node.nodeValue
  if (node.nodeType !== 1) return null
  const name = node.nodeName.toLowerCase()
  const children = Array.from(node.childNodes).map((c, i) => convert(c, i))
  const Tag = TAGS[name]
  if (!Tag) return <Fragment key={key}>{children}</Fragment>
  if (name === 'a') {
    const href = safeHref(node.getAttribute('href'))
    if (!href) return <Fragment key={key}>{children}</Fragment>
    return <a key={key} href={href} target="_blank" rel="noopener noreferrer" className="link">{children}</a>
  }
  return <Tag key={key}>{children}</Tag>
}

export default function RichText({ html, className = '' }) {
  const nodes = useMemo(() => {
    if (!html) return null
    try {
      const doc = new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html')
      return Array.from(doc.body.childNodes).map((n, i) => convert(n, i))
    } catch {
      return html
    }
  }, [html])
  return <div className={className}>{nodes}</div>
}
