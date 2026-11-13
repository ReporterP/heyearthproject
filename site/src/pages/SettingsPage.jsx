import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { updateProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount, { invalidateAccount } from '../account/useAccount.js'
import EmailCard from '../components/EmailCard.jsx'
import PasswordInput from '../components/PasswordInput.jsx'
import { copyToClipboard } from '../components/CopyButton.jsx'
import { Skeleton } from '../components/Skeleton.jsx'
import LoadErrorCard from '../components/LoadErrorCard.jsx'

function strengthOf(pw) {
  if (!pw) return null
  let score = 0
  if (pw.length >= 8) score++
  if (pw.length >= 12) score++
  if (/[a-zA-Zа-яА-Я]/.test(pw) && /\d/.test(pw)) score++
  if (/[^\w\s]/.test(pw)) score++
  if (score <= 1) return { label: 'Слабый', pct: 33, cls: 'bg-danger', text: 'text-danger' }
  if (score === 2) return { label: 'Средний', pct: 66, cls: 'bg-accent', text: 'text-accent' }
  return { label: 'Надёжный', pct: 100, cls: 'bg-brand', text: 'text-brand' }
}

/* One row of read-only account data */
function InfoRow({ label, children }) {
  return (
    <div className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-brand/8">
      <span className="text-xs text-text-secondary flex-shrink-0">{label}</span>
      <span className="text-sm text-right min-w-0">{children}</span>
    </div>
  )
}

export default function SettingsPage() {
  const navigate = useNavigate()
  const { token, logout } = useAuth()

  const [loading, setLoading] = useState(true)
  const [profile, setProfile] = useState(null)
  const [username, setUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [currentPassword, setCurrentPassword] = useState('')
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState(false)
  const [confirmLogout, setConfirmLogout] = useState(false)
  const [loadError, setLoadError] = useState('')

  const account = useAccount(['profile'])
  useEffect(() => {
    if (account.data.profile) {
      setProfile(account.data.profile)
      setUsername(account.data.profile.username || '')
    }
    if (!account.loading) setLoading(false)
    if (account.error && account.error.kind !== 'auth') setLoadError(account.error.message || 'Не удалось загрузить профиль — обновите страницу.')
  }, [account.data.profile, account.loading, account.error])

  const passwordIsSet = profile?.has_password
  const usernameIsSet = profile?.username && profile.username.length > 0
  const strength = strengthOf(newPassword)

  // Nothing to save → button stays disabled (no "Нечего сохранять" dead-end)
  const usernameChanged = (username.trim() || '') !== (profile?.username || '')
  const changingPassword = newPassword.length > 0
  const hasChanges = usernameChanged || changingPassword

  async function saveAll() {
    setMsg(''); setErr('')
    if (!hasChanges) return

    if (usernameChanged) {
      const u = username.trim()
      if (u && u.length < 5) { setErr('Имя пользователя должно быть не менее 5 символов'); return }
    }
    if (changingPassword) {
      if (newPassword.length < 4) { setErr('Пароль должен быть не менее 4 символов'); return }
      if (newPassword !== confirmPassword) { setErr('Пароли не совпадают'); return }
      if (profile?.has_password && !currentPassword) { setErr('Введите текущий пароль'); return }
    }

    setSaving(true)
    try {
      const payload = {}
      if (usernameChanged) payload.username = username.trim()
      if (changingPassword) {
        payload.newPassword = newPassword
        if (profile?.has_password) payload.currentPassword = currentPassword
      }
      const data = await updateProfile(token, payload)
      setProfile(data)
      invalidateAccount('profile')
      setNewPassword(''); setConfirmPassword(''); setCurrentPassword('')
      setMsg('Изменения сохранены')
      setTimeout(() => setMsg(''), 4000)
    } catch (e) {
      setErr(e.message || 'Не удалось сохранить. Попробуйте снова.')
    } finally {
      setSaving(false)
    }
  }

  function handleLogout() {
    logout()
    navigate('/')
  }

  async function copyId() {
    const ok = await copyToClipboard(profile?.user_id || '')
    if (ok) { setCopied(true); setTimeout(() => setCopied(false), 2000) }
  }

  if (loadError) return <LoadErrorCard message={loadError} />

  return (
    <section className="relative py-12 md:py-16 overflow-hidden">
      <div className="pointer-events-none absolute top-1/3 -left-24 w-80 h-80 rounded-full bg-brand/8 blur-[110px]" />

      <div className="relative max-w-lg mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8 animate-fade-in">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Настройки</h1>
            <p className="text-sm text-text-secondary mt-1">Управление аккаунтом</p>
          </div>
          <Link to="/dashboard" className="btn-ghost !py-2 !px-4 !text-sm">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" />
            </svg>
            Назад
          </Link>
        </div>

        {loading ? (
          <div className="space-y-4">
            <Skeleton className="h-48 !rounded-[22px]" />
            <Skeleton className="h-64 !rounded-[22px]" />
          </div>
        ) : (
          <>
            {/* ─── Аккаунт ─── */}
            <div className="card rounded-[22px] p-6 mb-4 animate-fade-in-up">
              <h2 className="serif text-base font-semibold mb-1">Аккаунт</h2>
              <p className="text-xs text-text-secondary mb-4">Данные вашего профиля</p>
              <div className="space-y-2.5">
                <div className="flex items-center justify-between gap-3 p-3 rounded-xl bg-white/[0.03] border border-brand/8">
                  <span className="text-xs text-text-secondary flex-shrink-0">HiEarth ID</span>
                  <button onClick={copyId} className="flex items-center gap-2 min-w-0 group" title="Скопировать">
                    <span className="font-mono text-sm text-brand truncate">{profile?.user_id || '—'}</span>
                    {copied
                      ? <svg className="w-4 h-4 text-brand flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.4}><path strokeLinecap="round" strokeLinejoin="round" d="M5 12l5 5L20 6" /></svg>
                      : <svg className="w-4 h-4 text-faint group-hover:text-text-secondary flex-shrink-0 transition" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25" /></svg>}
                  </button>
                </div>
                {usernameIsSet && <InfoRow label="Имя пользователя"><span className="font-medium">@{profile.username}</span></InfoRow>}
                <InfoRow label="Пароль">
                  <span className={passwordIsSet ? 'text-brand' : 'text-accent'}>{passwordIsSet ? 'Установлен' : 'Не установлен'}</span>
                </InfoRow>
                {profile?.current_plan && (
                  <InfoRow label="Подписка"><span className="font-medium text-brand">{profile.current_plan}</span></InfoRow>
                )}
              </div>
            </div>

            {/* ─── Вход в аккаунт ─── */}
            <div className="card rounded-[22px] p-6 mb-4 animate-fade-in-up delay-100">
              <h2 className="serif text-base font-semibold mb-1">{passwordIsSet || usernameIsSet ? 'Логин и пароль' : 'Создать логин и пароль'}</h2>
              <p className="text-xs text-text-secondary mb-5">Позволяет входить по короткому имени вместо длинного HiEarth ID</p>

              <div className="space-y-4">
                <div>
                  <label htmlFor="set-username" className="block text-xs font-medium text-text-secondary mb-2">Имя пользователя</label>
                  <input
                    id="set-username"
                    type="text"
                    value={username}
                    onChange={e => setUsername(e.target.value)}
                    placeholder="минимум 5 символов"
                    autoComplete="username"
                    className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brand/25 transition-all duration-300 text-sm placeholder:text-faint"
                  />
                </div>

                {passwordIsSet && (
                  <PasswordInput
                    value={currentPassword}
                    onChange={e => setCurrentPassword(e.target.value)}
                    placeholder="Текущий пароль"
                    label="Текущий пароль"
                    autoComplete="current-password"
                  />
                )}

                <div>
                  <PasswordInput
                    value={newPassword}
                    onChange={e => setNewPassword(e.target.value)}
                    placeholder={passwordIsSet ? 'Оставьте пустым, чтобы не менять' : 'Придумайте пароль'}
                    label={passwordIsSet ? 'Новый пароль' : 'Пароль'}
                    autoComplete="new-password"
                  />
                  {strength && (
                    <div className="mt-1.5">
                      <div className="h-1 rounded-full bg-white/8 overflow-hidden">
                        <div className={`h-full ${strength.cls} transition-all duration-300`} style={{ width: `${strength.pct}%` }} />
                      </div>
                      <p className={`text-[11px] mt-1 ${strength.text}`}>{strength.label}{strength.pct < 100 && ' — длиннее 8 символов надёжнее'}</p>
                    </div>
                  )}
                </div>

                {newPassword.length > 0 && (
                  <div className="animate-fade-in">
                    <PasswordInput
                      value={confirmPassword}
                      onChange={e => setConfirmPassword(e.target.value)}
                      placeholder="Повторите пароль"
                      label="Подтвердите пароль"
                      autoComplete="new-password"
                    />
                    {confirmPassword && confirmPassword !== newPassword && (
                      <p className="text-[11px] text-danger mt-1.5">Пароли не совпадают</p>
                    )}
                  </div>
                )}
              </div>

              {msg && (
                <div role="status" aria-live="polite" className="flex items-center gap-2 mt-4 animate-fade-in">
                  <svg className="w-4 h-4 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                  <p className="text-brand text-sm">{msg}</p>
                </div>
              )}
              {err && (
                <div role="alert" className="flex items-center gap-2 mt-4 animate-fade-in">
                  <svg className="w-4 h-4 text-danger" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
                  <p className="text-danger text-sm">{err}</p>
                </div>
              )}

              <button disabled={saving || !hasChanges} className="btn-primary w-full mt-5 !py-3 disabled:opacity-40" onClick={saveAll}>
                {saving ? (
                  <>
                    <svg className="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Сохраняем…
                  </>
                ) : hasChanges ? 'Сохранить изменения' : 'Нет изменений'}
              </button>
            </div>

            <EmailCard token={token} />

            {/* ─── Danger zone: separated from everyday actions ─── */}
            <div className="card rounded-[22px] p-6 animate-fade-in-up delay-200 !border-danger/15">
              <h2 className="serif text-base font-semibold mb-1">Выход</h2>
              <p className="text-xs text-text-secondary mb-4">
                Для входа снова понадобится HiEarth ID{usernameIsSet ? ' или ваш логин' : ''}. Убедитесь, что он сохранён.
              </p>
              {!confirmLogout ? (
                <button
                  className="w-full py-3 rounded-xl bg-danger/8 hover:bg-danger/15 border border-danger/20 hover:border-danger/35 text-danger text-sm font-medium transition-all duration-300 flex items-center justify-center gap-2"
                  onClick={() => setConfirmLogout(true)}
                >
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
                  </svg>
                  Выйти из аккаунта
                </button>
              ) : (
                <div className="animate-fade-in">
                  <p className="text-sm text-text-primary mb-3">Точно выйти?</p>
                  <div className="flex gap-3">
                    <button onClick={() => setConfirmLogout(false)} className="btn-ghost flex-1 !py-2.5 !text-sm">Остаться</button>
                    <button onClick={handleLogout} className="flex-1 py-2.5 rounded-full bg-danger text-white text-sm font-semibold hover:brightness-110 active:scale-[0.97] transition">Выйти</button>
                  </div>
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </section>
  )
}
