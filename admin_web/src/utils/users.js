// Users in the admin panel as pure rules (node --test).

// hasPlan: GET /users gives current_plan = 'Нет подписки' when there is none.
export const hasPlan = (u) => !!u?.current_plan && u.current_plan !== 'Нет подписки'

// telegramId: the API reads telegram_id as a number (C2); null = not a number.
export function telegramId(v) {
  const s = String(v ?? '').trim()
  return /^[1-9][0-9]{0,18}$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : null
}
