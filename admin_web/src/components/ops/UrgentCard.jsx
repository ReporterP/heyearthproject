import { useState } from 'react'
import { Button, Card, CardBody, CardHeader, Input } from '../ui/index.js'

/**
 * UrgentCard — «Срочное обновление»: clients update the subscription hourly until the deadline.
 * Props: until (RFC 3339 or ""), formatTime(iso), canWrite, busy, onSet(hours).
 */
export default function UrgentCard({ until, formatTime, canWrite, busy, onSet }) {
  const [hours, setHours] = useState('24')
  const active = until && Date.parse(until) > Date.now()
  return (
    <Card>
      <CardHeader className="text-sm font-medium text-primary">Срочное обновление</CardHeader>
      <CardBody className="space-y-3 text-sm">
        <p className="text-secondary">{active ? `Включено до ${formatTime(until)}: приложения обновляют подписку каждый час.` : 'Выключено: приложения обновляют подписку по обычному интервалу.'}</p>
        {canWrite && (
          <div className="flex flex-col sm:flex-row sm:items-center gap-2">
            <Input className="w-full sm:w-24 font-mono tabular-nums" type="number" min="1" max="48" aria-label="Часов" value={hours} onChange={e => setHours(e.target.value)} />
            <Button size="sm" variant="secondary" disabled={!!busy} loading={busy === 'urgent-on'} onClick={() => onSet(Number(hours))}>Включить на {hours || '…'} ч</Button>
            {active && <Button size="sm" variant="ghost" disabled={!!busy} loading={busy === 'urgent-off'} onClick={() => onSet(0)}>Выключить</Button>}
          </div>
        )}
      </CardBody>
    </Card>
  )
}
