// promoState: what the promo-code list shows in «Статус» — an expired or used-up code is not
// «активен» even while its switch is on (QA 2026-11-18).
export function promoState(item, now = new Date()) {
  if (!item?.is_active) return 'off'
  const exp = item.expires_at ? new Date(item.expires_at) : null
  if (exp && !Number.isNaN(exp.getTime()) && exp <= now) return 'expired'
  if (Number(item.max_uses) > 0 && Number(item.current_uses) >= Number(item.max_uses)) return 'used_up'
  return 'active'
}
