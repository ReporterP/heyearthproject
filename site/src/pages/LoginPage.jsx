import { useState, useEffect } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
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

  useEffect(() => {
    if (isLoggedIn) navigate('/dashboard', { replace: true })
  }, [isLoggedIn, navigate])

  const [idOrUsername, setIdOrUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleLogin(e) {
    e.preventDefault()
    if (!idOrUsername.trim()) {
      setError('Введите HiEarth ID или имя пользователя')
      return
    }
    setLoading(true)
    setError('')
    try {
      const data = await loginUser(idOrUsername.trim(), password)
      login(data.token)
      navigate(searchParams.get('redirect') || '/dashboard')
    } catch (e) {
      setError(e.message || 'Не удалось войти. Проверьте ID и попробуйте снова.')
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
          <p className="text-text-secondary text-center text-sm mb-8">Войдите в аккаунт HiEarth</p>

          <form onSubmit={handleLogin} className="space-y-5">
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
                  type="text"
                  autoComplete="username"
                  value={idOrUsername}
                  onChange={e => setIdOrUsername(e.target.value)}
                  placeholder="Введите ваш ID или username"
                  className="w-full pl-12 pr-4 py-3.5 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brand/25 transition-all duration-300 text-sm placeholder:text-faint"
                />
              </div>
            </div>

            <PasswordInput
              value={password}
              onChange={e => setPassword(e.target.value)}
              placeholder="Оставьте пустым, если не задан"
              label="Пароль (если установлен)"
            />

            {error && (
              <div className="flex items-start gap-2 p-3 rounded-xl bg-danger/10 border border-danger/20 animate-fade-in">
                <svg className="w-5 h-5 text-danger flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                </svg>
                <p className="text-danger text-sm">{error}</p>
              </div>
            )}

            <button type="submit" disabled={loading} className="btn-primary w-full !py-3.5 disabled:opacity-50">
              {loading ? (
                <>
                  <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  Вход…
                </>
              ) : 'Войти'}
            </button>
          </form>

          <div className="relative my-8 h-px bg-gradient-to-r from-transparent via-brand/15 to-transparent" />

          <p className="text-center text-sm text-text-secondary">
            Нет аккаунта?{' '}
            <Link to="/register" className="text-brand hover:text-brand-light font-medium transition-colors">
              Зарегистрироваться
            </Link>
          </p>
        </div>
      </div>
    </section>
  )
}
