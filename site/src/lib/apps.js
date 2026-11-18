// Happ and the other apps that take a subscription link. /api/v1/user/downloads answers an empty map
// today, so these are the store pages. Google Play dropped Happ (404 on 2026-11-18, only clones
// left): Android and desktop builds come from the official site happ.info. The bot's list is
// happStores in server/backend/cmd/api/tgbot_menu.go — keep the two in sync.
export const HAPP = {
  ios: { label: 'iPhone и iPad', store: 'App Store', url: 'https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108', icon: 'phone' },
  android: { label: 'Android', store: 'APK с сайта Happ', url: 'https://happ.info/ru/', icon: 'android' },
  desktop: { label: 'Компьютер', store: 'Windows, macOS, Linux', url: 'https://happ.info/ru/', icon: 'monitor' },
}

export const OTHER_APPS = [
  { name: 'v2rayNG', platforms: 'Android', url: 'https://play.google.com/store/apps/details?id=com.v2ray.ang' },
  { name: 'Streisand', platforms: 'iPhone, Mac', url: 'https://apps.apple.com/app/streisand/id6450534064' },
]

// the device the page is opened on, for the default tab of the guide
export function detectDevice() {
  const ua = typeof navigator !== 'undefined' ? navigator.userAgent : ''
  if (/iPhone|iPad|iPod/i.test(ua)) return 'ios'
  if (/Android/i.test(ua)) return 'android'
  return 'desktop'
}

// supportHref turns the support_contact setting into a link: a Telegram @handle, a t.me / https
// link, an e-mail; anything else is shown as text.
export function supportHref(contact) {
  const c = String(contact || '').trim()
  if (!c) return ''
  if (/^@[A-Za-z0-9_]{4,}$/.test(c)) return `https://t.me/${c.slice(1)}`
  if (/^https:\/\//i.test(c)) return c
  if (/^t\.me\//i.test(c)) return `https://${c}`
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(c)) return `mailto:${c}`
  return ''
}
