import { useEffect, useState } from 'react'
import { getEmailPrefs, setEmail as requestEmail, forgetEmail, setEmailReminders } from '../api/user.js'

// «Почта для чеков и напоминаний» (cleanup plan E8): an address is saved only after the link in
// the letter is opened; reminders can be switched off here or from any reminder.
export default function EmailCard({ token }) {
  const [prefs, setPrefs] = useState(null)
  const [loadError, setLoadError] = useState('')
  const [value, setValue] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  useEffect(() => {
    getEmailPrefs(token).then(setPrefs).catch((e) => { if (e.kind !== 'auth') setLoadError(e.message) })
  }, [token])

  async function send(e) {
    e.preventDefault()
    setBusy(true); setErr(''); setMsg('')
    try {
      const r = await requestEmail(token, value.trim())
      setPrefs((p) => ({ ...p, pending_email: r.pending_email }))
      setMsg(r.message || 'Отправили письмо — откройте ссылку из него')
      setEditing(false); setValue('')
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true); setErr(''); setMsg('')
    try {
      await forgetEmail(token)
      setPrefs({ email: '', pending_email: '', reminders_off: false })
      setMsg('Адрес удалён')
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  async function toggleReminders(on) {
    setBusy(true); setErr('')
    try {
      const r = await setEmailReminders(token, on)
      setPrefs((p) => ({ ...p, reminders_off: r.reminders_off }))
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }

  const form = (
    <form onSubmit={send} className="flex flex-col sm:flex-row gap-2 mt-3">
      <label htmlFor="cab-email" className="sr-only">Адрес почты</label>
      <input
        id="cab-email" type="email" inputMode="email" autoComplete="email" required value={value}
        onChange={(e) => setValue(e.target.value)} placeholder="you@mail.ru"
        className="flex-1 px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20"
      />
      <button type="submit" disabled={busy || !value.trim()} className="btn-primary !py-3 !text-sm disabled:opacity-50">Подтвердить</button>
    </form>
  )

  return (
    <div className="card rounded-[22px] p-6 mb-4 animate-fade-in-up delay-100">
      <h2 className="serif text-base font-semibold mb-1">Почта для чеков и напоминаний</h2>
      <p className="text-xs text-text-secondary mb-3">Пришлём чек об оплате с кошелька и напомним за 3 дня до конца подписки. Больше ничего.</p>

      {loadError ? <p className="text-sm text-danger">{loadError}</p>
        : !prefs ? <div className="skeleton h-10 rounded-xl" />
        : (
          <>
            {prefs.email && !editing && (
              <div className="flex flex-wrap items-center gap-3">
                <span className="text-sm font-medium break-all">{prefs.email}</span>
                <span className="text-[11px] px-2 py-0.5 rounded bg-brand/12 text-brand">подтверждена</span>
                <button onClick={() => setEditing(true)} className="text-xs text-brand hover:text-brand-light">Изменить</button>
                <button onClick={remove} disabled={busy} className="text-xs text-text-secondary hover:text-danger">Удалить</button>
              </div>
            )}
            {prefs.pending_email && (
              <p className="text-xs text-accent mt-2">Ждём подтверждения: {prefs.pending_email}. Откройте ссылку из письма (проверьте и «Спам»).</p>
            )}
            {(!prefs.email || editing) && form}
            {prefs.email && (
              <label className="flex items-center gap-2.5 mt-4 text-sm text-text-secondary cursor-pointer select-none">
                <input type="checkbox" checked={!prefs.reminders_off} disabled={busy} onChange={(e) => toggleReminders(e.target.checked)} className="w-4 h-4 accent-[#34d399]" />
                Напоминать о конце подписки
              </label>
            )}
          </>
        )}
      {msg && <p className="text-xs text-brand mt-3">{msg}</p>}
      {err && <p className="text-xs text-danger mt-3">{err}</p>}
    </div>
  )
}
