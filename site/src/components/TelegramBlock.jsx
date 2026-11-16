import { useEffect, useId, useState } from 'react'
import { getMtproto } from '../api/user.js'
import { copyToClipboard } from './CopyButton.jsx'
import { normalizeTelegramProxy } from './telegramProxyModel.js'

/* ── Icons (stroke, Lucide-style, MASTER §6) ── */
const IconSend = (p) => (
  <svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="m22 2-7 20-4-9-9-4Z" />
    <path d="M22 2 11 13" />
  </svg>
)
const IconCopy = (p) => (
  <svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="9" y="9" width="12" height="12" rx="2" />
    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
  </svg>
)
const IconCheck = (p) => (
  <svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M20 6 9 17l-5-5" />
  </svg>
)

/* Buttons stack full-width on phones; every target is at least 44px tall. */
const BTN_PRIMARY = 'btn-primary w-full sm:w-auto min-h-[44px] !py-3 !text-sm'
const BTN_GHOST = 'btn-ghost w-full sm:w-auto min-h-[44px] !py-3 !text-sm'
const TEXT_LINK = 'inline-flex items-center justify-center w-full sm:w-auto min-h-[44px] px-2 rounded-md text-sm font-medium text-brand hover:text-brand-light hover:underline underline-offset-4 transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand'
const SECTION = 'rounded-2xl border bg-surface-2/70 p-4 md:p-5'

const NEW_TAB = <span className="sr-only"> (откроется в новой вкладке)</span>

function WebSection({ web, primary }) {
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState('')

  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(t)
  }, [copied])

  async function copy() {
    setCopyError('')
    let ok = false
    try { ok = await copyToClipboard(web.copyText) } catch { ok = false }
    if (ok) setCopied(true)
    else setCopyError('Браузер не дал доступ к буферу обмена. Выделите адрес и секрет выше и скопируйте их вручную.')
  }

  // t.me/webproxy is the main path; tg://webproxy is the fallback for when t.me does not hand off to the app
  const main = web.link ? { href: web.link, external: true } : { href: web.tgLink, external: false }
  const fallback = web.link && web.tgLink ? web.tgLink : ''

  return (
    <div className={`${SECTION} ${primary ? 'border-brand/25' : 'border-brand/10'}`}>
      <h3 className="text-base font-semibold">WEB-прокси</h3>
      <p className="text-xs text-text-secondary mt-1">Telegram Desktop 7.1+ и Android — обновите Telegram до последней версии</p>

      <div className="mt-4 flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-4">
        <a
          href={main.href}
          {...(main.external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
          className={primary ? BTN_PRIMARY : BTN_GHOST}
        >
          <IconSend className="w-4 h-4" />
          Подключить
          {main.external && NEW_TAB}
        </a>
        {fallback && <a href={fallback} className={TEXT_LINK}>Открыть в приложении</a>}
      </div>

      {web.copyText && (
        <div className="mt-4 pt-4 border-t border-brand/10">
          {/* WEB address is a hostname only — no port, never a «server:port» MTProto pair (plan T4) */}
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-faint">Адрес</dt>
            <dd className="font-mono text-text-primary break-all select-all">{web.server}</dd>
            <dt className="text-faint">Секрет</dt>
            <dd className="font-mono text-text-secondary break-all select-all">{web.secret}</dd>
          </dl>
          <button type="button" onClick={copy} className={`${BTN_GHOST} mt-3`}>
            {copied ? <IconCheck className="w-4 h-4 text-brand" /> : <IconCopy className="w-4 h-4" />}
            {copied ? 'Скопировано' : 'Скопировать адрес и секрет'}
          </button>
          <p role="status" aria-live="polite" className={copyError ? 'text-xs text-danger mt-2' : 'sr-only'}>
            {copyError || (copied ? 'Адрес и секрет скопированы' : '')}
          </p>
          <p className="text-xs text-faint mt-2 leading-relaxed">
            Если кнопка не сработала: Telegram → Настройки → Данные и память → Прокси → Добавить прокси → тип
            WEB-прокси, вставьте адрес и секрет.
          </p>
        </div>
      )}
    </div>
  )
}

function ClassicSection({ classic, primary }) {
  // When there is no WEB proxy the first classic link owns the block's primary CTA.
  const links = [
    classic.tgLink && { key: 'tg', href: classic.tgLink, label: 'Открыть в Telegram', external: false },
    classic.link && { key: 'tme', href: classic.link, label: 'Открыть через t.me', external: true },
  ].filter(Boolean)

  return (
    <div className={`${SECTION} border-brand/10`}>
      <h3 className="text-base font-semibold">Классический прокси</h3>
      <p className="text-xs text-text-secondary mt-1">Для iPhone/iPad и старых версий Telegram</p>
      <div className="mt-4 flex flex-col sm:flex-row gap-2 sm:gap-3">
        {links.map((l, i) => (
          <a
            key={l.key}
            href={l.href}
            {...(l.external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
            className={primary && i === 0 ? BTN_PRIMARY : BTN_GHOST}
          >
            {l.label}
            {l.external && NEW_TAB}
          </a>
        ))}
      </div>
    </div>
  )
}

/**
 * TelegramBlock — «Прокси для Telegram» in the cabinet. Fetches /api/v1/user/mtproto on mount:
 * the WEB proxy first (when the API returns `web`), the classic proxy second (`classic`, or the
 * legacy flat fields from an older backend). Renders nothing while loading, when the feature is
 * off / the user has no active subscription, or on a fetch error.
 */
export default function TelegramBlock({ token }) {
  const [vm, setVm] = useState(null)
  const titleId = useId()

  useEffect(() => {
    if (!token) return
    let cancelled = false
    getMtproto(token)
      .then((d) => { if (!cancelled) setVm(normalizeTelegramProxy(d)) })
      .catch(() => { if (!cancelled) setVm(null) }) // the block simply stays hidden
    return () => { cancelled = true }
  }, [token])

  if (!vm) return null

  return (
    <section aria-labelledby={titleId} className="card rounded-[22px] p-6 md:p-7 mb-5 animate-fade-in-up">
      <div className="flex items-start gap-3 mb-5">
        <span className="w-11 h-11 rounded-xl bg-brand/12 border border-brand/20 text-brand flex items-center justify-center flex-shrink-0">
          <IconSend className="w-5 h-5" />
        </span>
        <div className="min-w-0">
          <h2 id={titleId} className="serif text-lg font-semibold">Прокси для Telegram</h2>
          <p className="text-sm text-text-secondary mt-0.5">Отдельный канал только для мессенджера, помимо основного подключения.</p>
        </div>
      </div>

      <div className="space-y-3">
        {vm.web && <WebSection web={vm.web} primary={vm.primary === 'web'} />}
        {vm.classic && <ClassicSection classic={vm.classic} primary={vm.primary === 'classic'} />}
      </div>
    </section>
  )
}
