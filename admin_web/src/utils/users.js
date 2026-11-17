// Users in the admin panel as pure rules (node --test).

// hasPlan: GET /users gives current_plan = 'Нет подписки' when there is none.
export const hasPlan = (u) => !!u?.current_plan && u.current_plan !== 'Нет подписки'

// passwordState: the «Пароль» row of the user card and whether «Сбросить пароль» makes sense —
// only an account with a password has one to reset (without one it signs in by HiEarth ID).
export function passwordState(u) {
  if (!u?.has_password) return { label: 'не задан', variant: 'default', canReset: false }
  if (u.password_must_change) return { label: 'временный — ждёт смены', variant: 'warning', canReset: true }
  return { label: 'установлен', variant: 'success', canReset: true }
}

// resetConsequences: what the confirmation dialog says before «Сбросить».
export function resetConsequences(u) {
  return [
    'Пароль заменится временным — он появится здесь один раз, передайте его человеку.',
    'Все сессии пользователя закроются: сайт, кабинет в Telegram, приложение. Подписка и подключение в Happ продолжат работать.',
    'При входе кабинет попросит придумать свой пароль, пока не сменит — на каждой странице.',
    u?.has_confirmed_email
      ? 'Временный пароль уйдёт и письмом на подтверждённую почту.'
      : 'Подтверждённой почты нет — письма не будет, передайте пароль сами (например, в ответе поддержки).',
  ]
}

// telegramId: the API reads telegram_id as a number (C2); null = not a number.
export function telegramId(v) {
  const s = String(v ?? '').trim()
  return /^[1-9][0-9]{0,18}$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : null
}
