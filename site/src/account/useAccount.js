// One place that loads the cabinet's data (cleanup plan E6). A page asks for the parts it needs;
// a part loaded less than a minute ago is reused and a request already in flight is shared, so
// going Dashboard → Подключение → Оплата does not refetch the profile on every page.
//
//   const { data, loading, error, reload } = useAccount(['profile', 'wallet'], { optional: ['wallet'] })
//
// A required part that fails puts its error in `error` (401 already signed the site out:
// apiFetch). An optional part that fails is null. After something changes the account (a
// payment, a promocode) call invalidateAccount() or reload().
import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import { getProfile, getSubscription, getTrafficStats, getSubscribeLink, getConnections } from '../api/user.js'
import { getWallet } from '../api/wallet.js'

const FETCHERS = {
  profile: getProfile,
  subscription: getSubscription,
  traffic: getTrafficStats,
  subLink: getSubscribeLink,
  connections: getConnections,
  wallet: getWallet,
}

export const FRESH_MS = 60_000

let cache = { token: '', parts: {} } // part → { value, at } | { promise }

function cached(token, part) {
  if (cache.token !== token) return undefined
  const c = cache.parts[part]
  return c && 'value' in c && Date.now() - c.at < FRESH_MS ? c.value : undefined
}

export function loadPart(token, part, force = false) {
  if (!FETCHERS[part]) return Promise.reject(new Error(`unknown account part ${part}`))
  if (cache.token !== token) cache = { token, parts: {} }
  const c = cache.parts[part]
  if (!force && c) {
    if (c.promise) return c.promise
    if (Date.now() - c.at < FRESH_MS) return Promise.resolve(c.value)
  }
  const mine = cache
  const promise = FETCHERS[part](token).then(
    (value) => { if (cache === mine) mine.parts[part] = { value, at: Date.now() }; return value },
    (err) => { if (cache === mine && mine.parts[part]?.promise === promise) delete mine.parts[part]; throw err },
  )
  mine.parts[part] = { promise }
  return promise
}

// invalidateAccount drops the named parts (all when none is named): the next read fetches them.
export function invalidateAccount(...parts) {
  if (parts.length === 0) cache.parts = {}
  else parts.forEach((p) => { delete cache.parts[p] })
}

export default function useAccount(parts, { optional = [] } = {}) {
  const { token } = useAuth()
  const key = parts.join(',')
  const opt = optional.join(',')
  const initial = () => {
    const data = {}
    let all = true
    for (const p of parts) {
      const v = cached(token, p)
      if (v === undefined) all = false
      data[p] = v ?? null
    }
    return { data, loading: !all, error: null }
  }
  const [state, setState] = useState(initial)
  const alive = useRef(true)
  useEffect(() => () => { alive.current = false }, [])

  const run = useCallback((force = false) => {
    if (!token) return Promise.resolve()
    const names = key ? key.split(',') : []
    const optSet = new Set(opt ? opt.split(',') : [])
    setState((s) => ({ ...s, loading: s.loading || force, error: null }))
    return Promise.all(names.map((p) => {
      const pr = loadPart(token, p, force)
      return optSet.has(p) ? pr.catch(() => null) : pr
    })).then((values) => {
      if (!alive.current) return
      const data = {}
      names.forEach((p, i) => { data[p] = values[i] ?? null })
      setState({ data, loading: false, error: null })
    }, (err) => {
      if (!alive.current) return
      setState((s) => ({ ...s, loading: false, error: err }))
    })
  }, [token, key, opt])

  useEffect(() => { run(false) }, [run])

  // reload refetches the named parts (all of this hook's when none is named) and re-reads.
  const reload = useCallback((...only) => {
    if (only.length) invalidateAccount(...only)
    return run(only.length === 0)
  }, [run])

  return { ...state, reload }
}
