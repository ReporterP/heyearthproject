import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from './AuthContext.jsx'
import { useT } from '../i18n/index.jsx'
import { login as apiLogin } from '../api/admin.js'

export default function LoginPage() {
  const { login } = useAuth()
  const { t } = useT()
  const navigate = useNavigate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(e) {
    e.preventDefault()
    if (!username.trim()) return
    setLoading(true)
    setError('')
    try {
      const result = await apiLogin(username.trim(), password)
      login(result)
      navigate('/dashboard', { replace: true })
    } catch (err) {
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-surface px-4">
      <div className="w-full max-w-sm">
        <h1 className="text-3xl font-bold text-center mb-1">{t.appTitle}</h1>
        <p className="text-text-secondary text-center mb-8">{t.loginSubtitle}</p>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-text-secondary mb-1">{t.username}</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-text-primary placeholder-text-secondary focus:border-brand focus:ring-1 focus:ring-brand outline-none"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm text-text-secondary mb-1">{t.password}</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-text-primary placeholder-text-secondary focus:border-brand focus:ring-1 focus:ring-brand outline-none"
            />
          </div>

          {error && <p className="text-red-400 text-sm">{error}</p>}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 rounded-lg bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
          >
            {loading ? t.loading : t.login}
          </button>
        </form>
      </div>
    </div>
  )
}
