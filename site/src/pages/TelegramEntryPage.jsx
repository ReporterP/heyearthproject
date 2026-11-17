import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { apiFetch } from '../api/http.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { initData, loadTelegram, openExternal, setBackButton, startParam, tgFirstName } from '../lib/telegram.js'
import { kopNum } from '../lib/format.js'
import AuthLayout from '../components/AuthLayout.jsx'
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
// linked, otherwise «Создать HiEarth ID» / «У меня есть HiEarth ID». ?p= — the cabinet page to open.
const PAGES = new Set(['dashboard', 'connect', 'wallet', 'payments', 'support', 'news', 'settings', 'help'])
const PENDING = 'he_tg_pending' // the «Аккаунт создан» step survives a reload, like on the site

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
  const [state, setState] = useState('boot') // boot | outside | error | welcome | login | issued
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

  // Telegram's «Назад» returns from the sign-in form to the choice (12 §12.1 «Края»)
  useEffect(() => (state === 'login' ? setBackButton(true, () => setState('welcome')) : setBackButton(false)), [state])

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
    return <TgLogin onDone={(token) => { login(token); navigate(dest(), { replace: true }) }} onBack={() => setState('welcome')} />
  }

  const refOn = refCode && terms?.enabled && terms.friend_kop > 0
  return (
    <AuthLayout>
      <div>
        <h1 className="t-h1 rise">{name ? `Здравствуйте, ${name}` : 'Здравствуйте'}</h1>
        <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 10 }}>Это кабинет HiEarth в Telegram. Войдите по HiEarth ID или создайте новый аккаунт — это один клик.</p>
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
        <Button variant="secondary" size="lg" block disabled={busy} onClick={() => setState('login')}>У меня есть HiEarth ID</Button>
      </div>
      <p className="t-xs subtle" style={{ textAlign: 'center' }}>
        Нажимая кнопку, вы принимаете <a href="/offer" target="_blank" rel="noopener" className="link">договор-оферту</a> и <a href="/privacy" target="_blank" rel="noopener" className="link">политику конфиденциальности</a>.
      </p>
    </AuthLayout>
  )
}

function TgLogin({ onDone, onBack }) {
  const [id, setId] = useState('')
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [idEmpty, setIdEmpty] = useState(false)
  const idRef = useRef(null)
  const inFlight = useRef(false)
  const site = useMemo(() => `${window.location.origin}/settings#telegram`, [])

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
      <form className="stack gap-5" onSubmit={submit} noValidate>
        <div>
          <h1 className="t-h1">Войти по HiEarth ID</h1>
          <p className="t-lead" style={{ marginTop: 8 }}>Telegram привяжется к аккаунту — дальше вход будет сам.</p>
        </div>
        {err && <Alert tone="bad" title={err.title} rise>{err.text}</Alert>}
        <Field label="HiEarth ID или имя пользователя" error={idEmpty ? 'Укажите HiEarth ID или имя пользователя' : null}>
          <Input ref={idRef} mono size="lg" value={id} invalid={err?.field === 'id'} autoComplete="username" autoCapitalize="none" spellCheck={false}
            onChange={(e) => { setId(e.target.value); setIdEmpty(false); if (err?.field === 'id') setErr(null) }} />
        </Field>
        <Field label="Пароль" aside="если задавали" hint="Не задавали пароль — оставьте поле пустым.">
          <Input type="password" size="lg" value={pw} placeholder="Ваш пароль" invalid={err?.field === 'pw'} autoComplete="current-password"
            onChange={(e) => { setPw(e.target.value); if (err?.field === 'pw') setErr(null) }} />
        </Field>
        <Button variant="primary" size="lg" block type="submit" loading={busy}>Войти и привязать Telegram</Button>
        <p className="t-sm muted">
          Удобнее на сайте? Откройте hiearth.ru → «Настройки» → «Подключить Telegram».{' '}
          <button type="button" className="link" onClick={() => openExternal(site)} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Открыть сайт</button>
        </p>
        <div><Button variant="ghost" onClick={onBack} disabled={busy}>Назад</Button></div>
      </form>
    </AuthLayout>
  )
}
