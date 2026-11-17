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
  // 2FA: the server asks for the code (401 + totp_required) after a correct password.
  const [needCode, setNeedCode] = useState(false)
  const [code, setCode] = useState('')

  async function handleSubmit(e) {
    e.preventDefault()
    if (!username.trim()) return
    setLoading(true)
    setError('')
    try {
      const result = await apiLogin(username.trim(), password, needCode ? code.trim() : undefined)
      login(result)
      navigate('/dashboard', { replace: true })
    } catch (err) {
      if (err.data?.totp_required) {
        setNeedCode(true)
        setCode('')
        setError(needCode ? err.message : '')
        return
      }
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-canvas px-4">
      <div className="w-full max-w-sm">
        <h1 className="text-3xl font-bold text-center mb-1">{t.appTitle}</h1>
        <p className="text-secondary text-center mb-8">{t.loginSubtitle}</p>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-secondary mb-1">{t.username}</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary placeholder-text-secondary focus:border-accent focus:ring-1 focus:ring-accent outline-none"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm text-secondary mb-1">{t.password}</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary placeholder-text-secondary focus:border-accent focus:ring-1 focus:ring-accent outline-none"
            />
          </div>

          {needCode && (
            <div>
              <label htmlFor="totp" className="block text-sm text-secondary mb-1">Код из приложения</label>
              <input
                id="totp"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary font-mono tabular-nums tracking-widest focus:border-accent focus:ring-1 focus:ring-accent outline-none"
                autoFocus
              />
              <p className="text-xs text-tertiary mt-1">Шесть цифр из Google Authenticator, Aegis или другого приложения.</p>
            </div>
          )}

          {error && <p className="text-danger text-sm">{error}</p>}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 rounded-lg bg-accent hover:bg-accent-hover text-white font-semibold transition disabled:opacity-50"
          >
            {loading ? t.loading : t.login}
          </button>
        </form>
      </div>
    </div>
  )
}
