import { useState, useEffect, useCallback, useRef } from 'react'

export default function usePagedData(fetchFn, { pageSize = 20, autoLoad = true } = {}) {
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const mountedRef = useRef(true)

  const load = useCallback(async (p = page, s = search) => {
    setLoading(true)
    setError('')
    try {
      const data = await fetchFn({ page: p, pageSize, search: s })
      if (!mountedRef.current) return
      setItems(data.items || data.data || [])
      setTotal(data.total || 0)
    } catch (err) {
      if (!mountedRef.current) return
      setError(err.message)
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }, [fetchFn, page, pageSize, search])

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  useEffect(() => {
    if (autoLoad) load(page, search)
  }, [page, search, autoLoad, load])

  const reload = useCallback(() => load(page, search), [load, page, search])

  const doSearch = useCallback((val) => {
    setSearch(val)
    setPage(1)
  }, [])

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return {
    items, total, page, setPage, search, doSearch,
    loading, error, reload, totalPages,
  }
}
