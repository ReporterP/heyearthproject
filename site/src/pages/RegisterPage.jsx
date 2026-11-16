import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { registerUser, updateProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { safeRedirect } from '../lib/redirect.js'
import { pendingReferral, forgetReferral } from '../lib/referral.js'
import { fetchPlans } from '../lib/plans.js'
import { copyText } from '../lib/clipboard.js'
import { kopNum } from '../lib/format.js'
import AuthLayout from '../components/AuthLayout.jsx'
import Field, { Input } from '../components/Field.jsx'
import Button from '../components/Button.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import CopyField from '../components/CopyField.jsx'
import { Checkbox } from '../components/Controls.jsx'
import Nomerok from '../components/Nomerok.jsx'
import { useToast } from '../components/Toast.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'

// Регистрация (spec 07 §7.1–7.2, artboards «Регистрация · шаг 1» and «ID выдан»).
// The account is created at once, but the site signs in only on «Продолжить в кабинет»: the screen
// with the ID — the only key to the account — stays until the person has saved it. The pending
// registration lives in sessionStorage, so reloading step 2 does not lose the ID.
const PENDING = 'he_pending_registration'

function readPending() {
  try { return JSON.parse(sessionStorage.getItem(PENDING) || 'null') } catch { return null }
}
function writePending(v) {
  try { if (v) sessionStorage.setItem(PENDING, JSON.stringify(v)); else sessionStorage.removeItem(PENDING) } catch { /* storage off */ }
}

function strength(pw) {
  if (!pw) return 0
  let score = 0
  if (pw.length >= 8) score++
  if (pw.length >= 12) score++
  if (/[a-zA-Zа-яА-Я]/.test(pw) && /\d/.test(pw)) score++
  if (/[^\w\s]/.test(pw)) score++
  return score <= 1 ? 1 : score === 2 ? 2 : 3
}
export const STRENGTH = { 1: { label: 'Слабый', tone: 'bad' }, 2: { label: 'Средний', tone: 'warn' }, 3: { label: 'Надёжный', tone: 'ok' } }

export function Meter({ value }) {
  if (!value) return null
  return <div className={`meter meter--${value}`} aria-hidden="true"><span /><span /><span /></div>
}

function useRefTerms() {
  const [t, setT] = useState(null)
  useEffect(() => {
    fetch('/api/v1/referral/terms').then((r) => (r.ok ? r.json() : null)).then(setT).catch(() => setT(null))
  }, [])
  return t
}

function downloadIdFile(id) {
  const d = new Date().toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' })
  const body = `HiEarth ID\n${id}\n\nВыдан ${d}.\nЭто ваш единственный ключ от аккаунта — восстановить его нельзя.\nВход: https://hiearth.ru/login\n`
  const href = URL.createObjectURL(new Blob([body], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = href
  a.download = 'hiearth-id.txt'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(href)
}

export default function RegisterPage() {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const { login, isLoggedIn } = useAuth()
  const toast = useToast()
  const narrow = useMedia('(max-width: 1024px)')
  const plan = params.get('plan')
  const redirectTo = safeRedirect(params.get('redirect') || params.get('next'))
  const afterwards = plan ? `/payment?plan=${encodeURIComponent(plan)}` : redirectTo
  usePageMeta('Регистрация — HiEarth')

  const [pending, setPending] = useState(readPending) // { id, token } after step 1
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const [hasFree, setHasFree] = useState(false)
  const terms = useRefTerms()
  const refCode = useMemo(() => pendingReferral(), [])

  useEffect(() => { if (isLoggedIn) navigate(afterwards, { replace: true }) }, [isLoggedIn]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { fetchPlans().then((l) => setHasFree(l.some((p) => Number(p.price_rub) === 0))).catch(() => {}) }, [])

  async function create() {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const data = await registerUser('web', '', pendingReferral())
      forgetReferral()
      const next = { id: data.user_id || data.id || '', token: data.token || '' }
      writePending(next)
      setPending(next)
    } catch (e) {
      setError(e?.kind === 'rate' || e?.status === 429
        ? { title: 'Слишком много попыток.', text: 'Подождите минуту и попробуйте снова.' }
        : { title: 'Не получилось создать аккаунт.', text: 'Попробуйте ещё раз через минуту.' })
    } finally {
      setBusy(false)
    }
  }

  if (pending?.id) {
    return <Issued pending={pending} afterwards={afterwards} narrow={narrow} onDone={() => { writePending(null); if (pending.token) login(pending.token); navigate(afterwards, { replace: true }) }} toast={toast} />
  }

  const refOn = refCode && terms?.enabled && terms.friend_kop > 0
  const side = (
    <>
      <div style={{ width: 320 }}>
        <div className="rail" />
        <div style={{ display: 'flex', justifyContent: 'center' }}><div className="ghost-hook" /></div>
      </div>
      <p className="t-h3" style={{ marginTop: 12 }}>Здесь появится ваш номерок</p>
      <p className="muted t-sm" style={{ maxWidth: 300, textAlign: 'center', marginTop: -16 }}>HiEarth ID вместо имени, почты и телефона. Только он открывает аккаунт.</p>
    </>
  )

  return (
    <AuthLayout side={side}>
      <div>
        <h1 className="t-h1 rise">Получить HiEarth ID</h1>
        <p className="t-lead rise" style={{ '--d': '120ms', marginTop: 10 }}>Анонимная регистрация за один клик.</p>
      </div>
      <ul className="plan__list rise" style={{ '--d': '160ms', margin: 0, fontSize: 15 }}>
        <li>Не нужен email или телефон</li>
        <li>Мгновенная активация</li>
        {hasFree && <li>Первая неделя — бесплатно, без карты</li>}
      </ul>
      {refOn && (
        <Alert tone="ok" icon="gift" title="Вас пригласил друг" className="rise" style={{ '--d': '200ms' }}>
          {terms.referrer_kop === terms.friend_kop
            ? `— после первой оплаты вам обоим придёт по ${kopNum(terms.friend_kop)} ₽.`
            : `— после первой оплаты вам придёт ${kopNum(terms.friend_kop)} ₽.`}
        </Alert>
      )}
      {error && <Alert tone="bad" title={error.title} rise>{error.text}</Alert>}
      <Button variant="primary" size="lg" block iconRight="arrowRight" loading={busy} onClick={create} className="rise" style={{ '--d': '200ms' }}>Получить HiEarth ID</Button>
      <p className="t-sm muted" style={{ textAlign: 'center' }}>Уже есть аккаунт? <Link to={redirectTo !== '/dashboard' ? `/login?redirect=${encodeURIComponent(redirectTo)}` : '/login'} className="link">Войти</Link></p>
      <p className="t-xs subtle" style={{ textAlign: 'center' }}>
        Нажимая кнопку, вы принимаете <a href="/offer" target="_blank" rel="noopener" className="link">договор-оферту</a> и <a href="/privacy" target="_blank" rel="noopener" className="link">политику конфиденциальности</a>.
      </p>
    </AuthLayout>
  )
}

// Step 2 — «Аккаунт создан»
function Issued({ pending, onDone, narrow, toast }) {
  const [saved, setSaved] = useState(false)
  const [creds, setCreds] = useState('open') // open | skipped | saved
  const [login, setLogin] = useState('')
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [touched, setTouched] = useState({})
  const [tried, setTried] = useState(false)
  const [serverErr, setServerErr] = useState(null) // { field?, text }
  const [busy, setBusy] = useState(false)
  const [savedLogin, setSavedLogin] = useState('')
  const [copiedBtn, setCopiedBtn] = useState(false)

  const errs = {
    login: !login.trim() ? 'Укажите имя пользователя'
      : login.trim().length < 5 ? 'Имя пользователя должно быть не менее 5 символов'
        : !/^[A-Za-z0-9_-]+$/.test(login.trim()) ? 'Имя может содержать только латинские буквы, цифры, «_» и «-»' : null,
    pw: pw.length < 4 ? 'Пароль должен быть не менее 4 символов' : null,
    pw2: pw2 !== pw ? 'Пароли не совпадают' : null,
  }
  const show = (k) => (tried || touched[k]) && errs[k]
  const s = strength(pw)

  async function save() {
    setTried(true)
    setServerErr(null)
    if (errs.login || errs.pw || errs.pw2) return
    setBusy(true)
    try {
      await updateProfile(pending.token, { username: login.trim(), newPassword: pw })
      setSavedLogin(login.trim())
      setCreds('saved')
      toast.ok('Изменения сохранены')
    } catch (e) {
      const raw = String(e?.data?.error || '').toLowerCase()
      if (raw === 'username already taken') setServerErr({ field: 'login', text: 'Это имя уже занято — попробуйте другое' })
      else if (raw.startsWith('username can only contain')) setServerErr({ field: 'login', text: 'Имя может содержать только латинские буквы, цифры, «_» и «-»' })
      else setServerErr({ text: 'Попробуйте снова.', title: 'Не удалось сохранить.' })
    } finally {
      setBusy(false)
    }
  }

  async function copyBtn() {
    if (await copyText(pending.id)) {
      setCopiedBtn(true)
      setTimeout(() => setCopiedBtn(false), 2000)
    }
  }

  const side = (
    <>
      <Nomerok id={pending.id} variant="swing" label="ваш номерок" railWidth={narrow ? 300 : 340} idSize={narrow ? 20 : 24} />
    </>
  )

  return (
    <AuthLayout reveal side={side} sideLabel="Ваш номерок" width={460}>
      <div>
        <h1 className="t-h1">Аккаунт создан</h1>
        <p className="t-lead" style={{ marginTop: 10 }}>
          {narrow ? 'Номерок выше' : 'Номерок справа'} — ваш ключ от аккаунта. Сохраните его: восстановить нельзя.
        </p>
      </div>
      <CopyField value={pending.id} label="HiEarth ID" />
      <div className="row gap-2 max-sm:flex-col max-sm:items-stretch">
        <Button variant="secondary" icon="download" onClick={() => downloadIdFile(pending.id)} className="sm:flex-1">Скачать файл с ID</Button>
        <Button variant="secondary" icon="copy" onClick={copyBtn} className="sm:flex-1" aria-live="polite">{copiedBtn ? '✓ Скопировано' : 'Копировать'}</Button>
      </div>

      {creds === 'open' ? (
        <div className="collapse is-open">
          <div>
            <section className="card" style={{ padding: 20 }} aria-labelledby="creds-t">
              <p id="creds-t"><b>Логин и пароль</b> <Badge tone="plain" style={{ marginLeft: 6 }}>необязательно</Badge></p>
              <p className="t-sm muted" style={{ marginTop: 2 }}>Тогда для входа хватит короткого имени вместо длинного ID.</p>
              <div className="stack gap-4" style={{ marginTop: 16 }}>
                {serverErr && !serverErr.field && <Alert tone="bad" title={serverErr.title}>{serverErr.text}</Alert>}
                <Field label="Имя пользователя" hint="минимум 5 символов" error={(serverErr?.field === 'login' && serverErr.text) || show('login')}>
                  <Input value={login} autoComplete="username" autoCapitalize="none" spellCheck={false}
                    onChange={(e) => { setLogin(e.target.value); if (serverErr?.field === 'login') setServerErr(null) }}
                    onBlur={() => setTouched((t) => ({ ...t, login: true }))} />
                </Field>
                <div className="grid g-2" style={{ gap: 12 }}>
                  <div className="stack gap-2">
                    <Field label="Пароль" error={show('pw')} hint={!show('pw') && s ? STRENGTH[s].label : null} hintTone={s ? STRENGTH[s].tone : ''}>
                      <Input type="password" value={pw} autoComplete="new-password" onChange={(e) => setPw(e.target.value)} onBlur={() => setTouched((t) => ({ ...t, pw: true }))} />
                    </Field>
                    <Meter value={s} />
                  </div>
                  <Field label="Повторите пароль" error={show('pw2')}>
                    <Input type="password" value={pw2} autoComplete="new-password" onChange={(e) => setPw2(e.target.value)} onBlur={() => setTouched((t) => ({ ...t, pw2: true }))} />
                  </Field>
                </div>
                <div className="row gap-2">
                  <Button variant="secondary" onClick={save} loading={busy}>Сохранить</Button>
                  <Button variant="ghost" onClick={() => setCreds('skipped')} disabled={busy}>Пропустить</Button>
                </div>
              </div>
            </section>
          </div>
        </div>
      ) : (
        <p className="t-sm muted swap">
          {creds === 'saved'
            ? <>Логин <span className="mono" style={{ color: 'var(--fg)' }}>{savedLogin}</span> сохранён — можно входить по нему.</>
            : 'Логин и пароль можно задать позже в настройках.'}
        </p>
      )}

      <Checkbox checked={saved} onChange={setSaved}>Я сохранил свой HiEarth ID в надёжном месте</Checkbox>
      <div className="stack gap-2">
        <Button variant="primary" size="lg" block disabled={!saved} onClick={onDone}>Продолжить в кабинет</Button>
        {!saved && <p className="t-xs subtle" style={{ textAlign: 'center' }}>Кнопка станет активной, когда отметите галочку</p>}
      </div>
    </AuthLayout>
  )
}
