// The site's public facts and switches (GET /api/v1/site-info, cleanup plan E1/E3/E4): the B2B
// switch, the support contacts, the exit countries, whether an entry for mobile networks works.
// Loaded once per page load and shared by every component.
import { useEffect, useState } from 'react'
import { apiFetch } from '../api/http.js'

const EMPTY = { b2b_enabled: false, support_contact: '', support_email: '', exit_countries: [], mobile_ready: false }

let pending = null
let value = null

export function loadSiteInfo() {
  if (value) return Promise.resolve(value)
  if (!pending) {
    pending = apiFetch('/api/v1/site-info', { signOutOn401: false, fallback: 'Не удалось загрузить сведения о сервисе.' })
      .then((v) => { value = { ...EMPTY, ...v }; return value })
      .catch(() => { pending = null; return null })
  }
  return pending
}

// useSiteInfo → { info, loaded }: info is EMPTY until loaded (and when the API is down), so a
// page never shows a fact it could not check.
export default function useSiteInfo() {
  const [info, setInfo] = useState(value)
  useEffect(() => {
    if (value) return
    let alive = true
    loadSiteInfo().then((v) => { if (alive) setInfo(v || EMPTY) })
    return () => { alive = false }
  }, [])
  return { info: info || EMPTY, loaded: !!info }
}

const COUNTRY_RU = {
  NL: 'Нидерланды', DE: 'Германия', FI: 'Финляндия', LV: 'Латвия', EE: 'Эстония', LT: 'Литва', PL: 'Польша',
  SE: 'Швеция', FR: 'Франция', GB: 'Великобритания', US: 'США', TR: 'Турция', KZ: 'Казахстан', AM: 'Армения',
  GE: 'Грузия', AE: 'ОАЭ', CH: 'Швейцария', AT: 'Австрия', CZ: 'Чехия', RS: 'Сербия', ES: 'Испания', IT: 'Италия',
  RO: 'Румыния', BG: 'Болгария', MD: 'Молдова', HK: 'Гонконг', SG: 'Сингапур', JP: 'Япония', CA: 'Канада',
}

export function countryName(code) {
  return COUNTRY_RU[String(code || '').toUpperCase()] || String(code || '').toUpperCase()
}

export function countryFlag(code) {
  const c = String(code || '').toUpperCase()
  if (!/^[A-Z]{2}$/.test(c)) return ''
  return String.fromCodePoint(...[...c].map((ch) => 0x1f1a5 + ch.charCodeAt(0)))
}
