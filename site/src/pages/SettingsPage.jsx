import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { getProfile, updateProfile } from '../api/user.js'

export default function SettingsPage() {
  const navigate = useNavigate()
  const token = localStorage.getItem('token')

  const [loading, setLoading] = useState(true)
  const [username, setUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [currentPassword, setCurrentPassword] = useState('')
  const [savingUsername, setSavingUsername] = useState(false)
  const [savingPassword, setSavingPassword] = useState(false)
  const [usernameMsg, setUsernameMsg] = useState('')
  const [usernameError, setUsernameError] = useState('')
  const [passwordMsg, setPasswordMsg] = useState('')
  const [passwordError, setPasswordError] = useState('')

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    getProfile(token)
      .then(data => setUsername(data.username || ''))
      .catch(e => {
        if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
          localStorage.removeItem('token')
          navigate('/login')
        }
      })
      .finally(() => setLoading(false))
  }, [])

  async function saveUsername() {
    if (!username.trim()) { setUsernameError('Введите имя пользователя'); return }
    setSavingUsername(true); setUsernameMsg(''); setUsernameError('')
    try {
      await updateProfile(token, { username: username.trim() })
      setUsernameMsg('Имя пользователя сохранено')
    } catch (e) {
      setUsernameError(e.message || 'Ошибка сохранения')
    } finally {
      setSavingUsername(false)
    }
  }

  async function savePassword() {
    setPasswordMsg(''); setPasswordError('')
    if (!newPassword) { setPasswordError('Введите новый пароль'); return }
    if (newPassword !== confirmPassword) { setPasswordError('Пароли не совпадают'); return }
    setSavingPassword(true)
    try {
      await updateProfile(token, { newPassword, currentPassword: currentPassword || undefined })
      setPasswordMsg('Пароль сохранён')
      setNewPassword(''); setConfirmPassword(''); setCurrentPassword('')
    } catch (e) {
      setPasswordError(e.message || 'Ошибка сохранения')
    } finally {
      setSavingPassword(false)
    }
  }

  function logout() {
    localStorage.removeItem('token')
    navigate('/')
  }

  return (
    <section className="py-24">
      <div className="max-w-lg mx-auto px-6">
        <h1 className="text-3xl font-bold text-center mb-10">Настройки</h1>

        {loading ? (
          <div className="text-center py-10 text-text-secondary">Загрузка...</div>
        ) : (
          <>
            {/* Username */}
            <div className="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
              <h2 className="text-lg font-semibold mb-4">Имя пользователя</h2>
              <input
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                placeholder="username"
                className="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition mb-3"
              />
              {usernameMsg && <p className="text-green-400 text-sm mb-3">{usernameMsg}</p>}
              {usernameError && <p className="text-red-400 text-sm mb-3">{usernameError}</p>}
              <button
                disabled={savingUsername}
                className="px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white text-sm font-semibold transition disabled:opacity-50"
                onClick={saveUsername}
              >
                {savingUsername ? 'Сохранение...' : 'Сохранить'}
              </button>
            </div>

            {/* Password */}
            <div className="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
              <h2 className="text-lg font-semibold mb-4">Облачный пароль</h2>
              <div className="space-y-3">
                <div>
                  <label className="block text-sm text-text-secondary mb-1">Текущий пароль (если установлен)</label>
                  <input type="password" value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} placeholder="Текущий пароль" className="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition" />
                </div>
                <div>
                  <label className="block text-sm text-text-secondary mb-1">Новый пароль</label>
                  <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} placeholder="Новый пароль" className="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition" />
                </div>
                <div>
                  <label className="block text-sm text-text-secondary mb-1">Подтвердите пароль</label>
                  <input type="password" value={confirmPassword} onChange={e => setConfirmPassword(e.target.value)} placeholder="Повторите пароль" className="w-full px-4 py-3 rounded-xl bg-surface border border-white/10 focus:border-brand/50 focus:outline-none transition" />
                </div>
              </div>
              {passwordMsg && <p className="text-green-400 text-sm mt-3">{passwordMsg}</p>}
              {passwordError && <p className="text-red-400 text-sm mt-3">{passwordError}</p>}
              <button
                disabled={savingPassword}
                className="mt-4 px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white text-sm font-semibold transition disabled:opacity-50"
                onClick={savePassword}
              >
                {savingPassword ? 'Сохранение...' : 'Сохранить пароль'}
              </button>
            </div>

            {/* Logout */}
            <button
              className="w-full py-3 rounded-xl border border-red-500/30 text-red-400 hover:bg-red-500/10 transition text-sm font-medium"
              onClick={logout}
            >
              Выйти из аккаунта
            </button>
          </>
        )}
      </div>
    </section>
  )
}
