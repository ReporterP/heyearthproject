import { useEffect, useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import { dateTime } from '../lib/format.js'

// Thread (spec 02 §35): the conversation with support. Own messages on the right, answers on the
// left with «Поддержка HiEarth»; a divider before the first answer that was unread on opening.
const VIA = { bot: 'из Telegram', site: '', admin: '', owner_tg: '' }

// AuthImage loads a picture that needs the session (an <img> cannot send the header).
function AuthImage({ src }) {
  const { token } = useAuth()
  const [url, setUrl] = useState('')
  useEffect(() => {
    let alive = true
    let made = ''
    fetch(src, { headers: { Authorization: `Bearer ${token}` } })
      .then((r) => (r.ok ? r.blob() : null))
      .then((b) => { if (b && alive) { made = URL.createObjectURL(b); setUrl(made) } })
      .catch(() => {})
    return () => { alive = false; if (made) URL.revokeObjectURL(made) }
  }, [src, token])
  if (!url) return <span className="msg__img skel" style={{ width: 160, height: 120 }} aria-hidden="true" />
  return (
    <a href={url} target="_blank" rel="noopener" aria-label="Открыть картинку целиком">
      <img className="msg__img" src={url} alt="Картинка из переписки" />
    </a>
  )
}

export default function Thread({ messages, unreadFrom, image }) {
  return (
    <div className="thread" role="log" aria-live="polite">
      {messages.map((m) => (
        <div key={m.id} style={{ display: 'contents' }}>
          {unreadFrom && m.id === unreadFrom && <div className="thread__new">Новые ответы</div>}
          <div className={`msg rise ${m.author === 'user' ? 'msg--me' : 'msg--them'} ${m.sending ? 'is-sending' : ''}`}>
            <div className="msg__b">
              {m.author !== 'user' && <span className="msg__who">Поддержка HiEarth</span>}
              {m.body}
              {m.image && (m.localUrl
                ? <img className="msg__img" src={m.localUrl} alt="Картинка из переписки" />
                : <AuthImage src={image(m.image)} />)}
            </div>
            <span className="msg__meta">
              {m.sending ? 'Отправляем…' : [dateTime(m.created_at), m.author === 'user' ? VIA[m.via] : ''].filter(Boolean).join(' · ')}
            </span>
          </div>
        </div>
      ))}
    </div>
  )
}
