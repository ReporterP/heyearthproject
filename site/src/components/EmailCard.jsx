import { useEffect, useRef, useState } from 'react'
import { getEmailPrefs, setEmail as requestEmail, forgetEmail, setEmailReminders } from '../api/user.js'
import Alert from './Alert.jsx'
import Badge from './Badge.jsx'
import Button from './Button.jsx'
import Dialog, { DialogActions } from './Dialog.jsx'
import Field, { Input } from './Field.jsx'
import Skeleton from './Skeleton.jsx'
import { Switch } from './Controls.jsx'
import { useToast } from './Toast.jsx'

// «Почта для чеков и напоминаний» (spec 08 §8.4): an address is saved only after the link in the
// letter is opened (pending → confirmed); reminders can be switched off here or from any reminder.
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export default function EmailCard({ token }) {
  const toast = useToast()
  const [prefs, setPrefs] = useState(null)
  const [loadErr, setLoadErr] = useState(false)
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [del, setDel] = useState(false)
  const [delBusy, setDelBusy] = useState(false)
  const [remBusy, setRemBusy] = useState(false)
  const inFlight = useRef(false)

  const load = () => {
    setLoadErr(false)
    getEmailPrefs(token).then(setPrefs).catch((e) => { if (e.kind !== 'auth') setLoadErr(true) })
  }
  useEffect(load, [token]) // eslint-disable-line react-hooks/exhaustive-deps

  async function send(address) {
    const a = String(address || '').trim()
    if (!a) { setErr('Укажите email'); return }
    if (!EMAIL_RE.test(a)) { setErr('Проверьте адрес почты — в нём должна быть «@» и домен'); return }
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const r = await requestEmail(token, a)
      setPrefs((p) => ({ ...p, pending_email: r.pending_email }))
      setEditing(false)
      setValue('')
      toast.ok('Письмо отправлено — проверьте почту')
    } catch (e) {
      if (editing || !prefs?.email) setErr(e.message)
      else toast.bad(e.message)
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  async function remove() {
    setDelBusy(true)
    try {
      await forgetEmail(token)
      setPrefs({ email: '', pending_email: '', reminders_off: false })
      setDel(false)
      toast.ok('Адрес удалён')
    } catch {
      toast.bad('Не удалось удалить адрес — попробуйте ещё раз')
    } finally {
      setDelBusy(false)
    }
  }

  async function toggleReminders(on) {
    const prev = prefs.reminders_off
    setPrefs((p) => ({ ...p, reminders_off: !on }))
    setRemBusy(true)
    try {
      const r = await setEmailReminders(token, on)
      setPrefs((p) => ({ ...p, reminders_off: r.reminders_off }))
    } catch {
      setPrefs((p) => ({ ...p, reminders_off: prev }))
      toast.bad('Не удалось изменить напоминания')
    } finally {
      setRemBusy(false)
    }
  }

  if (loadErr) {
    return <div className="card"><Alert tone="bad" title="Не удалось загрузить почту.">Обновите страницу. <button type="button" className="link" onClick={load} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Повторить</button></Alert></div>
  }
  if (!prefs) return <div className="card stack gap-3" aria-busy="true"><Skeleton w="60%" h={18} /><Skeleton w="40%" h={14} /></div>

  const form = (submitLabel, onCancel) => (
    <form className="swap" onSubmit={(e) => { e.preventDefault(); send(value) }} noValidate>
      <Field id="email-new" label="Почта" error={err || null} hint={err ? null : 'Используем только для чеков и напоминаний'}>
        <div className="input-group max-sm:flex-col">
          <Input id="email-new" aria-describedby="email-new-msg" type="email" inputMode="email" autoComplete="email" value={value} placeholder="you@example.ru"
            onChange={(e) => { setValue(e.target.value); setErr('') }} invalid={!!err} />
          <Button variant="secondary" type="submit" loading={busy}>{submitLabel}</Button>
          {onCancel && <Button variant="ghost" onClick={onCancel} disabled={busy}>Отмена</Button>}
        </div>
      </Field>
    </form>
  )

  return (
    <div className="card stack gap-3">
      {prefs.email && !editing && (
        <div className="row between wrap gap-2">
          <span className="row wrap gap-3"><b className="mono" style={{ wordBreak: 'break-all' }}>{prefs.email}</b><Badge tone="ok">Подтверждена</Badge></span>
          <span className="row gap-2">
            <Button variant="ghost" size="sm" onClick={() => { setEditing(true); setValue(''); setErr('') }}>Изменить</Button>
            <Button variant="ghost" size="sm" onClick={() => setDel(true)}>Удалить</Button>
          </span>
        </div>
      )}
      {prefs.pending_email && !editing && (
        <div className="stack gap-1" style={prefs.email ? { paddingTop: 12, borderTop: '1px solid var(--line)' } : undefined}>
          <span className="row wrap gap-3"><b className="mono" style={{ wordBreak: 'break-all' }}>{prefs.pending_email}</b><Badge tone="warn">Ждёт подтверждения</Badge></span>
          <span className="t-sm subtle">
            Откройте ссылку из письма (проверьте и «Спам»).{' '}
            <button type="button" className="link" disabled={busy} onClick={() => send(prefs.pending_email)} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Отправить письмо ещё раз</button>
          </span>
        </div>
      )}
      {editing && form('Сохранить', () => { setEditing(false); setErr('') })}
      {!prefs.email && !prefs.pending_email && !editing && form('Добавить')}
      {!prefs.email && prefs.pending_email && !editing && (
        <div><Button variant="ghost" size="sm" onClick={() => { setEditing(true); setValue(''); setErr('') }}>Указать другой адрес</Button></div>
      )}
      {prefs.email && (
        <div className="row between gap-3" style={{ paddingTop: 12, borderTop: '1px solid var(--line)' }}>
          <label htmlFor="rem" style={{ cursor: 'pointer' }}><b>Напоминания о конце подписки</b><br /><span className="t-sm subtle">Чеки приходят всегда</span></label>
          <Switch id="rem" checked={!prefs.reminders_off} disabled={remBusy} onChange={toggleReminders} />
        </div>
      )}

      <Dialog open={del} onClose={() => setDel(false)} title="Удалить почту?" busy={delBusy}>
        <p className="muted t-sm" style={{ marginTop: 8 }}>Чеки и напоминания перестанут приходить.</p>
        <DialogActions>
          <Button variant="ghost" onClick={() => setDel(false)} disabled={delBusy} data-autofocus>Отмена</Button>
          <Button variant="danger" onClick={remove} loading={delBusy}>Удалить</Button>
        </DialogActions>
      </Dialog>
    </div>
  )
}
