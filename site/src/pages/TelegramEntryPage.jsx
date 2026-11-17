import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { initData, loadTelegram, onReturn, openExternal, setBackButton, startParam, tgFirstName } from '../lib/telegram.js'
import { kopNum } from '../lib/format.js'
import AuthLayout from '../components/AuthLayout.jsx'
import CopyField from '../components/CopyField.jsx'
import Alert from '../components/Alert.jsx'
import Button from '../components/Button.jsx'
import Field, { Input } from '../components/Field.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import MiniPage from '../components/MiniPage.jsx'
import { normalizeLogin } from '../components/Nomerok.jsx'
import { Spinner } from '../components/Controls.jsx'
import { useToast } from '../components/Toast.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { Issued } from './RegisterPage.jsx'
import { explain } from './LoginPage.jsx'

// Вход в Mini App (spec 12 §12.1): Telegram's initData → a session when this Telegram account is
// linked, otherwise «Создать HiEarth ID» / «У меня уже есть аккаунт» (через браузер, 12.5, или по HiEarth ID).
// ?p= — the cabinet page to open.
const PAGES = new Set(['dashboard', 'connect', 'wallet', 'payments', 'support', 'news', 'settings', 'help'])
const PENDING = 'he_tg_pending' // the «Аккаунт создан» step survives a reload, like on the site
const WAIT_MS = 10 * 60 * 1000 // the site's link lives 10 minutes
const connectURL = () => `${window.location.origin}/telegram/connect`

function readPending() {
  try { return JSON.parse(sessionStorage.getItem(PENDING) || 'null') } catch { return null }
}
function writePending(v) {
  try { if (v) sessionStorage.setItem(PENDING, JSON.stringify(v)); else sessionStorage.removeItem(PENDING) } catch { /* storage off */ }
}

function destination(p) {
  const page = String(p || '').replace(/^\/+/, '')
  return PAGES.has(page) ? `/${page}` : '/dashboard'
}

function useRefTerms(enabled) {
  const [t, setT] = useState(null)
  useEffect(() => {
    if (!enabled) return
    fetch('/api/v1/referral/terms').then((r) => (r.ok ? r.json() : null)).then(setT).catch(() => setT(null))
  }, [enabled])
  return t
}

export default function TelegramEntryPage() {
  usePageMeta('Кабинет в Telegram — HiEarth', { noindex: true })
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { login } = useAuth()
  const toast = useToast()
  const narrow = useMedia('(max-width: 1024px)')
  const [state, setState] = useState('boot') // boot | outside | error | welcome | login | waiting | issued
  const [error, setError] = useState('')
  const [name, setName] = useState('')
  const [pending, setPending] = useState(readPending)
  const [busy, setBusy] = useState(false)
  const [createErr, setCreateErr] = useState(null)
  const sp = useRef('')
  const started = useRef(false)

  const dest = () => destination(params.get('p') || (sp.current.startsWith('ref_') ? '' : sp.current))
  const refCode = sp.current.startsWith('ref_') ? sp.current.slice(4) : ''
  const terms = useRefTerms(state === 'welcome' && !!refCode)

  async function boot() {
    setState('boot')
    setError('')
    const w = await loadTelegram()
    if (!w) { setState('outside'); return }
    sp.current = startParam()
    if (readPending()?.id) { setState('issued'); return }
    try {
      const r = await apiFetch('/api/v1/auth/telegram', { method: 'POST', body: { init_data: initData() }, signOutOn401: false })
      if (r.linked && r.token) {
        login(r.token)
        navigate(dest(), { replace: true })
        return
      }
      setName(r.telegram?.first_name || tgFirstName())
      setState('welcome')
    } catch (e) {
      setError(e.message)
      setState('error')
    }
  }

  useEffect(() => {
    if (started.current) return
    started.current = true
    boot()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Telegram's «Назад»: login → welcome, waiting → login (12 §12.1 «Края»)
  useEffect(() => {
    if (state === 'login') return setBackButton(true, () => setState('welcome'))
    if (state === 'waiting') return setBackButton(true, () => setState('login'))
    return setBackButton(false)
  }, [state])

  const enter = useCallback((token) => { login(token); navigate(dest(), { replace: true }) }, [login, navigate]) // eslint-disable-line react-hooks/exhaustive-deps

  // «Войти через браузер»: the site (signed in there) links this Telegram through the bot (12.5)
  function viaBrowser() {
    openExternal(connectURL())
    setState('waiting')
  }

  async function create() {
    if (busy) return
    setBusy(true)
    setCreateErr(null)
    try {
      const r = await apiFetch('/api/v1/auth/telegram/register', { method: 'POST', body: { init_data: initData(), referral_code: refCode }, signOutOn401: false })
      const next = { id: r.user_id, token: r.token }
      writePending(next)
      setPending(next)
      setState('issued')
    } catch (e) {
      setCreateErr(e?.status === 429 ? { title: 'Слишком много попыток.', text: 'Подождите минуту и попробуйте снова.' }
        : { title: 'Не получилось создать аккаунт.', text: e.message || 'Попробуйте ещё раз через минуту.' })
    } finally {
      setBusy(false)
    }
  }

  if (state === 'boot') {
    return (
      <MiniPage label="Открываем кабинет">
        <Spinner />
        <h1 className="t-h3">Открываем кабинет…</h1>
      </MiniPage>
    )
  }
  if (state === 'outside') {
    return (
      <MiniPage label="Кабинет в Telegram">
        <Hedgehog mood="idle" size={120} />
        <h1 className="t-h3">Эта страница открывается из Telegram</h1>
        <p className="muted t-sm">Откройте бота HiEarth и нажмите «Кабинет». На сайте можно войти как обычно.</p>
        <Button variant="secondary" to="/login">Войти на сайте</Button>
      </MiniPage>
    )
  }
  if (state === 'error') {
    return (
      <MiniPage label="Не удалось войти">
        <Hedgehog mood="worried" size={120} />
        <h1 className="t-h3">Не удалось войти</h1>
        <p className="muted t-sm">{error || 'Попробуйте ещё раз через минуту.'}</p>
        <Button variant="secondary" onClick={boot}>Повторить</Button>
      </MiniPage>
    )
  }
  if (state === 'issued' && pending?.id) {
    return (
      <Issued pending={pending} narrow={narrow} toast={toast}
        onDone={() => { writePending(null); login(pending.token); navigate(dest(), { replace: true }) }} />
    )
  }
  if (state === 'login') {
    return <TgLogin onDone={enter} onBack={() => setState('welcome')} onBrowser={viaBrowser} />
  }
  if (state === 'waiting') {
    return <TgWaiting onLinked={(token) => { toast.ok('Telegram подключён'); enter(token) }}
      onReopen={() => openExternal(connectURL())} onLogin={() => setState('login')} />
  }

  const refOn = refCode && terms?.enabled && terms.friend_kop > 0
  return (
    <AuthLayout>
      <div>
        <h1 className="t-h1 rise">{name ? `Здравствуйте, ${name}` : 'Здравствуйте'}</h1>
        <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 10 }}>Это кабинет HiEarth в Telegram. Создайте аккаунт в один клик или войдите в свой.</p>
      </div>
      {refOn && (
        <Alert tone="ok" icon="gift" title="Вас пригласил друг" className="rise" style={{ '--d': '160ms' }}>
          {terms.referrer_kop === terms.friend_kop
            ? `— после первой оплаты вам обоим придёт по ${kopNum(terms.friend_kop)} ₽.`
            : `— после первой оплаты вам придёт ${kopNum(terms.friend_kop)} ₽.`}
        </Alert>
      )}
      {createErr && <Alert tone="bad" title={createErr.title} rise>{createErr.text}</Alert>}
      <div className="stack gap-3 rise" style={{ '--d': '200ms' }}>
        <Button variant="primary" size="lg" block loading={busy} onClick={create}>Создать HiEarth ID</Button>
        <Button variant="secondary" size="lg" block disabled={busy} onClick={() => setState('login')}>У меня уже есть аккаунт</Button>
      </div>
      <p className="t-xs subtle" style={{ textAlign: 'center' }}>
        Нажимая кнопку, вы принимаете <a href="/offer" target="_blank" rel="noopener" className="link">договор-оферту</a> и <a href="/privacy" target="_blank" rel="noopener" className="link">политику конфиденциальности</a>.
      </p>
    </AuthLayout>
  )
}

function TgLogin({ onDone, onBack, onBrowser }) {
  const [id, setId] = useState('')
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [idEmpty, setIdEmpty] = useState(false)
  const idRef = useRef(null)
  const inFlight = useRef(false)

  async function submit(e) {
    e.preventDefault()
    if (inFlight.current) return
    const value = normalizeLogin(id)
    if (!value) { setIdEmpty(true); idRef.current?.focus(); return }
    inFlight.current = true
    setBusy(true)
    setErr(null)
    try {
      const r = await apiFetch('/api/v1/auth/telegram/login', { method: 'POST', body: { init_data: initData(), id_or_username: value, password: pw }, signOutOn401: false })
      onDone(r.token)
    } catch (e2) {
      setErr(e2?.status === 409 ? { title: 'Не удалось привязать Telegram.', text: e2.message } : explain(e2))
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  return (
    <AuthLayout width={400}>
      <div className="stack gap-5">
        <div>
          <h1 className="t-h1">Вход в свой аккаунт</h1>
          <p className="t-lead" style={{ marginTop: 8 }}>Уже вошли на hiearth.ru в браузере? Подтвердите там — HiEarth ID и пароль не понадобятся.</p>
        </div>
        <Button variant="primary" size="lg" block icon="external" disabled={busy} onClick={onBrowser}>Войти через браузер</Button>
        <form className="stack gap-5" onSubmit={submit} noValidate style={{ paddingTop: 20, borderTop: '1px solid var(--line)' }}>
          <p className="t-sm subtle">Или по HiEarth ID</p>
          {err && <Alert tone="bad" title={err.title} rise>{err.text}</Alert>}
          <Field label="HiEarth ID или имя пользователя" error={idEmpty ? 'Укажите HiEarth ID или имя пользователя' : null}>
            <Input ref={idRef} mono size="lg" value={id} invalid={err?.field === 'id'} autoComplete="username" autoCapitalize="none" spellCheck={false}
              onChange={(e) => { setId(e.target.value); setIdEmpty(false); if (err?.field === 'id') setErr(null) }} />
          </Field>
          <Field label="Пароль" aside="если задавали" hint={err?.field === 'pw' ? null : 'Не задавали пароль — оставьте поле пустым.'}>
            <Input type="password" size="lg" value={pw} placeholder="Ваш пароль" invalid={err?.field === 'pw'} autoComplete="current-password"
              onChange={(e) => { setPw(e.target.value); if (err?.field === 'pw') setErr(null) }} />
          </Field>
          <Button variant="secondary" size="lg" block type="submit" loading={busy}>Войти по HiEarth ID</Button>
        </form>
        <div><Button variant="ghost" onClick={onBack} disabled={busy}>Назад</Button></div>
      </div>
    </AuthLayout>
  )
}

// «Подтвердите в браузере» (12 §12.1 waiting): the site links this Telegram through the bot; the Mini
// App asks again when it is back on screen and every 10 s while visible, for 10 minutes.
function TgWaiting({ onLinked, onReopen, onLogin }) {
  const [checking, setChecking] = useState(false)
  const [note, setNote] = useState(null)
  const inFlight = useRef(false)
  const done = useRef(false)
  const until = useRef(Date.now() + WAIT_MS)

  const check = useCallback(async (manual) => {
    if (inFlight.current || done.current) return
    if (!manual && (Date.now() > until.current || document.visibilityState !== 'visible')) return
    inFlight.current = true
    if (manual) { setChecking(true); setNote(null) }
    try {
      const r = await apiFetch('/api/v1/auth/telegram', { method: 'POST', body: { init_data: initData() }, signOutOn401: false })
      if (r.linked && r.token) { done.current = true; onLinked(r.token); return }
      if (manual) setNote({ tone: 'info', title: 'Пока не подключено.', text: 'Нажмите «Подключить Telegram» в браузере, затем «Запустить» в боте.' })
    } catch (e) {
      if (manual) setNote({ tone: 'bad', title: 'Не удалось проверить.', text: e?.status === 429 ? 'Подождите минуту и попробуйте снова.' : (e?.message || 'Попробуйте ещё раз через минуту.') })
    } finally {
      inFlight.current = false
      if (manual) setChecking(false)
    }
  }, [onLinked])

  useEffect(() => {
    const off = onReturn(() => check(false))
    const t = setInterval(() => check(false), 10000)
    return () => { off(); clearInterval(t) }
  }, [check])

  return (
    <AuthLayout width={400}>
      <div className="stack gap-5">
        <div>
          <h1 className="t-h1">Подтвердите в браузере</h1>
          <p className="t-lead" style={{ marginTop: 8 }}>На открывшейся странице нажмите «Подключить Telegram», затем «Запустить» в боте. Вернитесь сюда — кабинет откроется сам.</p>
        </div>
        {note && <Alert tone={note.tone} title={note.title} rise>{note.text}</Alert>}
        <div className="stack gap-3">
          <Button variant="primary" size="lg" block loading={checking} onClick={() => check(true)}>Проверить и войти</Button>
          <Button variant="secondary" size="lg" block disabled={checking} onClick={onReopen}>Открыть сайт ещё раз</Button>
        </div>
        {/* Telegram may open its own browser, where the person is not signed in: the address for their own one */}
        <div className="stack gap-2">
          <p className="t-sm muted">Сайт попросил войти? Откройте адрес в своём браузере или на компьютере — там, где вы уже вошли:</p>
          <CopyField value={connectURL()} display={connectURL().replace(/^https?:\/\//, '')} label="Адрес страницы подключения" />
        </div>
        <div><Button variant="ghost" onClick={onLogin} disabled={checking}>Войти по HiEarth ID</Button></div>
      </div>
    </AuthLayout>
  )
}
