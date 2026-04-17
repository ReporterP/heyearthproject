import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getProfile, updateProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import PasswordInput from '../components/PasswordInput.jsx'

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

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    getProfile(token)
      .then(data => {
        setProfile(data)
        setUsername(data.username || '')
      })
      .catch(e => {
        if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
          logout()
          navigate('/login')
        }
      })
      .finally(() => setLoading(false))
  }, [])

  async function saveAll() {
    setMsg(''); setErr('')
    const usernameChanged = (username.trim() || '') !== (profile?.username || '')
    const changingPassword = newPassword.length > 0

    if (!usernameChanged && !changingPassword) {
      setErr('Нечего сохранять')
      return
    }

    if (usernameChanged) {
      const u = username.trim()
      if (u && u.length < 5) { setErr('Имя пользователя должно быть не менее 5 символов'); return }
    }

    if (changingPassword) {
      if (newPassword.length < 4) { setErr('Пароль должен быть не менее 4 символов'); return }
      if (newPassword !== confirmPassword) { setErr('Пароли не совпадают'); return }
      if (profile?.has_password && !currentPassword) { setErr('Введите текущий пароль'); return }
    }

    // Если меняется только username но пароля нет — нужно ТРЕБОВАТЬ установить пароль
    if (usernameChanged && !profile?.has_password && !changingPassword) {
      setErr('Чтобы задать имя пользователя, также установите пароль')
      return
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
      setNewPassword(''); setConfirmPassword(''); setCurrentPassword('')
      setMsg('Сохранено')
    } catch (e) {
      setErr(e.message || 'Ошибка сохранения')
    } finally {
      setSaving(false)
    }
  }

  function handleLogout() {
    logout()
    navigate('/')
  }

  const passwordIsSet = profile?.has_password
  const usernameIsSet = profile?.username && profile.username.length > 0

  return (
    <section className="relative py-16 md:py-24 overflow-hidden">
      <div className="absolute top-1/3 -left-32 w-80 h-80 rounded-full bg-brand/8 blur-[100px]" />
      <div className="absolute bottom-1/3 -right-32 w-80 h-80 rounded-full bg-accent/8 blur-[100px]" />

      <div className="relative max-w-lg mx-auto px-4 sm:px-6">
        {/* Header */}
        <div className="flex items-center justify-between mb-8 animate-fade-in">
          <div>
            <h1 className="text-2xl md:text-3xl font-bold">Настройки</h1>
            <p className="text-sm text-text-secondary mt-1">Управление аккаунтом</p>
          </div>
          <Link
            to="/dashboard"
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl glass hover:bg-white/10 text-sm text-text-secondary hover:text-text-primary transition-all duration-200"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" />
            </svg>
            Назад
          </Link>
        </div>

        {loading ? (
          <div className="flex items-center justify-center py-16">
            <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
          </div>
        ) : (
          <>
            {/* ─── Account info ─── */}
            <div className="glass-card rounded-2xl p-6 mb-4 animate-fade-in-up delay-100">
              <div className="flex items-center gap-3 mb-4">
                <div className="w-9 h-9 rounded-xl bg-brand/10 flex items-center justify-center">
                  <svg className="w-4.5 h-4.5 text-brand-light" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15 9h3.75M15 12h3.75M15 15h3.75M4.5 19.5h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 19.5zm6-10.125a1.875 1.875 0 11-3.75 0 1.875 1.875 0 013.75 0zm1.294 6.336a6.721 6.721 0 01-3.17.789 6.721 6.721 0 01-3.168-.789 3.376 3.376 0 016.338 0z" />
                  </svg>
                </div>
                <h2 className="text-base font-semibold">Аккаунт</h2>
              </div>
              <div className="space-y-3">
                <div className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-white/5">
                  <span className="text-xs text-text-secondary">HeYearth ID</span>
                  <span className="font-mono text-sm text-brand-light break-all ml-2">{profile?.user_id || '...'}</span>
                </div>
                {usernameIsSet && (
                  <div className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-white/5">
                    <span className="text-xs text-text-secondary">Имя пользователя</span>
                    <span className="text-sm font-medium">@{profile.username}</span>
                  </div>
                )}
                <div className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-white/5">
                  <span className="text-xs text-text-secondary">Пароль</span>
                  <span className={`text-sm ${passwordIsSet ? 'text-green-400' : 'text-amber-400'}`}>
                    {passwordIsSet ? 'Установлен' : 'Не установлен'}
                  </span>
                </div>
                {profile?.current_plan && (
                  <div className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-white/5">
                    <span className="text-xs text-text-secondary">Подписка</span>
                    <span className="text-sm font-medium text-green-400">{profile.current_plan}</span>
                  </div>
                )}
              </div>
            </div>

            {/* ─── Единая форма: логин + пароль ─── */}
            <div className="glass-card rounded-2xl p-6 mb-4 animate-fade-in-up delay-200">
              <div className="flex items-center gap-3 mb-5">
                <div className="w-9 h-9 rounded-xl bg-brand/10 flex items-center justify-center">
                  <svg className="w-4.5 h-4.5 text-brand-light" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
                  </svg>
                </div>
                <div>
                  <h2 className="text-base font-semibold">{passwordIsSet || usernameIsSet ? 'Логин и пароль' : 'Создать логин и пароль'}</h2>
                  <p className="text-xs text-text-secondary">Позволяет входить по короткому username вместо длинного HeYearth ID</p>
                </div>
              </div>

              <div className="space-y-4">
                {/* Username */}
                <div>
                  <label className="block text-xs font-medium text-text-secondary mb-2">Имя пользователя (username)</label>
                  <input
                    type="text"
                    value={username}
                    onChange={e => setUsername(e.target.value)}
                    placeholder="username"
                    autoComplete="username"
                    className="w-full px-4 py-3 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:bg-white/[0.07] focus:outline-none focus:ring-1 focus:ring-brand/30 transition-all duration-300 text-sm placeholder:text-text-secondary/50"
                  />
                </div>

                {/* Current password (только если уже установлен) */}
                {passwordIsSet && (
                  <PasswordInput
                    value={currentPassword}
                    onChange={e => setCurrentPassword(e.target.value)}
                    placeholder="Текущий пароль"
                    label="Текущий пароль (если меняете настройки)"
                    autoComplete="current-password"
                  />
                )}

                {/* New password */}
                <PasswordInput
                  value={newPassword}
                  onChange={e => setNewPassword(e.target.value)}
                  placeholder={passwordIsSet ? 'Оставьте пустым чтобы не менять' : 'Установите пароль'}
                  label={passwordIsSet ? 'Новый пароль' : 'Пароль'}
                  autoComplete="new-password"
                />

                {/* Confirm — только если ввели newPassword */}
                {newPassword.length > 0 && (
                  <PasswordInput
                    value={confirmPassword}
                    onChange={e => setConfirmPassword(e.target.value)}
                    placeholder="Повторите пароль"
                    label="Подтвердите пароль"
                    autoComplete="new-password"
                  />
                )}
              </div>

              {msg && (
                <div className="flex items-center gap-2 mt-3 animate-fade-in">
                  <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                  </svg>
                  <p className="text-green-400 text-sm">{msg}</p>
                </div>
              )}
              {err && (
                <div className="flex items-center gap-2 mt-3 animate-fade-in">
                  <svg className="w-4 h-4 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                  <p className="text-red-400 text-sm">{err}</p>
                </div>
              )}

              <button
                disabled={saving}
                className="group relative mt-5 w-full py-3 rounded-xl font-semibold text-sm text-white overflow-hidden transition-all duration-300 disabled:opacity-50 hover:shadow-lg hover:shadow-brand/20"
                onClick={saveAll}
              >
                <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark" />
                <span className="absolute inset-0 bg-gradient-to-r from-brand-light to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
                <span className="relative z-10 flex items-center justify-center gap-2">
                  {saving ? (
                    <>
                      <svg className="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                      </svg>
                      Сохранение...
                    </>
                  ) : 'Сохранить'}
                </span>
              </button>
            </div>

            {/* ─── Logout ─── */}
            <button
              className="w-full mt-2 py-3.5 rounded-xl glass hover:bg-red-500/10 border border-red-500/20 hover:border-red-500/30 text-red-400 text-sm font-medium transition-all duration-300 flex items-center justify-center gap-2 animate-fade-in-up delay-400"
              onClick={handleLogout}
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
              </svg>
              Выйти из аккаунта
            </button>
          </>
        )}
      </div>
    </section>
  )
}
