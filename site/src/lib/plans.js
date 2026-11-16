// Plans come only from GET /api/v1/plans (spec 05 §2): names, prices, terms and features are never
// written in the code. These helpers derive what the cards show from the API fields.
import { apiFetch } from '../api/http.js'
import { money, term, traffic } from './format.js'

export function fetchPlans() {
  return apiFetch('/api/v1/plans', { signOutOn401: false, fallback: 'Не удалось загрузить тарифы.' })
    .then((d) => (Array.isArray(d) ? d : []))
}

export function features(p) {
  if (!p || !p.features) return {}
  if (typeof p.features === 'object') return p.features
  try { return JSON.parse(p.features) || {} } catch { return {} }
}

const isFree = (p) => Number(p.price_rub) === 0

// What the card lists (02 §27): the description, unlimited traffic or the limit, extras from features.
export function planList(p) {
  const out = []
  if (p.description) out.push(p.description)
  const lim = Number(p.traffic_limit_bytes) || 0
  out.push(lim ? `Трафик — ${traffic(lim)}` : 'Трафик без лимита')
  const f = features(p)
  if (f.traffic_masking) out.push('Приватная маршрутизация')
  if (f.priority_support) out.push('Приоритетная поддержка')
  if (p.direct_routing_enabled) out.push('Доступ к РФ-сервисам')
  return out
}

const months = (p) => Math.max(1, Math.round(Number(p.duration_days) / 30))
const perMonth = (p) => Number(p.price_rub) / months(p)

// Saving of a long plan against the dearest monthly plan, in whole percent; 0 when not cheaper.
export function savings(plans) {
  const paid = plans.filter((p) => !isFree(p))
  const monthly = paid.filter((p) => Number(p.duration_days) <= 31)
  const base = monthly.length ? Math.max(...monthly.map(perMonth)) : 0
  const out = {}
  for (const p of paid) {
    if (Number(p.duration_days) <= 31 || !base) { out[p.id] = 0; continue }
    const s = Math.round((1 - perMonth(p) / base) * 100)
    out[p.id] = s > 0 ? s : 0
  }
  return out
}

// The recommended plan: the one with the biggest saving (one per grid).
export function featuredId(plans) {
  const s = savings(plans)
  let best = null
  for (const p of plans) {
    if (isFree(p)) continue
    if (s[p.id] > 0 && (best === null || s[p.id] > s[best])) best = p.id
  }
  return best
}

export function splitPlans(list) {
  const sorted = [...list].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
  return { paid: sorted.filter((p) => !isFree(p)), free: sorted.find(isFree) || null, all: sorted }
}

export function minPaidPrice(list) {
  const paid = list.filter((p) => !isFree(p)).map((p) => Number(p.price_rub))
  return paid.length ? Math.min(...paid) : 0
}

export const perMonthLabel = (p) => (Number(p.duration_days) > 31 ? `${money(Math.round(perMonth(p)))} в месяц` : '')
export const termLabel = (p) => term(p.duration_days)

// Catalogue price: digits grouped only from 10 000 (02 §27) — «3000 ₽», «10 000 ₽».
export const priceTag = (rub) => (Number(rub) >= 10000 ? money(rub) : `${Number(rub)} ₽`)
