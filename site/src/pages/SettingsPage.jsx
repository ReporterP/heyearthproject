import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount, { invalidateAccount } from '../account/useAccount.js'
import { updateProfile } from '../api/user.js'
import AppShell, { PageHead, LogoutDialog } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import CopyField from '../components/CopyField.jsx'
import EmailCard from '../components/EmailCard.jsx'
import Field, { Input } from '../components/Field.jsx'
import Nomerok from '../components/Nomerok.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import { useToast } from '../components/Toast.jsx'
import { Meter, STRENGTH } from './RegisterPage.jsx'
import usePageMeta from '../hooks/usePageMeta.js'
import { currentSub } from '../lib/account.js'
import { date } from '../lib/format.js'

// Настройки (spec 08 §8.4, artboard «Настройки»): sections `set` — title on the left, content right.
function strength(pw) {
  if (!pw) return 0
  let s = 0
  if (pw.length >= 8) s++
  if (pw.length >= 12) s++
  if (/[a-zA-Zа-яА-Я]/.test(pw) && /\d/.test(pw)) s++
  if (/[^\w\s]/.test(pw)) s++
  return s <= 1 ? 1 : s === 2 ? 2 : 3
}

function Section({ title, text, children }) {
  return (
    <section className="set">
      <div><h2 className="t-h4">{title}</h2>{text && <p className="t-sm subtle" style={{ marginTop: 4 }}>{text}</p>}</div>
      <div style={{ minWidth: 0 }}>{children}</div>
    </section>
  )
}

export default function SettingsPage() {
  const { token } = useAuth()
  const toast = useToast()
  usePageMeta('Настройки — HiEarth', { noindex: true })
  const acc = useAccount(['profile', 'subscription'], { optional: ['subscription'] })
  const profile = acc.data.profile
  const sub = currentSub(acc.data.subscription)
  const showSkel = useDelayed(acc.loading && !profile, 300)
  const [logout, setLogout] = useState(false)

  // the login + password form
  const [username, setUsername] = useState('')
  const [cur, setCur] = useState('')
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [tried, setTried] = useState(false)
  const [srvErr, setSrvErr] = useState(null) // { field?, text, title? }
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)
  useEffect(() => { if (profile) setUsername(profile.username || '') }, [profile?.username]) // eslint-disable-line react-hooks/exhaustive-deps

  const hasPw = !!profile?.has_password
  const nameChanged = profile && username.trim() !== (profile.username || '')
  const pwChanged = pw.length > 0
  const changed = !!(nameChanged || pwChanged)
  const errs = {
    username: nameChanged && (!hasPw && !pwChanged ? 'Сначала задайте пароль — без него логин не сохранится'
      : username.trim().length < 5 ? 'Имя пользователя должно быть не менее 5 символов'
      : !/^[A-Za-z0-9_-]+$/.test(username.trim()) ? 'Имя может содержать только латинские буквы, цифры, «_» и «-»' : null),
    cur: pwChanged && hasPw && !cur ? 'Введите текущий пароль' : null,
    pw: pwChanged && pw.length < 4 ? 'Пароль должен быть не менее 4 символов' : null,
    pw2: pwChanged && pw2 !== pw ? 'Пароли не совпадают' : null,
  }
  const show = (k) => (tried && errs[k]) || (srvErr?.field === k && srvErr.text) || null
  const s = strength(pw)

  async function save(e) {
    e.preventDefault()
    if (!changed || inFlight.current) return
    setTried(true)
    setSrvErr(null)
    if (Object.values(errs).some(Boolean)) return
    inFlight.current = true
    setBusy(true)
    try {
      const payload = {}
      if (nameChanged) payload.username = username.trim()
      if (pwChanged) { payload.newPassword = pw; if (hasPw) payload.currentPassword = cur }
      await updateProfile(token, payload)
      invalidateAccount('profile')
      await acc.reload('profile')
      setCur(''); setPw(''); setPw2(''); setTried(false)
      toast.ok('Изменения сохранены')
    } catch (e2) {
      const raw = String(e2?.data?.error || '').toLowerCase()
      if (raw === 'username already taken') setSrvErr({ field: 'username', text: 'Это имя уже занято — попробуйте другое' })
      else if (raw.startsWith('username can only contain')) setSrvErr({ field: 'username', text: 'Имя может содержать только латинские буквы, цифры, «_» и «-»' })
      else if (raw.includes('облачный пароль')) setSrvErr({ field: 'username', text: 'Сначала задайте пароль — без него логин не сохранится' })
      else if (raw.includes('имя пользователя должно быть')) setSrvErr({ field: 'username', text: 'Имя пользователя должно быть не менее 5 символов' })
      else if (raw === 'invalid current password') setSrvErr({ field: 'cur', text: 'Неверный текущий пароль' })
      else if (raw === 'current password required') setSrvErr({ field: 'cur', text: 'Введите текущий пароль' })
      else if (raw.startsWith('password must be at least')) setSrvErr({ field: 'pw', text: 'Пароль должен быть не менее 4 символов' })
      else setSrvErr({ title: 'Не удалось сохранить.', text: 'Попробуйте снова.' })
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  let body
  if (acc.error && acc.error.kind !== 'auth' && !profile) {
    body = <Alert tone="bad" title="Не удалось загрузить профиль.">Обновите страницу. <button type="button" className="link" onClick={() => acc.reload()} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Повторить</button></Alert>
  } else if (!profile) {
    body = <div className="stack gap-4" aria-busy="true">{showSkel && [0, 1, 2].map((i) => <SkeletonCard key={i} lines={3} />)}</div>
  } else {
    body = (
      <>
        <Section title="Номерок" text="Единственный ключ от аккаунта. Восстановить нельзя.">
          <div className="card row gap-6 max-sm:flex-col" style={{ alignItems: 'center' }}>
            <div style={{ width: 200, flex: 'none' }} aria-hidden="true">
              <Nomerok id={profile.user_id} variant="idle" label="номерок" railWidth={200} idSize={13} />
            </div>
            <div className="stack gap-3" style={{ flex: 1, minWidth: 0, width: '100%' }}>
              <CopyField value={profile.user_id} label="HiEarth ID" buttonLabel="Скопировать" />
              <dl className="kv" style={{ gridTemplateColumns: 'minmax(0, 160px) 1fr' }}>
                <dt>Имя пользователя</dt><dd className={profile.username ? 'mono' : 'subtle'}>{profile.username || 'не задано'}</dd>
                <dt>Пароль</dt><dd>{hasPw ? <Badge tone="ok">Установлен</Badge> : <Badge tone="plain">Не установлен</Badge>}</dd>
                <dt>Подписка</dt>
                <dd>{sub?.active ? `${sub.plan_name} · до ${date(sub.valid_until)}` : <>Нет активной подписки · <Link to="/payment" className="link">Выбрать тариф</Link></>}</dd>
              </dl>
            </div>
          </div>
        </Section>

        <Section title={profile.username || hasPw ? 'Логин и пароль' : 'Создать логин и пароль'} text="Входить по короткому имени вместо длинного HiEarth ID.">
          <form className="card stack gap-4" onSubmit={save} noValidate>
            {srvErr && !srvErr.field && <Alert tone="bad" title={srvErr.title} rise>{srvErr.text}</Alert>}
            <Field label="Имя пользователя" hint="минимум 5 символов" error={show('username')}>
              <Input value={username} autoComplete="username" autoCapitalize="none" spellCheck={false} onChange={(e) => { setUsername(e.target.value); setSrvErr(null) }} />
            </Field>
            {hasPw && (
              <Field label="Текущий пароль" error={show('cur')}>
                <Input type="password" value={cur} placeholder="Нужен, чтобы сменить пароль" autoComplete="current-password" onChange={(e) => { setCur(e.target.value); setSrvErr(null) }} />
              </Field>
            )}
            <div className="grid g-2" style={{ gap: 12 }}>
              <div className="stack gap-2">
                <Field label={hasPw ? 'Новый пароль' : 'Пароль'} error={show('pw')} hint={!show('pw') && s ? STRENGTH[s].label : null} hintTone={s ? STRENGTH[s].tone : ''}>
                  <Input type="password" value={pw} placeholder={hasPw ? 'Оставьте пустым, чтобы не менять' : 'Придумайте пароль'} autoComplete="new-password" onChange={(e) => { setPw(e.target.value); setSrvErr(null) }} />
                </Field>
                <Meter value={s} />
              </div>
              <Field label="Повторите пароль" error={show('pw2')}>
                <Input type="password" value={pw2} autoComplete="new-password" onChange={(e) => setPw2(e.target.value)} />
              </Field>
            </div>
            {changed ? (
              <div><Button variant="primary" type="submit" loading={busy}>{busy ? 'Сохраняем…' : 'Сохранить изменения'}</Button></div>
            ) : (
              <div className="row wrap gap-3"><Button variant="secondary" disabled>Нет изменений</Button><span className="t-sm subtle">Кнопка оживёт, когда что-то поменяете</span></div>
            )}
          </form>
        </Section>

        <Section title="Почта для чеков и напоминаний" text="Пришлём чек об оплате с кошелька и напомним за 3 дня до конца подписки. Больше ничего.">
          <EmailCard token={token} />
        </Section>

        <Section title="Выход" text="Для входа понадобится HiEarth ID или логин.">
          <Button variant="danger" icon="logout" onClick={() => setLogout(true)}>Выйти из аккаунта</Button>
        </Section>
        <LogoutDialog open={logout} onClose={() => setLogout(false)} profile={profile} />
      </>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 1080 }}>
        <PageHead title="Настройки" lead="Аккаунт, вход и почта для чеков." />
        {body}
      </div>
    </AppShell>
  )
}

