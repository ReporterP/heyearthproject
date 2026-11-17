import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { safeRedirect } from '../lib/redirect.js'
import Button from '../components/Button.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import MiniPage from '../components/MiniPage.jsx'
import { Spinner } from '../components/Controls.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// Одноразовый вход /m/:token (spec 12 §12.2): opened in the browser from the Telegram Mini App or
// bot («Пополнить на сайте»). The token is spent by a POST, so a link preview never uses it up.
const spent = new Set() // StrictMode runs effects twice in development

export default function MagicLinkPage() {
  usePageMeta('Вход — HiEarth', { noindex: true })
  const { token } = useParams()
  const navigate = useNavigate()
  const { login } = useAuth()
  const [state, setState] = useState('wait') // wait | bad | temp
  const next = useRef('/dashboard')

  function redeem() {
    setState('wait')
    apiFetch('/api/v1/auth/magic/redeem', { method: 'POST', body: { token }, signOutOn401: false })
      .then((r) => {
        login(r.token)
        navigate(safeRedirect(r.next), { replace: true })
      })
      .catch((e) => setState(e.status === 404 || e.status === 400 ? 'bad' : 'temp'))
  }

  useEffect(() => {
    if (spent.has(token)) return
    spent.add(token)
    redeem()
  }, [token]) // eslint-disable-line react-hooks/exhaustive-deps

  if (state === 'wait') {
    return (
      <MiniPage label="Вход">
        <Spinner />
        <h1 className="t-h3">Входим…</h1>
      </MiniPage>
    )
  }
  return (
    <MiniPage label="Вход">
      <Hedgehog mood="worried" size={120} />
      {state === 'bad' ? (
        <>
          <h1 className="t-h3">Ссылка для входа устарела</h1>
          <p className="muted t-sm">Она действует 5 минут и срабатывает один раз. Нажмите «Пополнить» в Telegram ещё раз — или войдите по HiEarth ID.</p>
          <Button variant="secondary" to={`/login?redirect=${encodeURIComponent(next.current)}`}>Войти</Button>
        </>
      ) : (
        <>
          <h1 className="t-h3">Не получилось</h1>
          <p className="muted t-sm">Сервис временно недоступен. Попробуйте ещё раз через минуту.</p>
          <Button variant="secondary" onClick={redeem}>Повторить</Button>
        </>
      )}
    </MiniPage>
  )
}
