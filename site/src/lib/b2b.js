// B2B base prices — the same formula the signup endpoint records (POST /api/public/create-tenant).
// There is no price endpoint for companies yet; the landing says «Указаны базовые значения».
export const B2B = { perUser: 250, ourServers: 1000, includedServers: 5, perExtraServer: 100 }

export function estimate({ users, useOur, own }) {
  let total = Math.max(1, users | 0) * B2B.perUser
  if (useOur) total += B2B.ourServers
  total += Math.max(0, (own | 0) - B2B.includedServers) * B2B.perExtraServer
  return total
}

// roles of /api/v1/business/* (05 §6)
export const ROLES = {
  admin: { text: 'Админ', tone: 'info' },
  billing: { text: 'Биллинг', tone: 'warn' },
  user: { text: 'Сотрудник', tone: undefined },
  viewer: { text: 'Только чтение', tone: undefined },
}
export const ROLE_OPTIONS = ['admin', 'billing', 'user', 'viewer'].map((v) => ({ value: v, label: ROLES[v].text }))

export const initials = (name) => String(name || '?').replace(/[_-]+/g, ' ').trim().split(/\s+/).slice(0, 2).map((w) => w[0]?.toUpperCase() || '').join('') || '?'
