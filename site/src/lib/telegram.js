// Telegram Mini App bridge (spec 12 §12.4). The site is the same; inside Telegram the root gets
// .he--tg, Telegram's BackButton follows the router history, MainButton repeats the screen's one
// primary button, toasts give haptic feedback and links leave the webview the Telegram way.
// telegram-web-app.js (a copy in /tg/, Telegram's official bridge) is loaded only on /tg and in
// an already opened Mini App (flag in sessionStorage) — never on the plain site.

const FLAG = 'he_tg'
const SCRIPT = '/tg/telegram-web-app.js'
const BG = '#090C14' // --bg
const ACCENT = '#22C55E' // --accent
const ACCENT_INK = '#052E14' // --accent-ink

let ready = null

function flagged() {
  try { return sessionStorage.getItem(FLAG) === '1' } catch { return false }
}

function launchedFromTelegram() {
  const h = typeof location !== 'undefined' ? location.hash : ''
  return h.includes('tgWebAppData=') || h.includes('tgWebAppVersion=')
}

// shouldLoad: the page may be a Mini App (the /tg entry, Telegram's launch hash, or the flag).
export function shouldLoad() {
  if (typeof window === 'undefined') return false
  return location.pathname === '/tg' || launchedFromTelegram() || flagged()
}

// webApp is Telegram.WebApp when the page really runs inside Telegram (initData present).
export function webApp() {
  const w = typeof window !== 'undefined' ? window.Telegram?.WebApp : null
  return w && w.initData ? w : null
}

export function isMiniApp() { return !!webApp() }

export function initData() { return webApp()?.initData || '' }

export function startParam() { return webApp()?.initDataUnsafe?.start_param || '' }

export function tgFirstName() { return webApp()?.initDataUnsafe?.user?.first_name || '' }

function versionAtLeast(v) {
  const w = webApp()
  try { return !!w && w.isVersionAtLeast(v) } catch { return false }
}

// loadTelegram loads the bridge once; resolves to Telegram.WebApp or null (not inside Telegram).
export function loadTelegram() {
  if (ready) return ready
  ready = new Promise((resolve) => {
    if (!shouldLoad()) { resolve(null); return }
    if (window.Telegram?.WebApp) { resolve(setup()); return }
    const s = document.createElement('script')
    s.src = SCRIPT
    s.async = true
    s.onload = () => resolve(setup())
    s.onerror = () => resolve(null)
    document.head.appendChild(s)
    setTimeout(() => resolve(webApp() ? setup() : null), 5000)
  })
  return ready
}

let setUp = false

function setup() {
  const w = webApp()
  if (!w) return null
  if (setUp) return w
  setUp = true
  try { sessionStorage.setItem(FLAG, '1') } catch { /* storage off */ }
  document.querySelector('.he')?.classList.add('he--tg')
  try {
    w.ready()
    w.expand()
    if (versionAtLeast('6.1')) { w.setHeaderColor(BG); w.setBackgroundColor(BG) }
    if (versionAtLeast('7.10')) w.setBottomBarColor(BG)
    if (versionAtLeast('7.7')) w.disableVerticalSwipes()
    const phone = w.platform === 'ios' || w.platform === 'android'
    if (phone && versionAtLeast('8.0') && !w.isFullscreen) w.requestFullscreen()
    w.MainButton.setParams({ color: ACCENT, text_color: ACCENT_INK, is_visible: false })
  } catch { /* an old client: the cabinet still works */ }
  document.addEventListener('click', onLinkClick, true)
  return w
}

// ── links (12 §12.4 «Ссылки») ──

function tgToTme(href) {
  // tg://proxy?server=… → https://t.me/proxy?server=…; tg://resolve?domain=x → https://t.me/x
  const u = new URL(href)
  if (u.hostname === 'resolve' && u.searchParams.get('domain')) return `https://t.me/${u.searchParams.get('domain')}`
  return `https://t.me/${u.hostname}${u.search}`
}

function onLinkClick(e) {
  const w = webApp()
  if (!w || e.defaultPrevented || e.button !== 0) return
  const a = e.target.closest?.('a[href]')
  if (!a) return
  const href = a.getAttribute('href') || ''
  let target = null
  try {
    if (href.startsWith('tg:')) target = { tg: tgToTme(href) }
    else if (/^https:\/\/t\.me\//.test(href)) target = { tg: href }
    else if (/^(happ|intent):/i.test(href)) target = { ext: `${location.origin}/connect${location.search.includes('sub=') ? location.search : ''}` }
    else if (href.startsWith('/connect?') && href.includes('sub=')) target = { ext: location.origin + href }
    else if (/^https?:\/\//.test(href) && new URL(href).origin !== location.origin) target = { ext: href }
    else if (a.target === '_blank') target = { ext: new URL(href, location.href).href } // documents open outside the webview
  } catch { target = null }
  if (!target) return
  e.preventDefault()
  e.stopPropagation()
  if (target.tg) w.openTelegramLink(target.tg)
  else w.openLink(target.ext)
}

// openExternal opens a URL in the external browser (must run inside a click handler).
export function openExternal(url) {
  const w = webApp()
  if (w) w.openLink(url)
  else window.open(url, '_blank', 'noopener')
}

// ── haptics ──

export function haptic(kind) {
  const w = webApp()
  try { if (w && versionAtLeast('6.1')) w.HapticFeedback.notificationOccurred(kind) } catch { /* ignore */ }
}

// ── BackButton ──

export function setBackButton(visible, onClick) {
  const w = webApp()
  if (!w || !versionAtLeast('6.1')) return () => {}
  if (!visible) { w.BackButton.hide(); return () => {} }
  w.BackButton.show()
  w.BackButton.onClick(onClick)
  return () => { w.BackButton.offClick(onClick) }
}

// ── MainButton: mirrors the one primary button on the screen ──

const primaries = [] // mounted primary buttons, newest last: { el, get }
let mainHandler = null

function topPrimary() {
  for (let i = primaries.length - 1; i >= 0; i--) {
    const p = primaries[i]
    if (p.el.isConnected && !p.el.closest('[role="dialog"]')) return p
  }
  return null
}

function dialogOpen() { return !!document.querySelector('.he [role="dialog"]') }

export function syncMainButton() {
  const w = webApp()
  if (!w) return
  const mb = w.MainButton
  const p = dialogOpen() ? null : topPrimary()
  if (mainHandler) { mb.offClick(mainHandler); mainHandler = null }
  if (!p) { mb.hideProgress?.(); mb.hide(); return }
  const { text, disabled, loading } = p.get()
  try {
    // hideProgress() re-activates the button in telegram-web-app.js: call it before is_active
    if (!loading) mb.hideProgress?.()
    mb.setParams({ text: (text || 'Продолжить').slice(0, 64), color: ACCENT, text_color: ACCENT_INK, is_active: !disabled && !loading, is_visible: true })
    if (loading) mb.showProgress?.(false)
  } catch { mb.hide(); return }
  mainHandler = () => { if (!p.get().disabled && !p.get().loading) p.el.click() }
  mb.onClick(mainHandler)
}

// registerPrimary is used by <Button variant="primary">: returns the unregister function.
export function registerPrimary(entry) {
  if (!webApp()) return () => {}
  primaries.push(entry)
  queueMicrotask(syncMainButton)
  return () => {
    const i = primaries.indexOf(entry)
    if (i >= 0) primaries.splice(i, 1)
    queueMicrotask(syncMainButton)
  }
}

// ── «Добавить на экран «Домой»» (Bot API 8.0) ──

export function homeScreenStatus() {
  const w = webApp()
  return new Promise((resolve) => {
    if (!w || !versionAtLeast('8.0')) { resolve('unsupported'); return }
    try { w.checkHomeScreenStatus((s) => resolve(s || 'unknown')) } catch { resolve('unsupported') }
  })
}

export function addToHomeScreen() {
  try { webApp()?.addToHomeScreen() } catch { /* ignore */ }
}
