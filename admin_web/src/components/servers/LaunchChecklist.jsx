import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { CheckCircle2, Circle, ChevronDown, ChevronUp, Rocket } from 'lucide-react'
import { fetchLaunchChecklist } from '../../api/admin.js'
import { Badge, Card, CardBody, CardHeader, Skeleton } from '../ui/index.js'
import { launchView } from './serversModel.js'

/**
 * LaunchChecklist — «Запуск» on the dashboard (GET /launch-checklist): Hub → two exits → an entry
 * in RU → checked from a phone → enabled for everyone → the domain on the entry. Each step comes
 * from the real state with a button into the right place; folds away once everything is done.
 */
export default function LaunchChecklist() {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [open, setOpen] = useState(false)
  useEffect(() => {
    let alive = true
    fetchLaunchChecklist()
      .then((d) => { if (alive) setData(d) })
      .catch((e) => { if (alive) setError(e?.message || 'ошибка') })
    return () => { alive = false }
  }, [])

  if (error) return <p role="alert" className="text-xs text-danger">Чек-лист «Запуск» не загрузился: {error}</p>
  if (!data) return <Skeleton className="h-20" />
  const v = launchView(data)
  const expanded = !v.collapsed || open
  return (
    <Card>
      <CardHeader className="flex items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-primary">
          <Rocket className="w-4 h-4 text-accent" aria-hidden="true" /> Запуск
          <Badge variant={v.collapsed ? 'success' : 'accent'}>{v.collapsed ? 'всё готово' : v.progress}</Badge>
        </h3>
        {v.collapsed && (
          <button type="button" onClick={() => setOpen(!open)} aria-expanded={open}
            className="inline-flex items-center gap-1 text-xs text-secondary hover:text-primary rounded focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">
            {open ? 'Свернуть' : 'Показать'}
            {open ? <ChevronUp className="w-3.5 h-3.5" aria-hidden="true" /> : <ChevronDown className="w-3.5 h-3.5" aria-hidden="true" />}
          </button>
        )}
      </CardHeader>
      {expanded && (
        <CardBody>
          <ol className="space-y-1">
            {v.items.map((it) => {
              const isNext = v.next?.key === it.key
              return (
                <li key={it.key} className={`flex flex-col sm:flex-row sm:items-center gap-2 rounded px-2 py-1.5 ${isNext ? 'bg-accent/10' : ''}`}>
                  <div className="flex items-start gap-2 flex-1 min-w-0">
                    {it.done
                      ? <CheckCircle2 className="w-4 h-4 text-success mt-0.5 shrink-0" aria-label="сделано" />
                      : <Circle className={`w-4 h-4 mt-0.5 shrink-0 ${isNext ? 'text-accent' : 'text-tertiary'}`} aria-label="не сделано" />}
                    <div className="min-w-0">
                      <p className={`text-sm ${it.done ? 'text-secondary' : 'text-primary'}`}>{it.title}</p>
                      {it.detail && <p className="text-xs text-tertiary break-words">{it.detail}</p>}
                    </div>
                  </div>
                  {!it.done && it.action && (
                    <Link to={it.action.href}
                      className={`shrink-0 inline-flex items-center justify-center h-7 px-2.5 rounded text-xs border transition-colors
                        ${isNext ? 'border-accent bg-accent text-white hover:bg-accent-hover' : 'border-default text-secondary hover:bg-hover hover:text-primary'}`}>
                      {it.action.label}
                    </Link>
                  )}
                </li>
              )
            })}
          </ol>
        </CardBody>
      )}
    </Card>
  )
}
