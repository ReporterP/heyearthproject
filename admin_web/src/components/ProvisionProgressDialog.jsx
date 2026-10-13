import { useState, useEffect, useRef } from 'react'
import { Check, Circle, X } from 'lucide-react'
import { fetchActionProgress } from '../api/admin.js'
import { Button, Dialog, Spinner } from './ui/index.js'

function StepIcon({ status }) {
  if (status === 'done') return <Check className="w-4 h-4 text-success shrink-0" aria-label="готово" />
  if (status === 'error') return <X className="w-4 h-4 text-danger shrink-0" aria-label="ошибка" />
  if (status === 'running') return <Spinner size="xs" />
  return <Circle className="w-4 h-4 text-tertiary shrink-0" aria-label="ожидает" />
}

// Progress of a long server operation. Closable at any moment: the operation keeps running on the
// server, closing only stops watching (the old dialog stayed locked until "finished", and an API
// restart mid-operation locked it forever).
export default function ProvisionProgressDialog({ open, server, title = 'Операция с нодой', onClose }) {
  const [steps, setSteps] = useState([])
  const [finished, setFinished] = useState(false)
  const [error, setError] = useState(null)
  const intervalRef = useRef(null)

  useEffect(() => {
    if (!open || !server?.id) return
    let mounted = true
    async function poll() {
      try {
        const data = await fetchActionProgress(server.id)
        if (!mounted) return
        if (data.active === false) {
          setFinished(true)
          setError('API не помнит эту операцию (возможно, он перезапускался). Проверьте состояние ноды на её странице.')
          clearInterval(intervalRef.current)
          return
        }
        setSteps(data.steps || [])
        setError(null)
        if (data.finished) {
          setFinished(true)
          clearInterval(intervalRef.current)
        }
      } catch (err) {
        if (!mounted) return
        setError(err.message)
        if (err.status === 403) clearInterval(intervalRef.current) // no permission: polling will not help
      }
    }
    poll()
    intervalRef.current = setInterval(poll, 1000)
    return () => {
      mounted = false
      clearInterval(intervalRef.current)
    }
  }, [open, server?.id])

  return (
    <Dialog open={open} onClose={onClose} title={title} maxWidth="max-w-lg">
      <p className="text-sm text-secondary mb-4">{server?.name}</p>
      <div className="space-y-3 mb-4 max-h-80 overflow-y-auto">
        {steps.length === 0 && !error && (
          <div className="flex items-center gap-2 text-sm text-secondary"><Spinner size="xs" /> Ждём данные о шагах…</div>
        )}
        {steps.map((step, idx) => (
          <div key={idx} className="flex items-start gap-3">
            <StepIcon status={step.status} />
            <div className="flex-1 min-w-0">
              <p className={`text-sm ${step.status === 'error' ? 'text-danger' : step.status === 'done' ? 'text-primary' : 'text-secondary'}`}>
                {step.description}
              </p>
              {step.error && <p className="text-xs text-danger mt-0.5 break-all">{step.error}</p>}
            </div>
          </div>
        ))}
        {error && <p className="text-sm text-danger">{error}</p>}
      </div>
      {!finished && <p className="text-xs text-tertiary mb-4">Окно можно закрыть — операция продолжится на сервере.</p>}
      <div className="flex justify-end">
        <Button variant={finished ? 'primary' : 'secondary'} onClick={onClose}>{finished ? 'Готово' : 'Закрыть'}</Button>
      </div>
    </Dialog>
  )
}
