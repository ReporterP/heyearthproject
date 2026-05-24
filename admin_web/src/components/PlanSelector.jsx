import { useEffect, useState } from 'react'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

/**
 * PlanSelector — reusable multi-select for subscription plans.
 *
 * Props:
 *   value:    number[]  selected plan IDs (controlled)
 *   onChange: (number[]) => void
 *   disabled: bool       optional, default false
 *
 * Fetches /api/admin/plans on mount, sorted by sort_order then name.
 * Renders as a collapsible <details> showing "Выбрано: N" so it stays compact
 * inside parent dialogs.
 */
export default function PlanSelector({ value = [], onChange, disabled = false }) {
  const { token } = useAuth()
  const { showError } = useToast()
  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const resp = await fetch('/api/admin/plans', {
          headers: { Authorization: `Bearer ${token}` },
        })
        if (!resp.ok) {
          showError('Не удалось загрузить список планов')
          return
        }
        const data = await resp.json()
        if (cancelled) return
        const list = Array.isArray(data) ? data : (data.items || [])
        list.sort((a, b) => {
          const so = (a.sort_order ?? 0) - (b.sort_order ?? 0)
          if (so !== 0) return so
          return (a.name || '').localeCompare(b.name || '')
        })
        setPlans(list)
      } catch (e) {
        if (!cancelled) showError('Ошибка сети: ' + e.message)
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [token])  // eslint-disable-line react-hooks/exhaustive-deps

  const toggle = (id) => {
    if (disabled) return
    const set = new Set(value)
    if (set.has(id)) set.delete(id)
    else set.add(id)
    onChange(Array.from(set).sort((a, b) => a - b))
  }

  const priceLabel = (p) => (p.price_rub === 0 ? 'Бесплатно' : `${p.price_rub}₽`)

  return (
    <details className="rounded border border-slate-700 bg-slate-900/50">
      <summary className="cursor-pointer px-3 py-2 text-sm text-slate-200 hover:bg-slate-800">
        Планы: выбрано {value.length}{loading ? ' (загрузка…)' : ''}
      </summary>
      <div className="max-h-64 overflow-y-auto p-2 space-y-1">
        {plans.length === 0 && !loading && (
          <div className="text-xs text-slate-400 px-2 py-1">Нет планов</div>
        )}
        {plans.map((p) => (
          <label key={p.id} className="flex items-center gap-2 px-2 py-1 rounded hover:bg-slate-800 cursor-pointer">
            <input
              type="checkbox"
              className="h-4 w-4"
              checked={value.includes(p.id)}
              onChange={() => toggle(p.id)}
              disabled={disabled}
            />
            <span className="text-sm text-slate-200 flex-1">{p.name || p.slug}</span>
            <span className="text-xs text-slate-400">{priceLabel(p)}</span>
          </label>
        ))}
      </div>
    </details>
  )
}
