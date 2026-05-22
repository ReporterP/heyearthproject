import { useEffect, useRef, useState } from 'react'
import { getConnections } from '../api/user'

const POLL_INTERVAL = 30_000 // 30 sec
const LAST_VERSION_KEY = 'subscription_version_last_seen'

/**
 * AvailableLocations — polls /api/v1/user/connections every 30 sec.
 * When subscription_version increments (admin provisioned/deprovisioned
 * a server), shows a toast and refetches the latest connection bundle.
 *
 * Locations rendering is best-effort — backend may not yet expose a
 * locations[] array; if absent we show a quiet placeholder.
 */
export default function AvailableLocations({ token }) {
  const [conns, setConns] = useState(null)
  const [error, setError] = useState(null)
  const [toast, setToast] = useState(null)
  const lastVersionRef = useRef(
    parseInt(localStorage.getItem(LAST_VERSION_KEY) || '0', 10)
  )

  useEffect(() => {
    if (!token) return
    let cancelled = false

    async function tick() {
      try {
        const data = await getConnections(token)
        if (cancelled) return
        const ver = data.subscription_version || data.__subscriptionVersionHeader || 0
        if (lastVersionRef.current && ver > lastVersionRef.current) {
          setToast('Подписка обновлена администратором. Список локаций синхронизирован.')
          setTimeout(() => setToast(null), 6000)
        }
        if (ver > 0) {
          lastVersionRef.current = ver
          localStorage.setItem(LAST_VERSION_KEY, String(ver))
        }
        setConns(data)
        setError(null)
      } catch (e) {
        if (!cancelled) setError(e.message)
      }
    }

    tick()
    const handle = setInterval(tick, POLL_INTERVAL)
    return () => {
      cancelled = true
      clearInterval(handle)
    }
  }, [token])

  if (!conns && !error) return null

  const locations = Array.isArray(conns?.locations) ? conns.locations : []

  return (
    <>
      {locations.length > 0 && (
        <div className="rounded-xl border border-border bg-bg-surface p-4 mb-4">
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-sm font-semibold text-text-primary">Доступные локации</h3>
            <span className="text-xs text-text-secondary">обновление каждые 30 сек</span>
          </div>
          {error && <p className="text-xs text-red-400 mb-2">{error}</p>}
          <ul className="flex flex-wrap gap-2">
            {locations.map((loc) => (
              <li
                key={loc.id || loc.name}
                className="px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-300 text-xs"
              >
                {loc.country_code ? `${loc.country_code} · ` : ''}{loc.name}
              </li>
            ))}
          </ul>
        </div>
      )}
      {toast && (
        <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-40 px-4 py-2 rounded-lg bg-blue-500/90 text-white text-sm shadow-lg">
          {toast}
        </div>
      )}
    </>
  )
}
