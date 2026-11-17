import { useEffect, useState } from 'react'
import { getMtproto } from '../api/user.js'
import { normalizeTelegramProxy } from '../components/telegramProxyModel.js'

// The Telegram proxy of the account (GET /api/v1/user/mtproto) as the view model of
// telegramProxyModel.js; null while loading, when switched off, for no subscription or on error.
let cache = { token: '', vm: undefined }

export default function useMtproto(token, enabled = true) {
  const [vm, setVm] = useState(cache.token === token ? cache.vm ?? null : null)
  useEffect(() => {
    if (!token || !enabled) return undefined
    let alive = true
    getMtproto(token)
      .then((d) => { const v = normalizeTelegramProxy(d); cache = { token, vm: v }; if (alive) setVm(v) })
      .catch(() => { if (alive) setVm(null) })
    return () => { alive = false }
  }, [token, enabled])
  return vm
}
