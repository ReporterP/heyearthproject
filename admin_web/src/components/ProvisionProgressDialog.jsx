import { useState, useEffect, useRef } from 'react'
import { useT } from '../i18n/index.jsx'
import { fetchActionProgress } from '../api/admin.js'

/* Step status icons */
function StepIcon({ status }) {
  if (status === 'done') {
    return (
      <svg className="w-5 h-5 text-green-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
      </svg>
    )
  }
  if (status === 'error') {
    return (
      <svg className="w-5 h-5 text-red-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
      </svg>
    )
  }
  if (status === 'running') {
    return (
      <svg className="w-5 h-5 text-brand animate-spin flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <circle cx="12" cy="12" r="10" strokeOpacity="0.25" />
        <path strokeLinecap="round" d="M12 2a10 10 0 019.95 9" />
      </svg>
    )
  }
  // pending
  return (
    <svg className="w-5 h-5 text-text-secondary flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <circle cx="12" cy="12" r="10" />
    </svg>
  )
}

export default function ProvisionProgressDialog({ open, server, onClose }) {
  const { t } = useT()
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
        setSteps(data.steps || [])
        setError(null)
        if (data.finished) {
          setFinished(true)
          clearInterval(intervalRef.current)
        }
      } catch (err) {
        if (!mounted) return
        setError(err.message)
      }
    }

    // Initial poll
    poll()
    // Start polling every 1 second
    intervalRef.current = setInterval(poll, 1000)

    return () => {
      mounted = false
      clearInterval(intervalRef.current)
    }
  }, [open, server?.id])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="bg-surface rounded-2xl p-6 w-full max-w-lg shadow-xl" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <h3 className="text-lg font-semibold text-text-primary mb-1">
          {t.serverSetupExitNode || 'Настройка Exit-ноды'}
        </h3>
        <p className="text-sm text-text-secondary mb-5">{server?.name}</p>

        {/* Steps list */}
        <div className="space-y-3 mb-6 max-h-80 overflow-y-auto">
          {steps.length === 0 && !error && (
            <div className="flex items-center gap-2 text-text-secondary text-sm">
              <svg className="w-5 h-5 animate-spin" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <circle cx="12" cy="12" r="10" strokeOpacity="0.25" />
                <path strokeLinecap="round" d="M12 2a10 10 0 019.95 9" />
              </svg>
              {t.loading || 'Загрузка...'}
            </div>
          )}
          {steps.map((step, idx) => (
            <div key={idx} className="flex items-start gap-3">
              <StepIcon status={step.status} />
              <div className="flex-1 min-w-0">
                <p className={`text-sm ${step.status === 'error' ? 'text-red-400' : step.status === 'done' ? 'text-text-primary' : 'text-text-secondary'}`}>
                  {step.description}
                </p>
                {step.error && (
                  <p className="text-xs text-red-400 mt-0.5">{step.error}</p>
                )}
              </div>
            </div>
          ))}
          {error && (
            <p className="text-sm text-red-400">{error}</p>
          )}
        </div>

        {/* Close button */}
        <div className="flex justify-end">
          <button
            onClick={onClose}
            disabled={!finished}
            className={`px-4 py-2 rounded-lg text-sm font-medium transition ${
              finished
                ? 'bg-brand text-white hover:bg-brand/80 cursor-pointer'
                : 'bg-surface-alt text-text-secondary cursor-not-allowed opacity-50'
            }`}
          >
            {t.close || 'Закрыть'}
          </button>
        </div>
      </div>
    </div>
  )
}
