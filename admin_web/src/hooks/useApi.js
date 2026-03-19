import { useState, useCallback, useRef, useEffect } from 'react'

export default function useApi(apiFn) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const execute = useCallback(async (...args) => {
    setLoading(true)
    setError('')
    try {
      const result = await apiFn(...args)
      if (mountedRef.current) {
        setData(result)
        setLoading(false)
      }
      return result
    } catch (err) {
      if (mountedRef.current) {
        setError(err.message)
        setLoading(false)
      }
      throw err
    }
  }, [apiFn])

  return { data, loading, error, execute }
}
