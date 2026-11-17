import { useState } from 'react'
import { KeyRound } from 'lucide-react'
import { resetUserPassword } from '../../api/admin.js'
import { Button, Dialog, CopyButton } from '../ui/index.js'
import { resetConsequences } from '../../utils/users.js'

/**
 * «Сбросить пароль» (users.password_reset): a confirmation, then the temporary password — shown
 * once, with copying. Closing forgets it; the list is refreshed (onDone) so the card shows
 * «временный — ждёт смены».
 */
export default function PasswordResetDialog({ user, open, onClose, onDone }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState(null) // { temp_password, sessions_closed, email_sent }

  function close() {
    if (busy) return
    const done = !!result
    setResult(null)
    setError('')
    onClose()
    if (done) onDone?.()
  }

  async function reset() {
    setBusy(true)
    setError('')
    try {
      setResult(await resetUserPassword(user.id))
    } catch (err) {
      setError(err.message || 'Не удалось сбросить пароль')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onClose={close} title={result ? 'Временный пароль' : 'Сбросить пароль'} icon={<KeyRound className="w-4 h-4" />} maxWidth="max-w-md">
      {!result ? (
        <div className="space-y-3 text-sm">
          <p className="text-secondary">
            Пользователь <span className="font-mono text-primary">{user.username || user.user_id}</span>
          </p>
          <ul className="list-disc pl-5 space-y-1 text-secondary">
            {resetConsequences(user).map((line) => <li key={line}>{line}</li>)}
          </ul>
          {error && <p role="alert" className="text-danger">{error}</p>}
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
            <Button variant="ghost" onClick={close} disabled={busy}>Отмена</Button>
            <Button variant="danger" loading={busy} onClick={reset}>Сбросить пароль</Button>
          </div>
        </div>
      ) : (
        <div className="space-y-3 text-sm">
          <div className="flex items-center gap-2 rounded-md border border-default bg-elevated px-3 py-2">
            <span className="flex-1 font-mono text-base text-primary tracking-wide select-all" aria-label="Временный пароль">{result.temp_password}</span>
            <CopyButton text={result.temp_password} label="Скопировать пароль" />
          </div>
          <p className="text-warning">Показан один раз — после закрытия окна его не увидеть. Нужен новый — сбросьте ещё раз.</p>
          <ul className="list-disc pl-5 space-y-1 text-secondary">
            <li>{result.email_sent ? 'Письмо с паролем отправлено на подтверждённую почту.' : 'Письмо не отправлено — передайте пароль сами.'}</li>
            <li>Закрыто сессий: <span className="font-mono tabular-nums">{result.sessions_closed}</span>.</li>
            <li>При входе кабинет попросит сменить пароль.</li>
          </ul>
          <div className="flex justify-end">
            <Button variant="primary" onClick={close}>Готово</Button>
          </div>
        </div>
      )}
    </Dialog>
  )
}
