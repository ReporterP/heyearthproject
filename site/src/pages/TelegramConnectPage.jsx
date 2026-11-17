import { useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import Alert from '../components/Alert.jsx'
import Button from '../components/Button.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import MiniPage from '../components/MiniPage.jsx'
import { shortId } from '../components/Nomerok.jsx'
import { Spinner } from '../components/Controls.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// Подключение Telegram из браузера /telegram/connect (spec 12 §12.5): opened from the Mini App
// («Войти через браузер») in a browser where the person is signed in. The Mini App cannot see that
// sign-in, so the browser links the Telegram account through the bot (t.me/<bot>?start=link_…, the
// same path as «Подключить Telegram» in the settings); from then on the Mini App opens by itself.
// The link is made only by the button — never by opening the page — and the bot, not the page,
// says which Telegram account it is.
const WAIT_MS = 10 * 60 * 1000

export default function TelegramConnectPage() {
  usePageMeta('Подключить Telegram — HiEarth', { noindex: true })
  const { token } = useAuth()
  const [state, setState] = useState('loading') // loading | ready | waiting | done | already | error
  const [error, setError] = useState('')
  const [linkErr, setLinkErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [id, setId] = useState('')
  const [tg, setTg] = useState(null)
  const [url, setUrl] = useState('')
  const until = useRef(0)

  async function load() {
    setState('loading')
    setError('')
    try {
      const [p, t] = await Promise.all([
        apiFetch('/api/v1/user/profile', { token }),
        apiFetch('/api/v1/user/telegram', { token }),
      ])
      setId(p?.user_id || '')
      setTg(t)
      setState(t?.linked ? 'already' : 'ready')
    } catch (e) {
      if (e?.kind === 'auth') return // RequireAuth takes the page to /login
      setError(e?.message || 'Попробуйте ещё раз через минуту.')
      setState('error')
    }
  }

  useEffect(() => { load() }, [token]) // eslint-disable-line react-hooks/exhaustive-deps

  // waiting: ask every 3 s, while the tab is visible, whether the bot linked it
  useEffect(() => {
    if (state !== 'waiting') return undefined
    const t = setInterval(async () => {
      if (Date.now() > until.current) { clearInterval(t); return }
      if (document.visibilityState !== 'visible') return
      try {
        const d = await apiFetch('/api/v1/user/telegram', { token })
        if (d?.linked) { clearInterval(t); setTg(d); setState('done') }
      } catch { /* the next tick asks again */ }
    }, 3000)
    return () => clearInterval(t)
  }, [state, token])

  async function connect() {
    if (busy) return
    setBusy(true)
    setLinkErr('')
    try {
      const r = await apiFetch('/api/v1/user/telegram/link', { token, method: 'POST' })
      until.current = Date.now() + WAIT_MS
      setUrl(r.url)
      setState('waiting')
      window.location.href = r.url // on a phone this opens Telegram; the page stays behind it
    } catch (e) {
      setLinkErr(e?.message || 'Не удалось подготовить ссылку — попробуйте через минуту.')
    } finally {
      setBusy(false)
    }
  }

  if (state === 'loading') {
    return (
      <MiniPage label="Проверяем аккаунт">
        <Spinner />
        <h1 className="t-h3">Проверяем аккаунт…</h1>
      </MiniPage>
    )
  }
  if (state === 'error') {
    return (
      <MiniPage label="Не удалось загрузить данные">
        <Hedgehog mood="worried" size={120} />
        <h1 className="t-h3">Не удалось загрузить данные</h1>
        <p className="muted t-sm">{error}</p>
        <Button variant="secondary" onClick={load}>Повторить</Button>
      </MiniPage>
    )
  }
  if (state === 'waiting') {
    return (
      <MiniPage label="Ждём подтверждения в Telegram">
        <Hedgehog mood="search" size={120} />
        <h1 className="t-h3">Нажмите «Запустить» и «Привязать» в боте</h1>
        <p className="muted t-sm">Потом вернитесь в кабинет в Telegram — он откроется сам.</p>
        <Button variant="secondary" block href={url}>Открыть бота ещё раз</Button>
      </MiniPage>
    )
  }
  if (state === 'done' || state === 'already') {
    return (
      <MiniPage label="Telegram подключён">
        <Hedgehog mood="happy" size={120} />
        <h1 className="t-h3">{state === 'done' ? 'Telegram подключён' : 'Telegram уже подключён'}</h1>
        <p className="muted t-sm">Вернитесь в Telegram — кабинет откроется сам.</p>
        {tg?.username && <p className="mono t-sm">@{tg.username}</p>}
        <div className="stack gap-3" style={{ width: '100%' }}>
          {tg?.bot_url && <Button variant="primary" size="lg" block href={tg.bot_url}>Открыть Telegram</Button>}
          <Button variant="ghost" to="/dashboard">В кабинет на сайте</Button>
        </div>
        {state === 'already' && (
          <p className="t-xs subtle">Другой Telegram? Отвяжите этот в «<a href="/settings#telegram" className="link">Настройках</a>» и подключите заново.</p>
        )}
      </MiniPage>
    )
  }
  return (
    <MiniPage label="Подключить Telegram">
      <Hedgehog mood="idle" size={120} />
      <h1 className="t-h3">Подключить Telegram</h1>
      <p className="muted t-sm">Кабинет в Telegram будет открываться сам — без HiEarth ID и пароля.</p>
      {id && <p className="t-sm">Аккаунт: <span className="mono">{shortId(id)}</span></p>}
      {linkErr && <Alert tone="bad" title="Не удалось подключить." rise>{linkErr}</Alert>}
      <Button variant="primary" size="lg" block icon="send" loading={busy} onClick={connect}>Подключить Telegram</Button>
      <p className="t-xs subtle">Откроется бот HiEarth — нажмите в нём «Запустить», затем «Привязать». Ссылка действует 10 минут.</p>
    </MiniPage>
  )
}
