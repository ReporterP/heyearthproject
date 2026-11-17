import { useEffect, useState } from 'react'
import { KeyRound } from 'lucide-react'
import { fetchHubKey } from '../../api/admin.js'
import { CopyButton, Skeleton } from '../ui/index.js'

/**
 * HubKeyBox — the Hub's public SSH key to paste into the hoster's panel when creating a server
 * (field «SSH-ключ» for root): then no root password is needed in the wizard.
 */
export default function HubKeyBox({ className = '' }) {
  const [k, setK] = useState(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    let alive = true
    fetchHubKey()
      .then((r) => { if (alive) setK(r) })
      .catch((e) => { if (alive) setErr(e?.message || 'ошибка') })
    return () => { alive = false }
  }, [])
  return (
    <div className={`rounded border border-default bg-elevated p-3 ${className}`}>
      <div className="flex items-center gap-2 text-sm text-primary mb-1">
        <KeyRound className="w-4 h-4 text-accent" aria-hidden="true" />
        Ключ Hub для панели хостера
      </div>
      <p className="text-xs text-tertiary mb-2">
        При создании сервера вставьте его в поле «SSH-ключ» (для пользователя root) — тогда пароль сюда вводить не нужно.
      </p>
      {err && <p role="alert" className="text-xs text-danger break-words">Не удалось загрузить ключ Hub: {err}</p>}
      {!k && !err && <Skeleton className="h-8 w-full" />}
      {k && (
        <div className="flex items-start gap-1">
          <code className="flex-1 min-w-0 font-mono text-[11px] text-secondary break-all">{k.public_key}</code>
          <CopyButton text={k.public_key} label="Скопировать открытый ключ Hub" />
        </div>
      )}
    </div>
  )
}
