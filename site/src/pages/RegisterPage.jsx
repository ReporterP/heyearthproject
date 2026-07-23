import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { registerUser, updateProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import PasswordInput from '../components/PasswordInput.jsx'
import Fireflies from '../components/Fireflies.jsx'

const Arrow = () => (
  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
)

export default function RegisterPage() {
  const navigate = useNavigate()
  const { login, isLoggedIn } = useAuth()

  useEffect(() => {
    if (isLoggedIn) navigate('/dashboard', { replace: true })
  }, [isLoggedIn, navigate])
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
      setError(e.message || 'Не удалось создать аккаунт. Попробуйте ещё раз.')
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
      setCredsErr(e.message || 'Не удалось сохранить. Попробуйте снова.')
    } finally {
      setCredsLoading(false)
    }
  }

  return (
    <section className="relative min-h-[calc(100vh-8rem)] flex items-center justify-center py-16 overflow-hidden">
      <Fireflies density={22000} />
      <div className="pointer-events-none absolute inset-0">
        <div className="blob-a absolute top-1/4 -left-24 w-80 h-80 rounded-full bg-brand/10 blur-[110px]" />
        <div className="blob-b absolute bottom-1/4 -right-24 w-80 h-80 rounded-full bg-accent/8 blur-[110px]" />
      </div>

      <div className="relative w-full max-w-md mx-auto px-6">
        <div className="glass-strong rounded-[26px] p-8 md:p-10 animate-fade-in-scale">
          {!generatedId ? (
            <>
              <div className="flex justify-center mb-7">
                <div className="w-14 h-14 rounded-2xl bg-brand/12 border border-brand/25 text-brand flex items-center justify-center glow-brand">
                  <svg className="w-7 h-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round">
                    <path d="M19 7.5v6M22 10.5h-6M12.75 7a3.375 3.375 0 11-6.75 0 3.375 3.375 0 016.75 0zM4 19.2v-.1a6.375 6.375 0 0112.75 0v.1A12.3 12.3 0 0110.37 21c-2.33 0-4.51-.64-6.37-1.77z" />
                  </svg>
                </div>
              </div>

              <h1 className="serif text-[26px] font-semibold text-center mb-1">Регистрация</h1>
              <p className="text-text-secondary text-center text-sm mb-9">
                Уникальный HiEarth ID за один клик. Анонимно и бесплатно.
              </p>

              {error && (
                <div className="flex items-start gap-2 p-3 rounded-xl bg-danger/10 border border-danger/20 mb-6 animate-fade-in">
                  <svg className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                  </svg>
                  <p className="text-danger text-sm">{error}</p>
                </div>
              )}

              <button disabled={loading} className="btn-primary w-full !py-4 !text-base disabled:opacity-50" onClick={handleRegister}>
                {loading ? (
                  <>
                    <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                    Создание…
                  </>
                ) : (<>Получить HiEarth ID <Arrow /></>)}
              </button>

              <div className="mt-8 space-y-3">
                {['Анонимная регистрация', 'Не нужен email или телефон', 'Мгновенная активация'].map(f => (
                  <div key={f} className="flex items-center gap-3 text-sm text-text-secondary">
                    <svg className="w-4 h-4 text-brand flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                    </svg>
                    {f}
                  </div>
                ))}
              </div>

              <div className="relative my-8 h-px bg-gradient-to-r from-transparent via-brand/15 to-transparent" />

              <p className="text-center text-sm text-text-secondary">
                Уже есть аккаунт?{' '}
                <Link to="/login" className="text-brand hover:text-brand-light font-medium transition-colors">Войти</Link>
              </p>
            </>
          ) : (
            <div className="text-center animate-fade-in-scale">
              <div className="flex justify-center mb-6">
                <div className="w-16 h-16 rounded-full bg-brand/12 border border-brand/25 flex items-center justify-center">
                  <svg className="w-8 h-8 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </div>

              <h2 className="serif text-2xl font-semibold mb-2">Ваш HiEarth ID</h2>
              <p className="text-text-secondary text-sm mb-6">Сохраните этот ID — он понадобится для входа</p>

              <div className="relative my-6 p-6 rounded-2xl glass border border-brand/30 glow-brand">
                <p className="text-3xl md:text-4xl font-mono font-bold tracking-wider text-brand-light break-all">{generatedId}</p>
                <div className="absolute top-2 left-2 w-3 h-3 border-l-2 border-t-2 border-brand/40 rounded-tl" />
                <div className="absolute top-2 right-2 w-3 h-3 border-r-2 border-t-2 border-brand/40 rounded-tr" />
                <div className="absolute bottom-2 left-2 w-3 h-3 border-l-2 border-b-2 border-brand/40 rounded-bl" />
                <div className="absolute bottom-2 right-2 w-3 h-3 border-r-2 border-b-2 border-brand/40 rounded-br" />
              </div>

              <button className="btn-ghost mb-6" onClick={copyId}>
                {copied ? (
                  <>
                    <svg className="w-5 h-5 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                    Скопировано!
                  </>
                ) : (
                  <>
                    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25" /></svg>
                    Копировать ID
                  </>
                )}
              </button>

              <div className="flex items-start gap-3 p-4 rounded-xl bg-accent/10 border border-accent/25 text-left mb-6">
                <svg className="w-5 h-5 text-accent flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                <div>
                  <p className="text-sm font-semibold text-accent mb-0.5">Важно! Сохраните ваш ID</p>
                  <p className="text-xs text-accent/70">Без него вы не сможете войти в аккаунт. Запишите его в надёжное место.</p>
                </div>
              </div>

              {!credsSaved && (
                <div className="mb-6 text-left">
                  {!showCreds ? (
                    <button
                      type="button"
                      onClick={() => setShowCreds(true)}
                      className="w-full py-3 rounded-xl border border-dashed border-brand/20 hover:border-brand/45 hover:bg-brand/5 text-sm text-text-secondary hover:text-text-primary transition-all flex items-center justify-center gap-2"
                    >
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" /></svg>
                      Создать логин и пароль (необязательно)
                    </button>
                  ) : (
                    <div className="p-4 rounded-xl bg-white/[0.03] border border-brand/12 space-y-3">
                      <div className="flex items-center justify-between">
                        <p className="text-sm font-semibold">Логин и пароль</p>
                        <button onClick={() => setShowCreds(false)} className="text-xs text-text-secondary hover:text-text-primary">Пропустить</button>
                      </div>
                      <p className="text-xs text-text-secondary">Удобнее входить по короткому username вместо длинного ID. Можно задать позже в настройках.</p>
                      <div>
                        <label htmlFor="reg-username" className="sr-only">Имя пользователя</label>
                        <input
                          id="reg-username"
                          type="text"
                          value={username}
                          onChange={e => setUsername(e.target.value)}
                          placeholder="username (мин. 5 символов)"
                          autoComplete="username"
                          className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/25 text-sm placeholder:text-faint"
                        />
                      </div>
                      <PasswordInput value={password} onChange={e => setPassword(e.target.value)} placeholder="Пароль" label="" autoComplete="new-password" />
                      <PasswordInput value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} placeholder="Повторите пароль" label="" autoComplete="new-password" />
                      {credsErr && <p className="text-danger text-xs">{credsErr}</p>}
                      {credsMsg && <p className="text-brand text-xs">{credsMsg}</p>}
                      <button disabled={credsLoading} onClick={saveCreds} className="btn-primary w-full !py-2.5 !text-sm disabled:opacity-50">
                        {credsLoading ? 'Сохранение…' : 'Сохранить'}
                      </button>
                    </div>
                  )}
                </div>
              )}

              {credsSaved && (
                <div className="mb-6 flex items-center gap-2 p-3 rounded-xl bg-brand/10 border border-brand/20">
                  <svg className="w-4 h-4 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                  <p className="text-brand text-sm">Логин <strong>@{username}</strong> сохранён. Можно входить по нему.</p>
                </div>
              )}

              <button className="btn-primary w-full !py-3.5" onClick={() => navigate('/dashboard')}>
                Перейти в личный кабинет <Arrow />
              </button>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
