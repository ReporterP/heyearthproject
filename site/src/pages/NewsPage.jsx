import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { date } from '../lib/format.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import EmptyState from '../components/EmptyState.jsx'
import RichText from '../components/RichText.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import { refreshBadges } from '../components/SupportCount.js'
import usePageMeta from '../hooks/usePageMeta.js'

// Новости (spec 08 §8.7): the feed of posts the owner sent to the cabinet.
export default function NewsPage() {
  usePageMeta('Новости — HiEarth', { noindex: true })
  const { token } = useAuth()
  const [data, setData] = useState(null)
  const [err, setErr] = useState(false)
  const [fresh, setFresh] = useState(0) // how many were unread when the page opened
  const marked = useRef(false)
  const showSkel = useDelayed(!data && !err, 300)

  const load = useCallback(async () => {
    setErr(false)
    try {
      const d = await apiFetch('/api/v1/news', { token })
      setData(d)
      if (!marked.current && d.unread > 0) {
        marked.current = true
        setFresh(d.unread)
        apiFetch('/api/v1/news/read', { token, method: 'POST' }).then(refreshBadges).catch(() => {})
      }
    } catch (e) {
      if (e.kind !== 'auth') setErr(true)
    }
  }, [token])
  useEffect(() => { load() }, [load])

  let body
  if (err && !data) {
    body = <EmptyState mood="worried" title="Не удалось загрузить новости." actions={<Button variant="secondary" onClick={load}>Повторить</Button>}>Обновите страницу.</EmptyState>
  } else if (!data) {
    body = <div className="stack gap-4" aria-busy="true">{showSkel && [0, 1].map((i) => <SkeletonCard key={i} lines={4} />)}</div>
  } else if (!data.items.length) {
    body = <EmptyState title="Новостей пока нет">Здесь появятся новости HiEarth, советы и акции.</EmptyState>
  } else {
    body = (
      <div className="stack gap-4">
        {data.items.map((n, i) => (
          <article key={n.id} className="card news">
            {n.image_url && <img className="news__img" src={n.image_url} alt="" loading="lazy" />}
            <div className="row wrap gap-2" style={{ alignItems: 'center' }}>
              <span className="t-xs subtle">{date(n.sent_at)}</span>
              {i < fresh && <Badge tone="info">Новое</Badge>}
            </div>
            {n.title && <h2 className="t-h3" style={{ marginTop: 8 }}>{n.title}</h2>}
            {n.body && <RichText html={n.body} className="news__body" />}
            {n.button_text && n.button_url && (
              <div style={{ marginTop: 16 }}>
                <Button variant="secondary" size="sm" href={n.button_url} target="_blank" rel="noopener noreferrer">{n.button_text}</Button>
              </div>
            )}
          </article>
        ))}
      </div>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 760 }}>
        <PageHead title="Новости" lead="Что нового в HiEarth: возможности, советы и акции."
          actions={data?.channel_url ? <Button variant="ghost" size="sm" icon="send" href={data.channel_url} target="_blank" rel="noopener noreferrer">Канал в Telegram</Button> : null} />
        {body}
      </div>
    </AppShell>
  )
}
