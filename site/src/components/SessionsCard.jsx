import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '../api/http.js'
import Alert from './Alert.jsx'
import Badge from './Badge.jsx'
import Button from './Button.jsx'
import { useToast } from './Toast.jsx'
import { dateTime } from '../lib/format.js'

// «Входы» in Settings (spec 08): where the account is signed in — never an address — with
// «Закрыть» per sign-in and «Выйти везде, кроме этого» (QA 2026-11-18).
const SOURCE = { site: 'Сайт', app: 'Приложение', telegram: 'Telegram' }

export default function SessionsCard({ token }) {
  const toast = useToast()
  const [items, setItems] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(null) // session id being closed, or 'others'

  const load = useCallback(() => {
    setError('')
    apiFetch('/api/v1/user/sessions', { token })
      .then((d) => setItems(d?.items || []))
      .catch((e) => { if (e.kind !== 'auth') setError(e.message || 'Не удалось загрузить входы.') })
  }, [token])
  useEffect(() => { load() }, [load])

  async function closeOne(id) {
    setBusy(id)
    try {
      await apiFetch(`/api/v1/user/sessions/${id}`, { token, method: 'DELETE' })
      setItems((list) => (list || []).filter((s) => s.id !== id))
      toast.ok('Вход закрыт')
    } catch (e) {
      toast.bad(e.message || 'Не удалось закрыть вход.')
    } finally {
      setBusy(null)
    }
  }

  async function closeOthers() {
    setBusy('others')
    try {
      await apiFetch('/api/v1/user/sessions/close-others', { token, method: 'POST' })
      setItems((list) => (list || []).filter((s) => s.current))
      toast.ok('Остальные входы закрыты')
    } catch (e) {
      toast.bad(e.message || 'Не удалось закрыть входы.')
    } finally {
      setBusy(null)
    }
  }

  if (error) {
    return (
      <Alert tone="bad" title="Не удалось загрузить входы.">
        {error}
        <div style={{ marginTop: 8 }}><Button variant="ghost" size="sm" onClick={load}>Повторить</Button></div>
      </Alert>
    )
  }
  if (!items) return <p className="t-sm subtle" aria-busy="true">Загружаем входы…</p>

  return (
    <div className="stack gap-3">
      {items.map((s) => (
        <div key={s.id} className="row between gap-3" style={{ minWidth: 0 }}>
          <span style={{ minWidth: 0 }}>
            <b className="t-sm">{SOURCE[s.source] || 'Вход'}</b>
            {s.current && <Badge tone="ok" style={{ marginLeft: 8 }}>это устройство</Badge>}
            <br /><span className="t-xs subtle">с {dateTime(s.created_at)}</span>
          </span>
          {!s.current && (
            <Button variant="ghost" size="sm" loading={busy === s.id} disabled={busy !== null} onClick={() => closeOne(s.id)}>Закрыть</Button>
          )}
        </div>
      ))}
      {items.length > 1 && (
        <div><Button variant="secondary" size="sm" loading={busy === 'others'} disabled={busy !== null} onClick={closeOthers}>Выйти везде, кроме этого</Button></div>
      )}
    </div>
  )
}
