import { useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { registerUser } from '../api/user.js'

export default function RegisterPage() {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [generatedId, setGeneratedId] = useState('')
  const [copied, setCopied] = useState(false)

  async function handleRegister() {
    setLoading(true)
    setError('')
    try {
      const data = await registerUser('web', '')
      setGeneratedId(data.id || data.user_id || data.heyearth_id || '')
      if (data.token) localStorage.setItem('token', data.token)
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

  return (
    <section className="py-24">
      <div className="max-w-md mx-auto px-6">
        {!generatedId ? (
          <>
            <h1 className="text-3xl font-bold text-center mb-2">Регистрация</h1>
            <p className="text-text-secondary text-center mb-10">
              Получите свой уникальный HeYearth ID за один клик
            </p>

            {error && <p className="text-red-400 text-sm text-center mb-4">{error}</p>}

            <button
              disabled={loading}
              className="w-full py-4 rounded-xl bg-brand hover:bg-brand-dark text-white text-lg font-bold transition shadow-lg shadow-brand/25 disabled:opacity-50"
              onClick={handleRegister}
            >
              {loading ? 'Создание...' : 'Получить HeYearth ID'}
            </button>

            <p className="text-center text-sm text-text-secondary mt-8">
              Уже есть аккаунт?{' '}
              <Link to="/login" className="text-brand-light hover:text-brand transition">Войти</Link>
            </p>
          </>
        ) : (
          <div className="text-center">
            <div className="text-5xl mb-6">&#127881;</div>
            <h2 className="text-2xl font-bold mb-2">Ваш HeYearth ID</h2>

            <div className="my-6 p-6 rounded-2xl bg-surface-card border border-brand/30">
              <p className="text-4xl md:text-5xl font-mono font-extrabold tracking-wider text-brand-light break-all">
                {generatedId}
              </p>
            </div>

            <button
              className="inline-flex items-center gap-2 px-6 py-3 rounded-xl border border-white/10 hover:border-white/20 text-text-primary transition mb-6"
              onClick={copyId}
            >
              <svg xmlns="http://www.w3.org/2000/svg" className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
              {copied ? 'Скопировано!' : 'Копировать ID'}
            </button>

            <div className="p-4 rounded-xl bg-yellow-500/10 border border-yellow-500/30 text-yellow-200 text-sm mb-8">
              <p className="font-semibold mb-1">Важно! Сохраните ваш ID!</p>
              <p>Без него вы не сможете войти в аккаунт. Запишите его в надёжное место.</p>
            </div>

            <button
              className="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition"
              onClick={() => navigate('/dashboard')}
            >
              Перейти в личный кабинет
            </button>
          </div>
        )}
      </div>
    </section>
  )
}
