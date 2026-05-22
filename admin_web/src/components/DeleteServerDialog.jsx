import React, { useState } from 'react'

export default function DeleteServerDialog({ serverId, serverName, onConfirm, onCancel }) {
  const [typed, setTyped] = useState('')
  const expected = `delete-${serverId}`
  const ok = typed.trim() === expected
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div className="bg-bg-surface border border-border rounded-xl p-6 max-w-md w-full">
        <h3 className="text-lg font-semibold mb-2">Удалить сервер #{serverId}?</h3>
        <p className="text-sm text-text-secondary mb-4">
          Сервер «{serverName}» будет полностью удалён: остановлен hysteria2-клиент на хабе,
          выполнена попытка остановки сервисов на самой ноде, артефакты сохранены в архив,
          запись в БД удалена. Это действие необратимо.
        </p>
        <p className="text-sm mb-2">
          Для подтверждения введите <code className="text-text-primary bg-bg-app px-1 rounded">{expected}</code>:
        </p>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="w-full px-3 py-2 bg-bg-app border border-border rounded-md text-sm mb-4"
          placeholder={expected}
          autoFocus
        />
        <div className="flex justify-end gap-2">
          <button onClick={onCancel} className="px-4 py-2 text-sm rounded-md border border-border hover:bg-bg-app">
            Отмена
          </button>
          <button
            onClick={onConfirm}
            disabled={!ok}
            className="px-4 py-2 text-sm rounded-md bg-red-600 text-white disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Удалить навсегда
          </button>
        </div>
      </div>
    </div>
  )
}
