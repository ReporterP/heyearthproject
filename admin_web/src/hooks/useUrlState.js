import { useNavigate, useLocation } from 'react-router-dom'

/**
 * useUrlState — keeps filter/sort/search state in the URL.
 *
 *   const [state, update] = useUrlState({ search: '', status: 'all', page: 1 })
 *   update({ search: 'foo' })  // → ?search=foo
 *   update({ search: '' })     // → drops param (back to default)
 *
 * Defaults type-driven: number defaults parsed as int, boolean as 'true'/'false'.
 */
export function useUrlState(defaults = {}) {
  const navigate = useNavigate()
  const location = useLocation()
  const params = new URLSearchParams(location.search)

  const state = { ...defaults }
  for (const key of Object.keys(defaults)) {
    const v = params.get(key)
    if (v === null) continue
    if (typeof defaults[key] === 'number') state[key] = parseInt(v, 10) || defaults[key]
    else if (typeof defaults[key] === 'boolean') state[key] = v === 'true'
    else state[key] = v
  }

  const update = (patch) => {
    const next = new URLSearchParams(location.search)
    for (const [k, v] of Object.entries(patch)) {
      if (v === defaults[k] || v === '' || v == null) next.delete(k)
      else next.set(k, String(v))
    }
    navigate({ search: next.toString() ? '?' + next.toString() : '' }, { replace: true })
  }

  return [state, update]
}
