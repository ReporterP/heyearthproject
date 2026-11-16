import { useCallback, useEffect, useState } from 'react'
import { fetchPlans } from '../lib/plans.js'

// /api/v1/plans for a page: { list, loading, error, reload }. Shared for the page load.
let shared = null
export default function usePlans() {
  const [state, setState] = useState(shared ? { list: shared, loading: false, error: false } : { list: [], loading: true, error: false })
  const load = useCallback((force = false) => {
    if (shared && !force) { setState({ list: shared, loading: false, error: false }); return }
    setState((s) => ({ ...s, loading: true, error: false }))
    fetchPlans().then((list) => { shared = list; setState({ list, loading: false, error: false }) }, () => setState({ list: [], loading: false, error: true }))
  }, [])
  useEffect(() => { load() }, [load])
  return { ...state, reload: () => load(true) }
}
