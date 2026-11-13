import { useState, useEffect, useRef } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { safeRedirect } from '../lib/redirect.js'
import { loginUser } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import PasswordInput from '../components/PasswordInput.jsx'
import Fireflies from '../components/Fireflies.jsx'

const LeafMark = () => (
  <svg className="w-7 h-7" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 21c-4.5-1-8.5-4.5-8.5-10.5C9 10.5 12 13.5 12 17M12 21c4.5-1 8.5-4.5 8.5-10.5C15 10.5 12 13.5 12 17M12 21V8" />
  </svg>
)

export default function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { login, isLoggedIn } = useAuth()
  const idRef = useRef(null)

  useEffect(() => {
    if (isLoggedIn) navigate('/dashboard', { replace: true })
  }, [isLoggedIn, navigate])

  // Focus the only field that matters on open
  useEffect(() => { idRef.current?.focus() }, [])

  const [idOrUsername, setIdOrUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false) // progressive disclosure
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [showHelp, setShowHelp] = useState(false)

  const redirectTo = safeRedirect(searchParams.get('redirect'))
  const registerHref = redirectTo !== '/dashboard' ? `/register?redirect=${encodeURIComponent(redirectTo)}` : '/register'
  const canSubmit = idOrUsername.trim().length > 0 && !loading

  async function handleLogin(e) {
    e.preventDefault()
    if (!idOrUsername.trim()) {
      setError('Введите HiEarth ID или имя пользователя')
      idRef.current?.focus()
      return
    }
    setLoading(true)
    setError('')
    try {
      const data = await loginUser(idOrUsername.trim(), password)
      login(data.token)
      navigate(redirectTo)
    } catch (e) {
      const msg = e.message || ''
      // Turn backend errors into a cause + a way forward (error-clarity)
      if (/password|пароль/i.test(msg)) {
        setError('Неверный пароль. Если вы его не задавали — оставьте поле пустым.')
        setShowPassword(true)
      } else if (/not found|не найден|404/i.test(msg)) {
        setError('Аккаунт не найден. Проверьте ID — его легко перепутать с похожими символами.')
      } else {
        setError(msg || 'Не удалось войти. Попробуйте ещё раз.')
      }
    } finally {
      setLoading(false)
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
          <div className="flex justify-center mb-7">
            <div className="w-14 h-14 rounded-2xl bg-brand/12 border border-brand/25 text-brand flex items-center justify-center glow-brand">
              <LeafMark />
            </div>
          </div>

          <h1 className="serif text-[26px] font-semibold text-center mb-1">С возвращением</h1>
          <p className="text-text-secondary text-center text-sm mb-8">
            {redirectTo !== '/dashboard' ? 'Войдите, чтобы продолжить' : 'Войдите в аккаунт HiEarth'}
          </p>

          <form onSubmit={handleLogin} className="space-y-4" noValidate>
            <div className="group">
              <label htmlFor="login-id" className="block text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand transition-colors">
                HiEarth ID или имя пользователя
              </label>
              <div className="relative">
                <div className="absolute left-3.5 top-1/2 -translate-y-1/2 text-faint group-focus-within:text-brand transition-colors">
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z" />
                  </svg>
                </div>
                <input
                  id="login-id"
                  ref={idRef}
                  type="text"
                  autoComplete="username"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck="false"
                  value={idOrUsername}
                  onChange={e => { setIdOrUsername(e.target.value); if (error) setError('') }}
                  placeholder="Введите ваш ID или username"
                  aria-invalid={!!error}
                  className="w-full pl-12 pr-4 py-3.5 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brand/25 transition-all duration-300 text-sm placeholder:text-faint font-mono"
                />
              </div>
            </div>

            {/* Password is optional for most accounts — don't show it by default */}
            {showPassword ? (
              <div className="animate-fade-in">
                <PasswordInput
                  value={password}
                  onChange={e => { setPassword(e.target.value); if (error) setError('') }}
                  placeholder="Ваш пароль"
                  label="Пароль"
                  autoComplete="current-password"
                />
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setShowPassword(true)}
                className="text-xs text-text-secondary hover:text-brand transition inline-flex items-center gap-1.5"
              >
                <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75M6.75 21.75h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" /></svg>
                У меня есть пароль
              </button>
            )}

            {error && (
              <div role="alert" aria-live="polite" className="flex items-start gap-2 p-3 rounded-xl bg-danger/10 border border-danger/20 animate-fade-in">
                <svg className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                </svg>
                <p className="text-danger text-sm">{error}</p>
              </div>
            )}

            <button type="submit" disabled={!canSubmit} className="btn-primary w-full !py-3.5 disabled:opacity-40">
              {loading ? (
                <>
                  <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  Входим…
                </>
              ) : 'Войти'}
            </button>
          </form>

          {/* Recovery path — an unrecoverable ID needs an honest answer, not silence */}
          <div className="mt-5">
            <button onClick={() => setShowHelp(h => !h)} aria-expanded={showHelp} className="text-xs text-text-secondary hover:text-text-primary transition">
              Потеряли ID?
            </button>
            <div className="grid transition-all duration-300 ease-out" style={{ gridTemplateRows: showHelp ? '1fr' : '0fr' }}>
              <div className="overflow-hidden">
                <div className="pt-3 p-3.5 rounded-xl bg-white/[0.03] border border-brand/10">
                  <p className="text-xs text-text-secondary leading-relaxed">
                    HiEarth ID выдаётся анонимно и нигде не дублируется — восстановить его мы не можем.
                    Проверьте буфер обмена, заметки и скачанный файл <code className="font-mono text-text-primary">hiearth-id.txt</code>.
                    Если ID утерян — <Link to={registerHref} className="text-brand hover:text-brand-light">создайте новый</Link> или напишите в{' '}
                    <Link to="/help" className="text-brand hover:text-brand-light">поддержку</Link>: найдём аккаунт по платежу.
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div className="relative my-7 h-px bg-gradient-to-r from-transparent via-brand/15 to-transparent" />

          <p className="text-center text-sm text-text-secondary">
            Нет аккаунта?{' '}
            <Link to={registerHref} className="text-brand hover:text-brand-light font-medium transition-colors">
              Зарегистрироваться
            </Link>
          </p>
        </div>
      </div>
    </section>
  )
}
