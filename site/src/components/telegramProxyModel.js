// Pure view-model for the cabinet «Прокси для Telegram» block.
//
// Input: the body of GET /api/v1/user/mtproto. Three shapes exist:
//   - new:    { enabled, web?: {link, tg_link, server, secret}, classic?: {link, tg_link, ...}, ...legacy }
//   - legacy: { enabled, host, port, secret, tg_deeplink, t_me_link }   (before phase T)
//   - off:    { enabled: false, reason? }
// The legacy flat fields are the classic proxy. They are read only when the response has
// neither a `web` nor a `classic` key (an old backend); a new backend that turned the classic
// proxy off must not have it resurrected from leftover flat fields.
//
// Rule (plan T4): the WEB server+secret are never presented as a classic MTProto pair — the
// view model carries no port for WEB and the copy text names the WEB proxy type only.
//
// No React, no DOM: safe to import from node for a quick check.

const WEB_LINK = /^https:\/\//i
const TG_LINK = /^tg:\/\//i

// Only the two schemes Telegram links use ever reach an href.
function pick(url, scheme) {
  return typeof url === 'string' && scheme.test(url.trim()) ? url.trim() : ''
}

function str(v) {
  return typeof v === 'string' ? v.trim() : ''
}

function normalizeWeb(raw) {
  if (!raw || typeof raw !== 'object') return null
  const link = pick(raw.link, WEB_LINK)
  const tgLink = pick(raw.tg_link, TG_LINK)
  if (!link && !tgLink) return null
  const server = str(raw.server)
  const secret = str(raw.secret)
  return {
    link,
    tgLink,
    server,
    secret,
    // manual entry needs both halves; with one missing the copy button is hidden
    copyText: server && secret ? webCopyText(server, secret) : '',
  }
}

function normalizeClassic(raw) {
  if (!raw || typeof raw !== 'object') return null
  const tgLink = pick(raw.tg_link, TG_LINK)
  const link = pick(raw.link, WEB_LINK)
  if (!tgLink && !link) return null
  return { tgLink, link }
}

function hasKey(obj, key) {
  return Object.prototype.hasOwnProperty.call(obj, key)
}

/** True when the response predates phase T (flat classic fields only). */
export function isLegacyShape(data) {
  return !!data && typeof data === 'object' && !hasKey(data, 'web') && !hasKey(data, 'classic')
}

/** Clipboard text for manual WEB-proxy entry. No port: WEB is not an MTProto host:port pair. */
export function webCopyText(server, secret) {
  return `Адрес: ${server}\nСекрет: ${secret}`
}

/**
 * normalizeTelegramProxy(data) → null | {
 *   web:     null | { link, tgLink, server, secret, copyText },
 *   classic: null | { tgLink, link },
 *   primary: 'web' | 'classic',   // which section owns the block's single primary CTA
 * }
 * null means «render nothing» (disabled, no subscription, or nothing usable in the payload).
 */
export function normalizeTelegramProxy(data) {
  if (!data || typeof data !== 'object' || !data.enabled) return null

  const web = normalizeWeb(data.web)
  const classic = isLegacyShape(data)
    ? normalizeClassic({ tg_link: data.tg_deeplink, link: data.t_me_link })
    : normalizeClassic(data.classic)

  if (!web && !classic) return null
  return { web, classic, primary: web ? 'web' : 'classic' }
}

/**
 * Quick links for the dashboard connect hero, in display order.
 * WEB present → «Подключить Telegram» (t.me/webproxy); plus the classic proxy labelled for
 * iPhone when it exists, because there is no iOS client for the WEB proxy and iPhone users
 * would otherwise lose the one-tap path they had before.
 * Classic only → the previous pair: tg:// deeplink and the t.me page.
 * Each item: { key, label, href, external } — external links open in a new tab.
 */
export function dashboardTelegramLinks(vm) {
  if (!vm) return []
  const out = []
  if (vm.web) {
    out.push(vm.web.link
      ? { key: 'web', label: 'Подключить Telegram', href: vm.web.link, external: true }
      : { key: 'web', label: 'Подключить Telegram', href: vm.web.tgLink, external: false })
    if (vm.classic) {
      const href = vm.classic.tgLink || vm.classic.link
      out.push({ key: 'classic', label: 'Telegram на iPhone', href, external: !vm.classic.tgLink })
    }
    return out
  }
  if (vm.classic) {
    if (vm.classic.tgLink) out.push({ key: 'classic-tg', label: 'Открыть в Telegram', href: vm.classic.tgLink, external: false })
    if (vm.classic.link) out.push({ key: 'classic-tme', label: 'Открыть через t.me', href: vm.classic.link, external: true })
  }
  return out
}
