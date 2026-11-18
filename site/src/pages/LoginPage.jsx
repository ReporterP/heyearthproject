import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { loginUser } from '../api/user.js'
import { useAuth, SESSION_EXPIRED_KEY } from '../auth/AuthContext.jsx'
import { safeRedirect } from '../lib/redirect.js'
import AuthLayout from '../components/AuthLayout.jsx'
import Field, { Input } from '../components/Field.jsx'
import Button from '../components/Button.jsx'
import Alert from '../components/Alert.jsx'
import Nomerok, { normalizeLogin } from '../components/Nomerok.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// Вход (spec 07 §7.3, artboard «Вход»). ?redirect= (or ?next=) — where to return after signing in.
function readExpired() {
  try {
    const v = sessionStorage.getItem(SESSION_EXPIRED_KEY) === '1'
    sessionStorage.removeItem(SESSION_EXPIRED_KEY)
    return v
  } catch { return false }
}

const HIEARTH_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// server answer → which field is wrong and the catalogue text (05 §7). `login` is what was
// typed: for a login name the server answers an unknown one like a known one (no enumeration),
// so the texts don't claim the account exists.
export function explain(e, login = '') {
  const raw = String(e?.data?.error || '').toLowerCase()
  const byName = !!login && !HIEARTH_ID.test(login)
  if (e?.kind === 'rate' || e?.status === 429) return { field: null, title: 'Слишком много попыток.', text: 'Подождите минуту и попробуйте снова.' }
  if (raw === 'user not found') return { field: 'id', title: 'Аккаунт не найден.', text: 'Проверьте ID — его легко перепутать с похожими символами.' }
  // «password required»: the account has a password and the field was empty (a login name
  // always has one — SetUsername needs a password first). «invalid password» only happens
  // when a password is set too, so neither says «leave the field empty».
  if (raw === 'password required') return byName
    ? { field: 'pw', title: 'Введите пароль.', text: 'Вход по логину — всегда с паролем. Забыли — напишите в поддержку, пароль сбросят.' }
    : { field: 'pw', title: 'У этого аккаунта есть пароль.', text: 'Введите его. Забыли — напишите в поддержку, пароль сбросят.' }
  if (raw === 'invalid password' || raw === 'invalid credentials') return { field: 'pw', title: byName ? 'Неверный логин или пароль.' : 'Неверный пароль.', text: 'Проверьте раскладку и Caps Lock. Забыли — напишите в поддержку, пароль сбросят.' }
  if (raw === 'user is blocked') return { field: null, title: 'Аккаунт заблокирован.', text: 'Напишите в поддержку — разберёмся.' }
  if (e?.kind === 'network') return { field: null, title: 'Нет связи с сервером.', text: 'Проверьте интернет и попробуйте ещё раз.' }
  return { field: null, title: 'Не удалось войти.', text: 'Попробуйте ещё раз через минуту.' }
}

export default function LoginPage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const { login, isLoggedIn } = useAuth()
  const redirectTo = safeRedirect(params.get('redirect') || params.get('next'))
  const registerHref = redirectTo !== '/dashboard' ? `/register?redirect=${encodeURIComponent(redirectTo)}` : '/register'
  const [expired] = useState(readExpired)
  const idRef = useRef(null)
  const pwRef = useRef(null)
  const inFlight = useRef(false)
  const [id, setId] = useState('')
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null) // { field, title, text }
  const [idEmpty, setIdEmpty] = useState(false)
  usePageMeta('Вход — HiEarth')

  useEffect(() => { if (isLoggedIn) navigate(redirectTo, { replace: true }) }, [isLoggedIn, navigate, redirectTo])
  useEffect(() => { idRef.current?.focus() }, [])

  async function submit(e) {
    e.preventDefault()
    if (inFlight.current) return
    const value = normalizeLogin(id)
    if (!value) { setIdEmpty(true); idRef.current?.focus(); return }
    inFlight.current = true
    setBusy(true)
    setErr(null)
    try {
      const data = await loginUser(value, pw)
      login(data.token)
      navigate(redirectTo, { replace: true })
    } catch (e2) {
      const x = explain(e2, value)
      setErr(x)
      // the person lands on the field to fix (03 §9)
      requestAnimationFrame(() => (x.field === 'pw' ? pwRef.current : idRef.current)?.focus())
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const side = (
    <>
      <Nomerok masked variant="idle" label="номерок" railWidth={320} />
      <p className="t-h3">Номерок с вами?</p>
      <p className="muted t-sm" style={{ maxWidth: 300, textAlign: 'center' }}>HiEarth ID есть в файле, который вы скачали при регистрации, и в шапке кабинета на других устройствах.</p>
    </>
  )

  return (
    <AuthLayout side={side} width={400}>
      <form className="stack gap-5" onSubmit={submit} noValidate>
        <div>
          <h1 className="t-h1 rise">С возвращением</h1>
          <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 8 }}>Войдите в аккаунт HiEarth.</p>
        </div>
        {expired && !err && <Alert tone="info" title="Сессия закончилась">— войдите снова.</Alert>}
        {redirectTo.startsWith('/claim/') && !expired && !err && <Alert tone="info">Войдите или зарегистрируйтесь, чтобы присоединиться к компании.</Alert>}
        {err && <div id="login-err"><Alert tone="bad" title={err.title} rise>{err.text}</Alert></div>}
        <Field label="HiEarth ID или имя пользователя" error={idEmpty ? 'Укажите HiEarth ID или имя пользователя' : null}>
          <Input
            ref={idRef}
            mono
            size="lg"
            value={id}
            invalid={err?.field === 'id'}
            aria-errormessage={err?.field === 'id' ? 'login-err' : undefined}
            onChange={(e) => { setId(e.target.value); setIdEmpty(false); if (err?.field === 'id') setErr(null) }}
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
          />
        </Field>
        <Field label="Пароль" aside="если задавали" hint={err?.field === 'pw' ? null : 'Не задавали пароль — оставьте поле пустым.'}>
          <Input
            ref={pwRef}
            aria-errormessage={err?.field === 'pw' ? 'login-err' : undefined}
            type="password"
            size="lg"
            value={pw}
            placeholder="Ваш пароль"
            invalid={err?.field === 'pw'}
            onChange={(e) => { setPw(e.target.value); if (err?.field === 'pw') setErr(null) }}
            autoComplete="current-password"
          />
        </Field>
        <Button variant="primary" size="lg" block type="submit" loading={busy} className="rise" style={{ '--d': '200ms' }}>Войти</Button>
        <hr className="divider" />
        <p className="t-sm muted">Нет аккаунта? <Link to={registerHref} className="link">Создайте новый</Link> — это один клик.</p>
        <p className="t-sm muted">Потеряли ID или забыли пароль? Напишите в <Link to="/help#lost-id" className="link">поддержку</Link> — найдём аккаунт по платежу и сбросим пароль.</p>
      </form>
    </AuthLayout>
  )
}
