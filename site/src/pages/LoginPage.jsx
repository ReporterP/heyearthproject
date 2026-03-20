import { useState } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { loginUser } from '../api/user.js'

export default function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()

  const [idOrUsername, setIdOrUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
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
      localStorage.setItem('token', data.token)
      navigate(searchParams.get('redirect') || '/dashboard')
    } catch (e) {
      setError(e.message || 'Ошибка входа. Попробуйте позже.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="py-24">
      <div className="max-w-md mx-auto px-6">
        <h1 className="text-3xl font-bold text-center mb-2">Вход</h1>
        <p className="text-text-secondary text-center mb-10">Войдите в свой аккаунт HeYearth</p>

        <form onSubmit={handleLogin} className="space-y-5">
          <div>
            <label className="block text-sm text-text-secondary mb-2">HeYearth ID или имя пользователя</label>
            <input
              type="text"
              value={idOrUsername}
              onChange={e => setIdOrUsername(e.target.value)}
              placeholder="Введите ваш ID или username"
              className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
            />
          </div>

          <div>
            {!showPassword ? (
              <button type="button" className="text-sm text-brand-light hover:text-brand transition" onClick={() => setShowPassword(true)}>
                У меня есть облачный пароль
              </button>
            ) : (
              <>
                <label className="block text-sm text-text-secondary mb-2">Облачный пароль</label>
                <input
                  type="password"
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  placeholder="Введите пароль"
                  className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
                />
              </>
            )}
          </div>

          {error && <p className="text-red-400 text-sm">{error}</p>}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
          >
            {loading ? 'Вход...' : 'Войти'}
          </button>
        </form>

        <p className="text-center text-sm text-text-secondary mt-8">
          Нет аккаунта?{' '}
          <Link to="/register" className="text-brand-light hover:text-brand transition">Зарегистрироваться</Link>
        </p>
      </div>
    </section>
  )
}
