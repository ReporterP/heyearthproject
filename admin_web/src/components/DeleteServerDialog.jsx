import { useState } from 'react'
import { Button, Dialog, Input } from './ui/index.js'

// Hard delete of a node, confirmed by typing delete-<id>.
export default function DeleteServerDialog({ serverId, serverName, onConfirm, onCancel }) {
  const [typed, setTyped] = useState('')
  const expected = `delete-${serverId}`
  const ok = typed.trim() === expected
  return (
    <Dialog open onClose={onCancel} title={`Удалить ноду #${serverId}?`} maxWidth="max-w-md">
      <p className="text-sm text-secondary mb-4">
        Нода «{serverName}» будет полностью удалена: туннель на Hub остановится, сервисы на самой
        ноде будут остановлены (если она доступна), артефакты сохранятся в архив, запись удалится.
        Это действие необратимо.
      </p>
      <label htmlFor="delete-server-confirm" className="block text-sm text-secondary mb-2">
        Для подтверждения введите <code className="font-mono text-primary bg-canvas px-1 rounded">{expected}</code>:
      </label>
      <Input
        id="delete-server-confirm"
        value={typed}
        onChange={(e) => setTyped(e.target.value)}
        placeholder={expected}
        className="font-mono mb-4"
        autoComplete="off"
        autoFocus
      />
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>Отмена</Button>
        <Button variant="danger" onClick={onConfirm} disabled={!ok}>Удалить навсегда</Button>
      </div>
    </Dialog>
  )
}
