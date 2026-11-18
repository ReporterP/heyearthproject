import { useState } from 'react'
import { Pencil, Check, X } from 'lucide-react'
import { useT } from '../../i18n/index.jsx'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { updateUser } from '../../api/admin.js'
import { Input, Select, Button, Badge } from '../ui/index.js'
import { formatDate } from '../../utils/format.js'
import { telegramId, passwordState } from '../../utils/users.js'

function EditableField({ label, value, mono, type = 'text', options, canEdit, onSave, inputMode }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value ?? '')
  const [busy, setBusy] = useState(false)

  async function commit() {
    setBusy(true)
    try {
      await onSave(draft)
      setEditing(false)
    } catch {
      // toast handled in onSave; keep editing mode open so user can retry
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="group grid grid-cols-[140px_1fr] gap-3 items-start py-2 border-b border-default last:border-b-0">
      <div className="text-sm text-tertiary pt-1.5">{label}</div>
      {!editing ? (
        <div className="flex items-center gap-2 min-h-[32px]">
          <span className={`text-sm text-primary ${mono ? 'font-mono' : ''}`}>
            {value || <span className="text-tertiary">—</span>}
          </span>
          {canEdit && (
            <button
              onClick={() => { setDraft(value ?? ''); setEditing(true) }}
              aria-label="Редактировать"
              className="opacity-0 group-hover:opacity-100 hover:bg-hover rounded p-1 transition"
            >
              <Pencil className="w-3 h-3 text-tertiary" />
            </button>
          )}
        </div>
      ) : (
        <div className="flex items-center gap-1">
          {options ? (
            <Select value={draft} onChange={e => setDraft(e.target.value)} className="flex-1">
              {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </Select>
          ) : (
            <Input
              type={type}
              inputMode={inputMode}
              value={draft}
              onChange={e => setDraft(e.target.value)}
              autoFocus
              className="flex-1"
            />
          )}
          <Button size="sm" variant="primary" loading={busy} onClick={commit} aria-label="Сохранить" icon={<Check className="w-3.5 h-3.5" />} />
          <Button size="sm" variant="ghost" onClick={() => setEditing(false)} aria-label="Отмена" icon={<X className="w-3.5 h-3.5" />} />
        </div>
      )}
    </div>
  )
}

export default function ProfileTab({ user, onSaved }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const canEdit = can('users.write')

  async function patch(field, value) {
    try {
      await updateUser(user.id, { [field]: value })
      toast.success('Профиль сохранён')
      onSaved?.()
    } catch (err) {
      toast.error(err.message)
      throw err
    }
  }

  return (
    <div>
      {user.username && <EditableField label="Ник" value={user.username} canEdit={false} />}
      <EditableField label="Telegram ID" value={user.telegram_id} mono canEdit={canEdit} inputMode="numeric"
        onSave={v => {
          const n = telegramId(v)
          if (n === null) { toast.error('Telegram ID — число, например 123456789'); return Promise.reject(new Error('bad')) }
          return patch('telegram_id', n)
        }} />
      <EditableField label={t.fieldUserId}     value={user.user_id}      mono canEdit={false} />
      <EditableField label="Пароль" canEdit={false} value={(() => {
        const ps = passwordState(user)
        return (
          <span className="inline-flex items-center gap-2">
            <Badge variant={ps.variant}>{ps.label}</Badge>
            {user.password_must_change && user.password_reset_at && <span className="text-xs text-tertiary">сброшен {formatDate(user.password_reset_at)}</span>}
          </span>
        )
      })()} />
      <EditableField label={t.fieldCreatedAt}  value={formatDate(user.created_at)} canEdit={false} />
      {user.updated_at && (
        <EditableField label="Обновлён" value={formatDate(user.updated_at)} canEdit={false} />
      )}
    </div>
  )
}
