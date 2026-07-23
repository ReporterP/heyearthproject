import { useEffect, useState } from 'react'
import { getMtproto } from '../api/user.js'

/**
 * TelegramProxyButton — fetches /api/v1/user/mtproto on mount.
 * Renders nothing if not enabled or user has no active subscription.
 * Shows two buttons: tg:// deeplink and t.me web fallback.
 */
export default function TelegramProxyButton({ token }) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    if (!token) {
      setLoading(false)
      return
    }
    getMtproto(token)
      .then((d) => {
        if (!cancelled) setData(d)
      })
      .catch(() => {
        // hide block on any error
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [token])

  if (loading) return null
  if (!data || !data.enabled) return null

  return (
    <div className="glass-card rounded-2xl p-5 md:p-6 mb-6 border border-brand/20 animate-fade-in-up">
      <div className="flex items-center gap-3 mb-3">
        <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-blue-400 flex items-center justify-center flex-shrink-0">
          <svg className="w-6 h-6 text-white" viewBox="0 0 24 24" fill="currentColor">
            <path d="M21.05 3.05 2.95 9.13c-.93.32-.92 1.6 0 1.92l4.62 1.55 1.55 4.62c.32.93 1.6.92 1.92 0l1.05-3.13 3.65 2.74c.7.52 1.7.13 1.86-.72L21.6 4.07c.17-.84-.6-1.55-1.55-1.02Z" />
          </svg>
        </div>
        <div>
          <h3 className="text-base font-semibold text-text-primary">Прокси для Telegram</h3>
          <p className="text-xs text-text-secondary">Бонус — быстрая настройка в один клик</p>
        </div>
      </div>
      <p className="text-sm text-text-secondary mb-3">
        Быстрый доступ к Telegram в один клик — отдельный канал только для мессенджера, помимо основного подключения.
      </p>
      <div className="flex flex-wrap gap-2">
        <a
          href={data.tg_deeplink}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-blue-500 hover:bg-blue-400 text-white text-sm font-medium transition min-h-[44px]"
        >
          Открыть в Telegram
        </a>
        <a
          href={data.t_me_link}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl glass hover:bg-white/10 text-sm text-text-secondary hover:text-text-primary transition min-h-[44px]"
        >
          Открыть через t.me
        </a>
      </div>
    </div>
  )
}
