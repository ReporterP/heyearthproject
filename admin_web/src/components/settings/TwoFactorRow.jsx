import { useEffect, useState } from 'react'
import { ShieldCheck } from 'lucide-react'
import { Badge, Button, Dialog, Input } from '../ui/index.js'
import { useToast } from '../Toast.jsx'
import { disableTOTP, enableTOTP, getTOTPStatus, setupTOTP } from '../../api/admin.js'

// «Двухфакторный вход» of the signed-in admin (internal/admin/login_security.go): a key for an
// authenticator app (QR + text), confirmed with one code; switching off needs the password and a
// code. Lost phone: docs/runbooks/ddos.md («Админка: 2FA»).
export default function TwoFactorRow() {
  const toast = useToast()
  const [enabled, setEnabled] = useState(null) // null = unknown yet
  const [setup, setSetup] = useState(null) // { secret, uri, qr }
  const [offOpen, setOffOpen] = useState(false)
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    getTOTPStatus().then((s) => setEnabled(!!s.enabled)).catch(() => setEnabled(null))
  }, [])

  const digits = (v) => v.replace(/\D/g, '').slice(0, 6)

  async function start() {
    setErr(''); setCode(''); setBusy(true)
    try { setSetup(await setupTOTP()) } catch (e) { toast.error(e.message) } finally { setBusy(false) }
  }
  async function confirm() {
    setErr(''); setBusy(true)
    try {
      await enableTOTP(code)
      setEnabled(true); setSetup(null)
      toast.success('Двухфакторный вход включён')
    } catch (e) { setErr(e.message) } finally { setBusy(false) }
  }
  async function turnOff() {
    setErr(''); setBusy(true)
    try {
      await disableTOTP(password, code)
      setEnabled(false); setOffOpen(false); setPassword(''); setCode('')
      toast.success('Двухфакторный вход выключен')
    } catch (e) { setErr(e.message) } finally { setBusy(false) }
  }

  return (
    <div className="py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
      <div className="min-w-0">
        <p className="text-sm text-primary flex items-center gap-2">
          Двухфакторный вход
          {enabled === true && <Badge variant="success">включён</Badge>}
          {enabled === false && <Badge variant="warning">выключен</Badge>}
        </p>
        <p className="text-xs text-tertiary">Код из приложения на телефоне при каждом входе. После 5 ошибок подряд вход блокируется на 15 минут.</p>
      </div>
      {enabled === false && <Button icon={<ShieldCheck size={14} />} loading={busy} onClick={start} className="w-full sm:w-auto">Включить</Button>}
      {enabled === true && <Button variant="secondary" onClick={() => { setErr(''); setCode(''); setOffOpen(true) }} className="w-full sm:w-auto">Выключить</Button>}

      <Dialog open={!!setup} onClose={() => setSetup(null)} title="Включить двухфакторный вход" maxWidth="max-w-md">
        {setup && (
          <div className="space-y-4">
            <p className="text-sm text-secondary">Отсканируйте QR-код в Google Authenticator, Aegis или 2FAS. Если камеры нет — введите ключ вручную.</p>
            <img src={setup.qr} alt="QR-код ключа двухфакторного входа" className="mx-auto w-48 h-48 rounded border border-default bg-white p-2" />
            <p className="font-mono text-xs text-primary break-all text-center select-all">{setup.secret}</p>
            <div>
              <label htmlFor="totp-enable" className="block text-sm text-secondary mb-1">Код из приложения</label>
              <Input id="totp-enable" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(digits(e.target.value))} className="font-mono tabular-nums tracking-widest" />
            </div>
            {err && <p role="alert" className="text-sm text-danger">{err}</p>}
            <div className="flex flex-col sm:flex-row gap-2 sm:justify-end">
              <Button variant="ghost" onClick={() => setSetup(null)}>Отмена</Button>
              <Button loading={busy} disabled={code.length !== 6} onClick={confirm}>Подтвердить</Button>
            </div>
          </div>
        )}
      </Dialog>

      <Dialog open={offOpen} onClose={() => setOffOpen(false)} title="Выключить двухфакторный вход" maxWidth="max-w-md">
        <div className="space-y-4">
          <div>
            <label htmlFor="totp-off-pass" className="block text-sm text-secondary mb-1">Пароль</label>
            <Input id="totp-off-pass" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <div>
            <label htmlFor="totp-off-code" className="block text-sm text-secondary mb-1">Код из приложения</label>
            <Input id="totp-off-code" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(digits(e.target.value))} className="font-mono tabular-nums tracking-widest" />
          </div>
          {err && <p role="alert" className="text-sm text-danger">{err}</p>}
          <div className="flex flex-col sm:flex-row gap-2 sm:justify-end">
            <Button variant="ghost" onClick={() => setOffOpen(false)}>Отмена</Button>
            <Button variant="danger" loading={busy} disabled={!password || code.length !== 6} onClick={turnOff}>Выключить</Button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
