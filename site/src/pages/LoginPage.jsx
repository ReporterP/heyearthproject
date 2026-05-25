import { useState, useEffect } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { loginUser } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import PasswordInput from '../components/PasswordInput.jsx'

export default function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { login, isLoggedIn } = useAuth()

  // Already logged in? Skip the login flow.
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
      setError('Введите HeYearth ID или имя пользователя')
      return
    }
    setLoading(true)
    setError('')
    try {
      const data = await loginUser(idOrUsername.trim(), password)
      login(data.token)
      navigate(searchParams.get('redirect') || '/dashboard')
    } catch (e) {
      setError(e.message || 'Ошибка входа. Попробуйте позже.')
    } finally {
      setLoading(false)
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
        {/* Card */}
        <div className="glass-strong rounded-2xl p-8 md:p-10 shadow-2xl shadow-black/20 animate-fade-in-scale">
          {/* Logo */}
          <div className="flex justify-center mb-8">
            <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-brand to-brand-dark flex items-center justify-center shadow-lg shadow-brand/25">
              <span className="text-xl font-black text-white">H</span>
            </div>
          </div>

          <h1 className="text-2xl font-bold text-center mb-1">Добро пожаловать</h1>
          <p className="text-text-secondary text-center text-sm mb-8">Войдите в свой аккаунт HeYearth</p>

          <form onSubmit={handleLogin} className="space-y-5">
            {/* ID / Username input */}
            <div className="group">
              <label className="block text-xs font-medium text-text-secondary mb-2 group-focus-within:text-brand-light transition-colors">
                HeYearth ID или имя пользователя
              </label>
              <div className="relative">
                <div className="absolute left-3.5 top-1/2 -translate-y-1/2 text-text-secondary group-focus-within:text-brand-light transition-colors">
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z" />
                  </svg>
                </div>
                <input
                  type="text"
                  value={idOrUsername}
                  onChange={e => setIdOrUsername(e.target.value)}
                  placeholder="Введите ваш ID или username"
                  className="w-full pl-12 pr-4 py-3.5 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:bg-white/[0.07] focus:outline-none focus:ring-1 focus:ring-brand/30 transition-all duration-300 text-sm placeholder:text-text-secondary/50"
                />
              </div>
            </div>

            {/* Password field — сразу виден, опционален (для аккаунтов с паролем) */}
            <div>
              <PasswordInput
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="Оставьте пустым, если не задан"
                label="Пароль (если установлен)"
              />
            </div>

            {/* Error */}
            {error && (
              <div className="flex items-start gap-2 p-3 rounded-xl bg-red-500/10 border border-red-500/20 animate-fade-in">
                <svg className="w-5 h-5 text-red-400 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                </svg>
                <p className="text-red-400 text-sm">{error}</p>
              </div>
            )}

            {/* Submit */}
            <button
              type="submit"
              disabled={loading}
              className="group relative w-full py-3.5 rounded-xl font-semibold text-white overflow-hidden transition-all duration-300 disabled:opacity-50 hover:shadow-lg hover:shadow-brand/25"
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
                    Вход...
                  </>
                ) : 'Войти'}
              </span>
            </button>
          </form>

          {/* Divider */}
          <div className="relative my-8">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full h-px bg-gradient-to-r from-transparent via-white/10 to-transparent" />
            </div>
          </div>

          <p className="text-center text-sm text-text-secondary">
            Нет аккаунта?{' '}
            <Link to="/register" className="text-brand-light hover:text-accent font-medium transition-colors duration-200">
              Зарегистрироваться
            </Link>
          </p>
        </div>
      </div>
    </section>
  )
}
