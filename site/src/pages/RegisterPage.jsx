import { useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { registerUser, updateProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import PasswordInput from '../components/PasswordInput.jsx'

export default function RegisterPage() {
  const navigate = useNavigate()
  const { login } = useAuth()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [generatedId, setGeneratedId] = useState('')
  const [copied, setCopied] = useState(false)
  const [authToken, setAuthToken] = useState('')

  // Optional credentials
  const [showCreds, setShowCreds] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [credsLoading, setCredsLoading] = useState(false)
  const [credsMsg, setCredsMsg] = useState('')
  const [credsErr, setCredsErr] = useState('')
  const [credsSaved, setCredsSaved] = useState(false)

  async function handleRegister() {
    setLoading(true)
    setError('')
    try {
      const data = await registerUser('web', '')
      setGeneratedId(data.id || data.user_id || data.heyearth_id || '')
      if (data.token) {
        setAuthToken(data.token)
        login(data.token)
      }
    } catch (e) {
      setError(e.message || 'Ошибка регистрации. Попробуйте позже.')
    } finally {
      setLoading(false)
    }
  }

  async function copyId() {
    try {
      await navigator.clipboard.writeText(generatedId)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* clipboard unavailable */ }
  }

  async function saveCreds() {
    setCredsErr(''); setCredsMsg('')
    if (!username.trim() || username.trim().length < 5) {
      setCredsErr('Имя пользователя — минимум 5 символов'); return
    }
    if (!password || password.length < 4) {
      setCredsErr('Пароль — минимум 4 символа'); return
    }
    if (password !== confirmPassword) {
      setCredsErr('Пароли не совпадают'); return
    }
    setCredsLoading(true)
    try {
      await updateProfile(authToken, { username: username.trim(), newPassword: password })
      setCredsSaved(true)
      setCredsMsg('Логин и пароль сохранены')
    } catch (e) {
      setCredsErr(e.message || 'Ошибка сохранения')
    } finally {
      setCredsLoading(false)
    }
  }

  return (
    <section className="relative min-h-[calc(100vh-8rem)] flex items-center justify-center py-16 overflow-hidden">
      {/* Background effects */}
      <div className="absolute inset-0">
        <div className="absolute top-1/3 -left-32 w-80 h-80 rounded-full bg-brand/8 blur-[100px]" />
        <div className="absolute bottom-1/3 -right-32 w-80 h-80 rounded-full bg-accent/8 blur-[100px]" />
      </div>

      <div className="relative w-full max-w-md mx-auto px-6">
        <div className="glass-strong rounded-2xl p-8 md:p-10 shadow-2xl shadow-black/20 animate-fade-in-scale">
          {!generatedId ? (
            <>
              {/* Registration form */}
              <div className="flex justify-center mb-8">
                <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-brand to-accent flex items-center justify-center shadow-lg shadow-brand/25">
                  <svg className="w-7 h-7 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19 7.5v3m0 0v3m0-3h3m-3 0h-3m-2.25-4.125a3.375 3.375 0 11-6.75 0 3.375 3.375 0 016.75 0zM4 19.235v-.11a6.375 6.375 0 0112.75 0v.109A12.318 12.318 0 0110.374 21c-2.331 0-4.512-.645-6.374-1.766z" />
                  </svg>
                </div>
              </div>

              <h1 className="text-2xl font-bold text-center mb-1">Регистрация</h1>
              <p className="text-text-secondary text-center text-sm mb-10">
                Получите уникальный HeYearth ID за один клик. Анонимно и бесплатно.
              </p>

              {error && (
                <div className="flex items-start gap-2 p-3 rounded-xl bg-red-500/10 border border-red-500/20 mb-6 animate-fade-in">
                  <svg className="w-5 h-5 text-red-400 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                  </svg>
                  <p className="text-red-400 text-sm">{error}</p>
                </div>
              )}

              <button
                disabled={loading}
                className="group relative w-full py-4 rounded-xl font-bold text-white text-lg overflow-hidden transition-all duration-300 disabled:opacity-50 hover:shadow-xl hover:shadow-brand/25 hover:-translate-y-0.5"
                onClick={handleRegister}
              >
                <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark" />
                <span className="absolute inset-0 bg-gradient-to-r from-brand-light to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
                <span className="relative z-10 flex items-center justify-center gap-2">
                  {loading ? (
                    <>
                      <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                      </svg>
                      Создание...
                    </>
                  ) : (
                    <>
                      Получить HeYearth ID
                      <svg className="w-5 h-5 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                      </svg>
                    </>
                  )}
                </span>
              </button>

              {/* Features list */}
              <div className="mt-8 space-y-3">
                {['Анонимная регистрация', 'Не нужен email или телефон', 'Мгновенная активация'].map(f => (
                  <div key={f} className="flex items-center gap-3 text-sm text-text-secondary">
                    <svg className="w-4 h-4 text-accent flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                    </svg>
                    {f}
                  </div>
                ))}
              </div>

              <div className="relative my-8">
                <div className="absolute inset-0 flex items-center">
                  <div className="w-full h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
                </div>
              </div>

              <p className="text-center text-sm text-text-secondary">
                Уже есть аккаунт?{' '}
                <Link to="/login" className="text-brand-light hover:text-accent font-medium transition-colors duration-200">
                  Войти
                </Link>
              </p>
            </>
          ) : (
            /* Success state */
            <div className="text-center animate-fade-in-scale">
              {/* Success icon */}
              <div className="flex justify-center mb-6">
                <div className="w-16 h-16 rounded-full bg-gradient-to-br from-green-500/20 to-emerald-500/10 flex items-center justify-center">
                  <svg className="w-8 h-8 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </div>

              <h2 className="text-2xl font-bold mb-2">Ваш HeYearth ID</h2>
              <p className="text-text-secondary text-sm mb-6">Сохраните этот ID — он понадобится для входа</p>

              {/* ID display */}
              <div className="relative my-6 p-6 rounded-2xl glass border border-brand/30 glow-brand">
                <p className="text-3xl md:text-4xl font-mono font-extrabold tracking-wider text-brand-light break-all">
                  {generatedId}
                </p>
                {/* Decorative corners */}
                <div className="absolute top-2 left-2 w-3 h-3 border-l-2 border-t-2 border-brand/40 rounded-tl" />
                <div className="absolute top-2 right-2 w-3 h-3 border-r-2 border-t-2 border-brand/40 rounded-tr" />
                <div className="absolute bottom-2 left-2 w-3 h-3 border-l-2 border-b-2 border-brand/40 rounded-bl" />
                <div className="absolute bottom-2 right-2 w-3 h-3 border-r-2 border-b-2 border-brand/40 rounded-br" />
              </div>

              {/* Copy button */}
              <button
                className="inline-flex items-center gap-2 px-6 py-3 rounded-xl glass hover:bg-white/10 text-text-primary font-medium transition-all duration-300 mb-6"
                onClick={copyId}
              >
                {copied ? (
                  <>
                    <svg className="w-5 h-5 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                    </svg>
                    Скопировано!
                  </>
                ) : (
                  <>
                    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
                    </svg>
                    Копировать ID
                  </>
                )}
              </button>

              {/* Warning */}
              <div className="flex items-start gap-3 p-4 rounded-xl bg-amber-500/10 border border-amber-500/20 text-left mb-6">
                <svg className="w-5 h-5 text-amber-400 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                <div>
                  <p className="text-sm font-semibold text-amber-200 mb-0.5">Важно! Сохраните ваш ID!</p>
                  <p className="text-xs text-amber-200/70">Без него вы не сможете войти в аккаунт. Запишите его в надёжное место.</p>
                </div>
              </div>

              {/* Optional: create credentials */}
              {!credsSaved && (
                <div className="mb-6 text-left">
                  {!showCreds ? (
                    <button
                      type="button"
                      onClick={() => setShowCreds(true)}
                      className="w-full py-3 rounded-xl border border-dashed border-white/15 hover:border-brand/40 hover:bg-white/5 text-sm text-text-secondary hover:text-text-primary transition-all flex items-center justify-center gap-2"
                    >
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
                      </svg>
                      Создать логин и пароль (необязательно)
                    </button>
                  ) : (
                    <div className="p-4 rounded-xl bg-white/[0.03] border border-white/10 space-y-3">
                      <div className="flex items-center justify-between">
                        <p className="text-sm font-semibold">Логин и пароль</p>
                        <button onClick={() => setShowCreds(false)} className="text-xs text-text-secondary hover:text-text-primary">Пропустить</button>
                      </div>
                      <p className="text-xs text-text-secondary">Удобнее входить по короткому username вместо длинного HeYearth ID. Можно задать позже в настройках.</p>
                      <input
                        type="text"
                        value={username}
                        onChange={e => setUsername(e.target.value)}
                        placeholder="username (мин. 5 символов)"
                        autoComplete="username"
                        className="w-full px-4 py-3 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:outline-none text-sm"
                      />
                      <PasswordInput
                        value={password}
                        onChange={e => setPassword(e.target.value)}
                        placeholder="Пароль"
                        label=""
                        autoComplete="new-password"
                      />
                      <PasswordInput
                        value={confirmPassword}
                        onChange={e => setConfirmPassword(e.target.value)}
                        placeholder="Повторите пароль"
                        label=""
                        autoComplete="new-password"
                      />
                      {credsErr && <p className="text-red-400 text-xs">{credsErr}</p>}
                      {credsMsg && <p className="text-green-400 text-xs">{credsMsg}</p>}
                      <button
                        disabled={credsLoading}
                        onClick={saveCreds}
                        className="w-full py-2.5 rounded-xl bg-brand text-white text-sm font-medium hover:bg-brand-dark transition disabled:opacity-50"
                      >
                        {credsLoading ? 'Сохранение…' : 'Сохранить'}
                      </button>
                    </div>
                  )}
                </div>
              )}

              {credsSaved && (
                <div className="mb-6 flex items-center gap-2 p-3 rounded-xl bg-green-500/10 border border-green-500/20">
                  <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                  </svg>
                  <p className="text-green-400 text-sm">Логин <strong>@{username}</strong> сохранён. Можно входить по нему.</p>
                </div>
              )}

              {/* CTA */}
              <button
                className="group relative w-full py-3.5 rounded-xl font-semibold text-white overflow-hidden transition-all duration-300 hover:shadow-lg hover:shadow-brand/25"
                onClick={() => navigate('/dashboard')}
              >
                <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark" />
                <span className="absolute inset-0 bg-gradient-to-r from-brand-light to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
                <span className="relative z-10 flex items-center justify-center gap-2">
                  Перейти в личный кабинет
                  <svg className="w-4 h-4 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                  </svg>
                </span>
              </button>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
