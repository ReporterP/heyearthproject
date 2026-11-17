// Dev-only mock of the HiEarth API (vite --mode mock). Never part of the production bundle: it lives
// outside src/ and vite.config.js loads it only in mock mode.
//
// Shapes follow the Go handlers (server/backend/cmd/api/*): money *_kop in kopecks, price_rub /
// amount_rub in rubles, /user/payments amount in rubles (float), RFC3339 times, errors {"error": "…"}.
//
// Switch the scenario at http://localhost:5175/__mock (links), or /__mock/set?sub=expired&wallet=zero…
//   sub      active | none | expired          — subscription state
//   wallet   enough | low | zero              — balance against the renewal price
//   trial    available | used                 — free week still available
//   renew    on | off                         — auto-renewal
//   history  full | empty                     — payments & wallet history
//   tg       both | web | classic | none      — Telegram proxy answer
//   bot      linked | unlinked | blocked | off — the HiEarth Telegram bot (settings «Telegram»)
//   support  answered | waiting | closed | empty — the support conversation
//   news     unread | read | empty             — the cabinet's «Новости»
//   stars    on | off                          — paying in Telegram Stars (Mini App)
//   email    confirmed | pending | none
//   login    password | nopassword            — has a login+password
//   referral on | off
//   b2b      off | admin | billing | viewer   — site-info.b2b_enabled + role in /business/me
//   tenant   active | inactive | suspended
//   members  many | solo
//   pay      succeeded | pending | canceled   — what a top-up turns into
//   sublink  ok | fail                        — /user/subscribe-token fails (link unavailable)
//   fail     comma list of endpoint keys that answer 503 (plans,profile,subscription,wallet,traffic,
//            payments,payment,connections,referral,quote,topup,purchase,register,login,email,mtproto,business)
//   delay    milliseconds added to every answer (see skeletons)

const DEFAULT = {
  sub: 'active', wallet: 'low', trial: 'used', renew: 'on', history: 'full', tg: 'both', email: 'confirmed',
  login: 'password', referral: 'on', b2b: 'off', tenant: 'active', members: 'many', pay: 'succeeded',
  sublink: 'ok', fail: '', delay: '350', bot: 'unlinked', support: 'answered', news: 'unread', stars: 'on',
}
const S = { ...DEFAULT }
const OPTIONS = {
  sub: ['active', 'none', 'expired'], wallet: ['enough', 'low', 'zero'], trial: ['available', 'used'], renew: ['on', 'off'],
  history: ['full', 'empty'], tg: ['both', 'web', 'classic', 'none'], email: ['confirmed', 'pending', 'none'],
  login: ['password', 'nopassword'], referral: ['on', 'off'], b2b: ['off', 'admin', 'billing', 'viewer'],
  tenant: ['active', 'inactive', 'suspended'], members: ['many', 'solo'], pay: ['succeeded', 'pending', 'canceled'],
  sublink: ['ok', 'fail'], delay: ['0', '350', '1500'], bot: ['linked', 'unlinked', 'blocked', 'off'],
  support: ['answered', 'waiting', 'closed', 'empty'],
  news: ['unread', 'read', 'empty'],
  stars: ['on', 'off'],
}

const USER_ID = '3f2b8c1e-7a4d-4e9b-8c21-5d6e7f8a9b0c'
const DAY = 86400000
const iso = (ms) => new Date(ms).toISOString()
const now = () => Date.now()

const PLANS = [
  { id: 1, slug: 'free-trial', name: 'Пробная неделя', description: 'Все способы подключения на 7 дней', price_rub: 0, duration_days: 7, traffic_limit_bytes: 10737418240, features: '{}', sort_order: 0, direct_routing_enabled: false },
  { id: 2, slug: 'start', name: 'Старт', description: 'Основные способы подключения', price_rub: 250, duration_days: 30, traffic_limit_bytes: null, features: '{}', sort_order: 1, direct_routing_enabled: false },
  { id: 3, slug: 'full', name: 'Полный', description: 'Все способы подключения', price_rub: 300, duration_days: 30, traffic_limit_bytes: null, features: '{}', sort_order: 2, direct_routing_enabled: true },
  { id: 4, slug: 'quarter', name: 'Квартал', description: 'Всё из «Полного» на три месяца', price_rub: 720, duration_days: 90, traffic_limit_bytes: null, features: '{"traffic_masking": true, "priority_support": true}', sort_order: 3, direct_routing_enabled: true },
  { id: 5, slug: 'year', name: 'Год', description: 'Всё включено на год', price_rub: 3000, duration_days: 365, traffic_limit_bytes: null, features: '{"priority_support": true}', sort_order: 4, direct_routing_enabled: true },
].map((p) => ({ ...p, allowed_protocols: ['all'], is_active: true, auto_renew_default: false, created_at: '2026-05-01T10:00:00Z', updated_at: '2026-05-01T10:00:00Z' }))

const CUR = PLANS[3] // the user's plan: «Квартал»
const visiblePlans = () => PLANS

function balanceKop() {
  if (S.wallet === 'enough') return 150000
  if (S.wallet === 'zero') return 0
  return 43000
}

function sub() {
  if (S.sub === 'none') return null
  const active = S.sub === 'active'
  const until = active ? now() + 90 * DAY : now() - 4 * DAY
  return { id: 42, plan_id: CUR.id, plan_name: CUR.name, valid_until: iso(until), auto_renew: S.renew === 'on', active, plan: CUR.slug, price_rub: CUR.price_rub, started_at: iso(until - CUR.duration_days * DAY) }
}

function profile() {
  const s = sub()
  const p = { user_id: USER_ID, username: S.login === 'password' ? 'ivan_moscow' : '', has_password: S.login === 'password', preferred_platform: 'web', locale: 'ru', is_blocked: false, is_premium: !!s?.active, access_status: 'active' }
  if (s?.active) Object.assign(p, { current_plan: s.plan_name, current_plan_slug: s.plan, valid_until: s.valid_until })
  return p
}

function autoRenew() {
  const s = sub()
  if (!s || !s.active) return null
  const bal = balanceKop()
  const price = CUR.price_rub * 100
  return { enabled: S.renew === 'on', plan_name: CUR.name, price_kop: price, renew_at: s.valid_until, enough: bal >= price, shortfall_kop: Math.max(0, price - bal), free: false }
}

function transactions() {
  if (S.history === 'empty') return []
  const t = now()
  return [
    { id: 9, amount_kop: 50000, kind: 'topup', title: 'Пополнение', comment: '', balance_after_kop: 77000, payment_id: 503, created_at: iso(t - 2 * DAY) },
    { id: 8, amount_kop: -72000, kind: 'purchase', title: 'Оплата подписки «Квартал»', comment: '', balance_after_kop: 27000, subscription_id: 42, created_at: iso(t - 2 * DAY + 60000) },
    { id: 7, amount_kop: 10000, kind: 'referral_bonus', title: 'Бонус за друга', comment: '', balance_after_kop: 99000, created_at: iso(t - 9 * DAY) },
    { id: 6, amount_kop: -30000, kind: 'auto_renew', title: 'Оплата подписки (автопродление)', comment: '', balance_after_kop: 89000, created_at: iso(t - 40 * DAY) },
    { id: 5, amount_kop: 100000, kind: 'topup', title: 'Пополнение', comment: '', balance_after_kop: 119000, payment_id: 501, created_at: iso(t - 41 * DAY) },
    { id: 4, amount_kop: 30000, kind: 'refund', title: 'Возврат на карту', comment: '', balance_after_kop: 19000, payment_id: 500, created_at: iso(t - 120 * DAY) },
  ]
}

function payments() {
  if (S.history === 'empty') return []
  const t = now()
  return [
    { id: 504, amount: 218, currency: 'RUB', status: 'pending', provider: 'yookassa', external_id: '2f6c90aa-000f-5000-9000-1c5e3d7aa91b', notes: 'HiEarth — пополнение кошелька', created_at: iso(t - 3600000) },
    { id: 503, amount: 500, currency: 'RUB', status: 'succeeded', provider: 'yookassa', external_id: '2f6c90ab-000f-5000-9000-1c5e3d7aa91c', notes: 'HiEarth — пополнение кошелька', created_at: iso(t - 2 * DAY), confirmed_at: iso(t - 2 * DAY + 70000) },
    { id: 502, amount: 300, currency: 'RUB', status: 'canceled', provider: 'yookassa', external_id: '2f6c90ac-000f-5000-9000-1c5e3d7aa91d', notes: 'HiEarth — пополнение кошелька', created_at: iso(t - 20 * DAY) },
    { id: 501, amount: 1000, currency: 'RUB', status: 'succeeded', provider: 'yookassa', external_id: '2f6c90ad-000f-5000-9000-1c5e3d7aa91e', notes: 'HiEarth — пополнение кошелька', created_at: iso(t - 41 * DAY), confirmed_at: iso(t - 41 * DAY + 60000) },
    { id: 500, amount: 300, currency: 'RUB', status: 'refunded', provider: 'yookassa', external_id: '2f6c90ae-000f-5000-9000-1c5e3d7aa91f', notes: 'HiEarth — пополнение кошелька', created_at: iso(t - 125 * DAY), confirmed_at: iso(t - 125 * DAY + 60000) },
  ]
}

function paymentDetail(id) {
  const p = payments().find((x) => x.id === id) || (id === 777 ? { id: 777, amount: 218, status: S.pay, created_at: iso(now() - 120000), external_id: '2f6c90ff-000f-5000-9000-1c5e3d7aa999' } : null)
  if (!p) return null
  const paid = p.status === 'succeeded' || p.status === 'refunded'
  return {
    id: p.id, amount: p.amount, currency: 'RUB', status: p.status, payment_method_type: 'bank_card',
    refunded_amount: p.status === 'refunded' ? p.amount : 0, receipt_registered: paid && p.id !== 503,
    yookassa_payment_id: p.external_id, description: 'HiEarth — пополнение кошелька', payer_email: S.email === 'confirmed' ? 'ivan@example.ru' : '',
    created_at: p.created_at, test: true, ...(paid ? { paid_at: p.confirmed_at, receipt_id: 'rcpt_1', payment_method_details: { type: 'bank_card', card: { last4: '4242', card_type: 'MasterCard' } } } : {}),
    ...(p.status === 'canceled' ? { cancellation_reason: 'expired_on_confirmation' } : {}),
  }
}

function traffic() {
  const t = now()
  const byDay = (n) => Array.from({ length: n }, (_, i) => ({ day: iso(t - (n - 1 - i) * DAY).slice(0, 10), bytes: Math.round((0.3 + Math.abs(Math.sin(i * 1.7)) * 2.6) * 1024 ** 3) }))
  const month = byDay(30)
  const sum = (a) => a.reduce((x, y) => x + y.bytes, 0)
  const year = Array.from({ length: 12 }, (_, i) => { const d = new Date(t); d.setMonth(d.getMonth() - (11 - i)); return { month: d.toISOString().slice(0, 7), bytes: Math.round((8 + Math.abs(Math.cos(i)) * 30) * 1024 ** 3) } })
  const s = sub()
  return {
    ...(s?.active ? { subscription: { started_at: s.started_at, valid_until: s.valid_until, plan_name: s.plan_name, is_unlimited: true } } : {}),
    cycle: { started_at: iso(t - 10 * DAY), ends_at: iso(t + 20 * DAY), used_bytes: sum(month), limit_bytes: 0, percent: 0, exceeded: false },
    periods: { day: { used_bytes: month[29].bytes, by_day: month.slice(-1) }, week: { used_bytes: sum(month.slice(-7)), by_day: month.slice(-7) }, month: { used_bytes: sum(month), by_day: month }, year: { used_bytes: sum(year), by_month: year } },
  }
}

function mtproto() {
  const s = sub()
  if (!s?.active) return { enabled: false, reason: 'Доступно для активных подписчиков' }
  if (S.tg === 'none') return { enabled: false, reason: 'Временно недоступно' }
  const web = { link: 'https://t.me/webproxy?server=tg.example.ru&secret=ee00mock', tg_link: 'tg://webproxy?server=tg.example.ru&secret=ee00mock', server: 'tg.example.ru', secret: 'ee00mock0000000000000000000000000' }
  const classic = { link: 'https://t.me/proxy?server=203.0.113.10&port=443&secret=dd00mock', tg_link: 'tg://proxy?server=203.0.113.10&port=443&secret=dd00mock', server: '203.0.113.10', port: '443', secret: 'dd00mock' }
  const out = { enabled: true }
  if (S.tg === 'both' || S.tg === 'web') out.web = web
  if (S.tg === 'both' || S.tg === 'classic') Object.assign(out, { classic, host: classic.server, port: classic.port, secret: classic.secret, tg_deeplink: classic.tg_link, t_me_link: classic.link })
  return out
}

function quote(planId, promo) {
  const p = PLANS.find((x) => x.id === Number(planId))
  if (!p || p.price_rub === 0) return [400, { error: 'Этот тариф сейчас недоступен — выберите другой' }]
  let price = p.price_rub * 100
  const out = {}
  if (promo) {
    if (promo.toUpperCase() !== 'ЛЕТО10') return [400, { error: 'Промокод не найден' }]
    const off = Math.round(price * 0.1)
    price -= off
    Object.assign(out, { promo_type: 'percent', promo_message: `Скидка 10% — минус ${off / 100} ₽` })
  }
  const bal = balanceKop()
  const short = Math.max(0, price - bal)
  const s = sub()
  const bonus = s?.active && p.id !== CUR.id ? 18 : 0
  return [200, { plan_id: p.id, plan_name: p.name, duration_days: p.duration_days, list_price_kop: p.price_rub * 100, price_kop: price, balance_kop: bal, shortfall_kop: short, topup_kop: short, new_valid_until: iso(now() + (p.duration_days + bonus) * DAY), bonus_days: bonus, is_downgrade_blocked: !!(s?.active && p.price_rub < CUR.price_rub && p.duration_days <= 31), free: false, ...out }]
}

function business() {
  const role = S.b2b
  const members = [
    { user_db_id: 7, user_id: USER_ID, username: 'ivan_moscow', email: 'anna@romashka.ru', role: role === 'admin' ? 'admin' : role, joined_at: iso(now() - 14 * DAY) },
    ...(S.members === 'many' ? [
      { user_db_id: 8, user_id: 'a1', username: 'igor_p', role: 'billing', joined_at: iso(now() - 13 * DAY) },
      { user_db_id: 9, user_id: 'a2', username: 'maria_s', role: 'user', joined_at: iso(now() - 2 * DAY) },
      { user_db_id: 10, user_id: 'a3', username: 'dmitry_l', role: 'viewer', joined_at: iso(now() - 1 * DAY) },
    ] : []),
  ]
  return {
    tenant: { id: 42, name: 'ООО «Ромашка»', slug: 'romashka', status: S.tenant, plan: 'basic', contact_email: 'buh@romashka.ru', balance: S.tenant === 'active' ? 12500 : 0, inn: '7707083893', max_users: 10, max_servers: 5, use_provider_servers: true, created_at: iso(now() - 15 * DAY) },
    role,
    members,
    transactions: S.history === 'empty' ? [] : [
      { id: 2, amount: 3500, direction: 'debit', description: 'Ежемесячное списание: 10 мест', created_at: iso(now() - 1 * DAY) },
      { id: 1, amount: 10000, direction: 'credit', description: '', payment_id: 600, created_at: iso(now() - 2 * DAY) },
    ],
    invoices: S.history === 'empty' ? null : [
      { id: 1042, payment_id: 600, inn: '7707083893', legal_name: 'ООО «Ромашка»', amount: 10000, currency: 'RUB', status: 'paid', description: 'HiEarth B2B', payer_email: 'buh@romashka.ru', created_at: iso(now() - 2 * DAY), paid_at: iso(now() - 2 * DAY) },
      { id: 1051, inn: '7707083893', legal_name: 'ООО «Ромашка»', amount: 3500, currency: 'RUB', status: 'pending', description: 'HiEarth B2B', payer_email: 'buh@romashka.ru', created_at: iso(now()) },
    ],
  }
}

// ── routing ──────────────────────────────────────────────────────────────────
const failing = (k) => S.fail.split(',').map((x) => x.trim()).filter(Boolean).includes(k)
const UNAVAILABLE = [503, { error: 'Сервис временно недоступен, попробуйте через минуту' }]

function route(method, path, q, body, authed) {
  const need = () => (authed ? null : [401, { error: 'Сессия истекла — войдите снова' }])
  let m

  // public
  if (path === '/api/v1/plans') return failing('plans') ? UNAVAILABLE : [200, visiblePlans()]
  if (path === '/api/v1/site-info') return [200, { b2b_enabled: S.b2b !== 'off', support_contact: '@hiearth_support', support_email: 'support@example.ru', exit_countries: ['NL'], mobile_ready: true }]
  if (path === '/api/v1/legal-info') return [200, { legal_company_name: 'ИП Пример П.П.', legal_address: '', legal_email: 'legal@example.ru', legal_phone: '', legal_inn: '000000000000' }]
  if (path === '/api/v1/referral/terms') return [200, S.referral === 'on' ? { enabled: true, referrer_kop: 10000, friend_kop: 10000, min_purchase_kop: 25000 } : { enabled: false, referrer_kop: 10000, friend_kop: 10000, min_purchase_kop: 25000 }]
  if (path === '/api/v1/user/downloads') return [200, { downloads: {} }]
  if ((m = path.match(/^\/api\/v1\/user\/happ-link\/(.*)$/))) {
    if (!m[1]) return [400, { error: 'missing token' }]
    if (m[1] === 'bad') return [404, { error: 'invalid token' }]
    const url = `https://go.example.ru/api/v1/user/subscribe/${m[1]}`
    return [200, { happ_link: `happ://add/${url}`, subscribe_url: url }]
  }
  if (path === '/api/v1/email/confirm') return q.has('bad') || body?.token === 'bad' ? [404, { error: 'Ссылка устарела или уже использована — укажите почту ещё раз' }] : [200, { email: 'ivan@example.ru' }]
  if (path === '/api/v1/email/unsubscribe') return q.get('s') === 'bad' ? [404, { error: 'Ссылка неверна — отключите напоминания в личном кабинете' }] : [200, { message: 'Напоминания отключены' }]
  if (path === '/api/public/create-tenant') return S.b2b === 'off' ? [404, null] : [201, { tenant_id: 43, message: 'Tenant created', claim_url: 'http://localhost:5175/claim/tn-r-admin-7qx4kp2m' }]
  if (path === '/api/v1/business/create-invoice') {
    if (S.b2b === 'off') return [404, null]
    if (!/^\d{10}$|^\d{12}$/.test(String(body?.inn || ''))) return [400, { error: 'ИНН должен содержать 10 (юрлицо) или 12 (физлицо/ИП) цифр' }]
    return [200, { payment_id: 602, confirmation_url: 'https://yoomoney.ru/checkout/payments/v2/contract?orderId=mock', amount: body.amount, email: body.email, company_name: body.company_name }]
  }

  // auth
  if (path === '/api/v1/auth/register') return failing('register') ? UNAVAILABLE : [200, { user_id: USER_ID, token: 'mock-token', profile: { ...profile(), username: '', has_password: false } }]
  if (path === '/api/v1/auth/login') {
    if (failing('login')) return UNAVAILABLE
    const id = String(body?.id_or_username || '').trim()
    if (id === 'rate') return [429, { error: 'too many requests' }]
    if (id !== USER_ID && id !== 'ivan_moscow') return [401, { error: 'user not found' }]
    if (S.login === 'password' && !body?.password) return [401, { error: 'password required' }]
    if (S.login === 'password' && body?.password !== '1234') return [401, { error: 'invalid password' }]
    return [200, { user_id: USER_ID, token: 'mock-token', profile: profile() }]
  }
  if (path === '/api/v1/auth/logout') return [204, null]

  // user
  if (path.startsWith('/api/v1/user/') || path.startsWith('/api/v1/wallet') || path.startsWith('/api/v1/payment/') || path.startsWith('/api/v1/referral') || path.startsWith('/api/v1/business/')) {
    const a = need(); if (a) return a
  }
  if (path === '/api/v1/user/profile') {
    if (failing('profile')) return UNAVAILABLE
    if (method === 'PATCH') {
      if (body?.username && body.username.length < 5) return [400, { error: 'имя пользователя должно быть от 5 до 64 символов' }]
      if (body?.username === 'taken') return [400, { error: 'username already taken' }]
      if (body?.new_password && S.login === 'password' && body.current_password !== '1234') return [400, { error: 'invalid current password' }]
      if (body?.username) S.login = 'password'
      return [200, { ...profile(), username: body?.username || profile().username, has_password: true }]
    }
    return [200, profile()]
  }
  if (path === '/api/v1/user/subscription') return failing('subscription') ? UNAVAILABLE : [200, { items: sub() ? [sub()] : null }]
  if (path === '/api/v1/user/traffic') return failing('traffic') ? UNAVAILABLE : [200, traffic()]
  if (path === '/api/v1/user/subscribe-token') {
    if (S.sublink === 'fail' || failing('sublink')) return UNAVAILABLE
    const url = 'https://go.example.ru/api/v1/user/subscribe/a1b2c3d4-0000-4000-8000-000000000001'
    return [200, { subscribe_token: 'a1b2c3d4-0000-4000-8000-000000000001', subscribe_url: url, clash_url: `${url}?format=clash`, happ_link: `happ://add/${url}` }]
  }
  if (path === '/api/v1/user/connections') {
    if (failing('connections')) return UNAVAILABLE
    const s = sub()
    const base = { user_id: USER_ID, can_connect: !!s?.active, is_premium: !!s?.active, traffic_masking_available: true, config_version: '7', subscription_version: 128 }
    return [200, s?.active ? { ...base, plan: { id: CUR.id, slug: CUR.slug, name: CUR.name, allowed_protocols: ['all'], server_ids: [1, 2], traffic_limit: 0, duration_days: 90 } } : base]
  }
  if (path === '/api/v1/user/mtproto') return failing('mtproto') ? UNAVAILABLE : [200, mtproto()]
  if (path === '/api/v1/user/payments') return failing('payments') ? UNAVAILABLE : [200, { items: S.history === 'empty' ? null : payments() }]
  if ((m = path.match(/^\/api\/v1\/user\/payments\/(.+)$/))) {
    if (failing('payment')) return UNAVAILABLE
    const id = Number(m[1])
    if (!(id > 0)) return [400, { error: 'invalid payment id' }]
    const d = paymentDetail(id)
    return d ? [200, d] : [404, { error: 'payment not found' }]
  }
  if (path === '/api/v1/user/auto-renew') {
    if (failing('autorenew')) return UNAVAILABLE
    if (!sub()?.active) return [404, { error: 'нет активной подписки' }]
    S.renew = body?.auto_renew ? 'on' : 'off'
    return [200, { message: body?.auto_renew ? 'Автопродление включено' : 'Автопродление выключено', auto_renew: String(!!body?.auto_renew) }]
  }
  if (path === '/api/v1/user/email') {
    if (failing('email')) return UNAVAILABLE
    if (method === 'POST') {
      const e = String(body?.email || '').toLowerCase()
      if (!e) return [400, { error: 'Укажите адрес почты' }]
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(e)) return [400, { error: 'Похоже, в адресе ошибка — проверьте его' }]
      S.email = 'pending'
      return [200, { pending_email: e, message: `Отправили письмо на ${e} — откройте ссылку из него` }]
    }
    if (method === 'DELETE') { S.email = 'none'; return [200, { message: 'Адрес удалён' }] }
    return [200, { email: S.email === 'confirmed' ? 'ivan@example.ru' : '', pending_email: S.email === 'pending' ? 'new@example.ru' : '', reminders_off: false }]
  }
  if (path === '/api/v1/user/email/reminders') return [200, { reminders_off: !body?.enabled }]

  // wallet
  if (path === '/api/v1/wallet') return failing('wallet') ? UNAVAILABLE : [200, { balance_kop: balanceKop(), min_topup_rub: 100, transactions: transactions(), auto_renew: autoRenew() }]
  if (path === '/api/v1/wallet/quote') return failing('quote') ? UNAVAILABLE : quote(q.get('plan_id'), q.get('promocode'))
  if (path === '/api/v1/wallet/purchase') {
    if (failing('purchase')) return UNAVAILABLE
    const p = PLANS.find((x) => x.id === Number(body?.plan_id))
    if (!p) return [400, { error: 'Этот тариф сейчас недоступен — выберите другой' }]
    if (p.price_rub === 0) return [400, { error: 'Этот тариф бесплатный — оформите его без оплаты' }]
    if (balanceKop() < p.price_rub * 100) return [402, { error: 'На кошельке не хватает денег — пополните его' }]
    S.sub = 'active'
    return [200, { status: 'activated', plan_name: p.name, price_kop: p.price_rub * 100, balance_kop: balanceKop() - p.price_rub * 100, valid_until: iso(now() + p.duration_days * DAY) }]
  }
  if (path === '/api/v1/wallet/topup') {
    if (failing('topup')) return [502, { error: 'ЮKassa не ответила, попробуйте ещё раз через минуту' }]
    const amt = Number(body?.amount_rub)
    if (!(amt >= 100 && amt <= 100000)) return [400, { error: 'Сумма пополнения — от 100 до 100000 ₽' }]
    return [200, { status: 'payment_required', payment_id: 777, amount_kop: amt * 100, confirmation_url: body?.confirmation_type === 'redirect' ? 'https://yoomoney.ru/checkout/mock' : '', confirmation_token: body?.confirmation_type === 'redirect' ? '' : 'ct-mock', test: true }]
  }
  if ((m = path.match(/^\/api\/v1\/wallet\/topup\/(\d+)\/resume$/))) return S.pay === 'pending' || q.has('ok') ? [200, { confirmation_token: 'ct-mock' }] : [409, { error: 'Этот платёж уже нельзя продолжить — начните оплату заново' }]
  if ((m = path.match(/^\/api\/v1\/wallet\/topup\/(\d+)$/))) {
    const paid = S.pay === 'succeeded'
    return [200, { status: S.pay, paid, amount_rub: '218.00', balance_kop: paid ? 0 : balanceKop(), purchase: { status: paid ? 'done' : S.pay === 'canceled' ? 'failed' : 'pending', reason: null, valid_until: paid ? iso(now() + 90 * DAY) : null, plan_name: CUR.name } }]
  }

  // payment
  if (path === '/api/v1/payment/apply-promocode' || path === '/api/v1/payment/redeem-code') {
    const code = String(body?.code || '').trim()
    if (!code) return [400, { error: 'Введите промокод' }]
    if (path.endsWith('redeem-code')) return code.startsWith('tn-') ? [200, { message: 'code redeemed', tenant_id: 42 }] : [400, { error: 'invalid or expired code' }]
    if (code.toUpperCase() === 'НЕДЕЛЯ') { S.sub = 'active'; return [200, { message: 'Промокод применён: +7 дней' }] }
    return [400, { error: 'Промокод не найден' }]
  }
  if (path === '/api/v1/payment/proration-preview') {
    const p = PLANS.find((x) => x.id === Number(q.get('plan_id')))
    if (!p) return [404, { error: 'plan not found' }]
    const s = sub()
    const bonus = s?.active && p.id !== CUR.id ? 18 : 0
    return [200, { original_price: p.price_rub, proration_credit: 0, final_price: p.price_rub, days_remaining: s?.active ? 90 : 0, bonus_days: bonus, new_valid_until: '', is_downgrade_blocked: !!(s?.active && p.price_rub < CUR.price_rub && p.duration_days <= 31), early_renewal_days_threshold: 3 }]
  }
  if (path === '/api/v1/payment/refund') return body?.payment_id ? [200, { message: 'Заявка на возврат отправлена. Мы рассмотрим её в ближайшее время.' }] : [400, { error: 'payment_id required' }]
  if (path === '/api/v1/payment/create') {
    const p = PLANS.find((x) => x.id === Number(body?.plan_id) || x.slug === body?.plan)
    if (!p) return [400, { error: 'план не найден' }]
    if (p.price_rub === 0) { S.sub = 'active'; S.trial = 'used'; return [200, { status: 'activated', plan: p.name, message: `Подписка '${p.name}' активирована на ${p.duration_days} дней` }] }
    return [200, { status: 'payment_required', payment_id: 777, amount_kop: p.price_rub * 100, confirmation_url: '', confirmation_token: 'ct-mock', test: true, original_price: p.price_rub, final_price: p.price_rub }]
  }

  // referral
  if (path === '/api/v1/referral') {
    if (failing('referral')) return UNAVAILABLE
    if (S.referral === 'off') return [200, { enabled: false, referrer_kop: 10000, friend_kop: 10000, min_purchase_kop: 25000 }]
    return [200, { enabled: true, referrer_kop: 10000, friend_kop: 10000, min_purchase_kop: 25000, code: 'K7M2QXH9', link: 'http://localhost:5175/r/K7M2QXH9', stats: { invited: 3, paid: 1, earned_kop: 10000 }, bound: false, can_bind: S.history === 'empty' }]
  }
  if (path === '/api/v1/referral/bind') return String(body?.code || '').toUpperCase() === 'FRIEND01' ? [200, { message: 'Код друга принят: бонус придёт после первой оплаты подписки' }] : [400, { error: 'Код друга не найден — проверьте его' }]

  // business
  if (path.startsWith('/api/v1/business/')) {
    if (failing('business')) return UNAVAILABLE
    if (S.b2b === 'off') return [404, { error: 'no B2B membership' }]
    if (path === '/api/v1/business/me') return [200, business()]
    if (path === '/api/v1/business/topup-init') return S.b2b === 'viewer' ? [403, { error: 'your role does not allow billing actions' }] : [200, { payment_id: 601, confirmation_url: 'https://yoomoney.ru/checkout/mock', tenant_id: 42, amount: body?.amount }]
    if (path === '/api/v1/business/invite') return S.b2b !== 'admin' ? [403, { error: 'admin role required' }] : [200, { code: 'tn-r-user-abcdefghijkl', claim_url: 'http://localhost:5175/claim/tn-r-user-abcdefghijkl', role: body?.role || 'user', expires_at: iso(now() + 7 * DAY) }]
    if ((m = path.match(/^\/api\/v1\/business\/members\/(\d+)$/))) {
      if (S.b2b !== 'admin') return [403, { error: 'admin role required' }]
      return method === 'DELETE' ? [200, { ok: true, removed_user_id: Number(m[1]) }] : [200, { ok: true, user_db_id: Number(m[1]), role: body?.role }]
    }
  }
  // Telegram bot (spec 08 §8.4, 12)
  if (path === '/api/v1/user/telegram') {
    if (method === 'DELETE') { S.bot = 'unlinked'; return [200, { linked: false }] }
    const linked = S.bot === 'linked' || S.bot === 'blocked'
    return [200, { linked, username: linked ? 'ivan_moscow' : '', linked_at: linked ? iso(now() - 3 * DAY) : '0001-01-01T00:00:00Z',
      notify_news: S.botNews !== 'off', notify_reminders: S.botRem !== 'off', bot_blocked: S.bot === 'blocked',
      bot_enabled: S.bot !== 'off', bot_username: 'hiearth_bot', bot_url: 'https://t.me/hiearth_bot' }]
  }
  if (path === '/api/v1/user/telegram/link') {
    if (S.bot === 'off') return [503, { error: 'Бот HiEarth пока не подключён — попробуйте позже' }]
    setTimeout(() => { if (S.bot === 'unlinked') S.bot = 'linked' }, 6000) // «the person pressed Start»
    return [200, { url: 'https://t.me/hiearth_bot?start=link_mockmockmockmockmockmockmockmock', expires_at: iso(now() + 600000) }]
  }
  if (path === '/api/v1/user/telegram/notify') {
    if (body?.news !== undefined) S.botNews = body.news ? 'on' : 'off'
    if (body?.reminders !== undefined) S.botRem = body.reminders ? 'on' : 'off'
    return [200, { notify_news: S.botNews !== 'off', notify_reminders: S.botRem !== 'off' }]
  }
  if (path === '/api/v1/auth/magic') return [200, { url: 'http://localhost:5175/m/mock-magic', expires_in: 300 }]
  if (path === '/api/v1/auth/magic/redeem') return body?.token === 'mock-magic' ? [200, { linked: true, user_id: USER_ID, token: 'mock-token', next: '/wallet' }] : [404, { error: 'Ссылка для входа устарела или уже использована — откройте оплату из Telegram ещё раз' }]
  // Support (spec 08 §8.6)
  if (path === '/api/v1/support') {
    if (failing('support')) return UNAVAILABLE
    if (S.support === 'empty' && !SUPPORT.extra.length) return [200, { ticket: null, messages: [], unread: 0, telegram_linked: S.bot === 'linked', bot_url: S.bot === 'off' ? '' : 'https://t.me/hiearth_bot' }]
    const msgs = supportMessages()
    const status = SUPPORT.extra.length ? 'waiting' : S.support === 'waiting' ? 'open' : S.support
    return [200, { ticket: { id: 12, status, created_at: iso(now() - 2 * DAY) }, messages: msgs, unread: msgs.filter((m) => m.author === 'admin' && !m.read_at).length,
      telegram_linked: S.bot === 'linked', bot_url: S.bot === 'off' ? '' : 'https://t.me/hiearth_bot' }]
  }
  if (path === '/api/v1/support/messages') {
    const text = String(body?.body || '').trim()
    if (!text) return [400, { error: 'Напишите сообщение или приложите картинку' }]
    const m = { id: 100 + SUPPORT.extra.length, ticket_id: 12, author: 'user', body: text, via: 'site', created_at: iso(now()), read_at: null }
    SUPPORT.extra.push(m)
    return [200, { message: m, ticket: { id: 12, status: 'waiting' } }]
  }
  if (path === '/api/v1/support/read') { SUPPORT.read = true; return [200, { unread: 0 }] }
  if (path === '/api/v1/user/badges') return [200, { support: S.support === 'answered' && !SUPPORT.read ? 1 : 0, news: S.news === 'unread' && !NEWS.read ? 1 : 0 }]
  if (path === '/api/v1/news') {
    if (failing('news')) return UNAVAILABLE
    if (S.news === 'empty') return [200, { items: [], unread: 0, channel_url: '' }]
    return [200, { items: newsItems(), unread: S.news === 'unread' && !NEWS.read ? 1 : 0, channel_url: 'https://t.me/hiearth_news' }]
  }
  if (path === '/api/v1/stars/plans') {
    const rate = 1.3
    const cur = sub() ? CUR.id : 0
    return [200, { enabled: S.stars === 'on', plans: PLANS.filter((p) => p.price_rub > 0).map((p) => ({ ...p, stars: Math.ceil(p.price_rub / rate), current: p.id === cur,
      allowed: !cur || p.id === cur || p.price_rub >= CUR.price_rub, reason: cur && p.id !== cur && p.price_rub < CUR.price_rub ? 'Переход доступен после 14 февраля' : '' })) }]
  }
  if (path === '/api/v1/stars/invoice') return [200, { url: 'https://t.me/$mock-invoice', stars: 231 }]
  if (path === '/api/v1/news/read') { NEWS.read = true; return [200, { unread: 0 }] }
  if (path === '/api/v1/support/unread') return [200, { unread: S.support === 'answered' && !SUPPORT.read ? 1 : 0 }]
  if (path === '/api/v1/auth/telegram') return [200, { linked: false, telegram: { first_name: 'Иван' } }]
  if (path === '/api/v1/auth/telegram/register') return [200, { linked: true, created: true, user_id: USER_ID, token: 'mock-token', profile: profile() }]
  if (path === '/api/v1/auth/telegram/login') {
    const ok = [USER_ID, 'ivan_moscow'].includes(String(body?.id_or_username || '').toLowerCase()) && (body?.password === '1234' || S.login === 'nopassword')
    if (!ok) return [400, { error: String(body?.id_or_username || '').includes('-') ? 'user not found' : 'invalid password' }]
    return [200, { linked: true, user_id: USER_ID, token: 'mock-token', profile: profile() }]
  }
  return [404, { error: 'not found' }]
}

const NEWS = { read: false }
function newsItems() {
  return [
    { id: 2, title: 'Вход для мобильных сетей', body: 'Если на мобильном интернете сайты не открывались — <b>обновите подписку в Happ</b>: потяните список вниз. Новый вход подтянется сам.\n\nПодробнее — в <a href="https://hiearth.ru/help#mobile">помощи</a>.', image_url: '', button_text: 'Как обновить', button_url: 'https://hiearth.ru/help#mobile', sent_at: iso(now() - 3600000) },
    { id: 1, title: 'Приведи друга — по 100 ₽ каждому', body: 'Друг оплатит подписку — бонус придёт вам обоим.\n<blockquote>Ссылка — в кабинете, раздел «Пригласить друга».</blockquote>', image_url: '', button_text: '', button_url: '', sent_at: iso(now() - 5 * DAY) },
  ]
}
const SUPPORT = { extra: [], read: false }
function supportMessages() {
  const base = [
    { id: 1, ticket_id: 12, author: 'user', body: 'Здравствуйте! На мобильном интернете Happ подключается, но сайты не открываются. Wi-Fi — всё работает.', via: 'site', created_at: iso(now() - 2 * DAY), read_at: iso(now() - 2 * DAY) },
    { id: 2, ticket_id: 12, author: 'user', body: 'Телефон — iPhone, оператор МТС.', via: 'bot', created_at: iso(now() - 2 * DAY + 60000), read_at: iso(now() - 2 * DAY) },
  ]
  if (S.support === 'answered' || S.support === 'closed') {
    base.push({ id: 3, ticket_id: 12, author: 'admin', body: 'Здравствуйте! Обновите подписку в Happ: потяните список вниз. Для мобильных сетей мы добавили отдельный вход — он подтянется сам.\n\nЕсли не поможет — напишите, разберёмся.', via: 'admin', created_at: iso(now() - DAY), read_at: SUPPORT.read || S.support === 'closed' ? iso(now()) : null })
  }
  return [...base, ...SUPPORT.extra]
}

function controlPage() {
  const rows = Object.entries(OPTIONS).map(([k, vals]) => `<tr><th>${k}</th><td>${vals.map((v) => (S[k] === v ? `<b>[${v}]</b>` : `<a href="/__mock/set?${k}=${v}">${v}</a>`)).join(' · ')}</td></tr>`).join('')
  return `<!doctype html><meta charset="utf-8"><title>mock</title><style>body{font:14px/1.6 system-ui;background:#090C14;color:#EEF2F7;padding:24px}a{color:#7AA7FA}th{text-align:left;padding-right:16px;color:#A1ABBD}</style>
<h1>HiEarth API mock</h1><p>Вход: ID <code>${USER_ID}</code> или <code>ivan_moscow</code>, пароль <code>1234</code>. Промокоды: <code>ЛЕТО10</code> (скидка), <code>НЕДЕЛЯ</code> (кабинет). Код друга <code>FRIEND01</code>.</p>
<table>${rows}<tr><th>fail</th><td>${S.fail || '—'} · <a href="/__mock/set?fail=">очистить</a> · <a href="/__mock/set?fail=plans">plans</a> · <a href="/__mock/set?fail=profile,subscription,wallet">кабинет</a> · <a href="/__mock/set?fail=payments">payments</a> · <a href="/__mock/set?fail=topup">topup</a> · <a href="/__mock/set?fail=connections">connections</a></td></tr></table>
<p><a href="/__mock/reset">сбросить всё</a> · <a href="/">на сайт</a></p>`
}

export default function mockApi() {
  return {
    name: 'hiearth-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url, 'http://x')
        if (url.pathname === '/__mock') { res.setHeader('Content-Type', 'text/html; charset=utf-8'); res.end(controlPage()); return }
        if (url.pathname === '/__mock/set') {
          for (const [k, v] of url.searchParams) if (k in S) S[k] = v
          res.statusCode = 302; res.setHeader('Location', '/__mock'); res.end(); return
        }
        if (url.pathname === '/__mock/reset') { Object.assign(S, DEFAULT); res.statusCode = 302; res.setHeader('Location', '/__mock'); res.end(); return }
        if (!url.pathname.startsWith('/api/')) { next(); return }
        let raw = ''
        for await (const chunk of req) raw += chunk
        let body = null
        try { body = raw ? JSON.parse(raw) : null } catch { body = null }
        if (/multipart\//.test(req.headers['content-type'] || '')) { // support: only the text field matters here
          const m = raw.match(/name="body"\r\n\r\n([\s\S]*?)\r\n--/)
          body = { body: m ? m[1] : '' }
        }
        const authed = /^Bearer mock-/.test(req.headers.authorization || '')
        const [status, data] = route(req.method, url.pathname, url.searchParams, body, authed)
        await new Promise((r) => setTimeout(r, Number(S.delay) || 0))
        res.statusCode = status
        if (data === null) { res.end(); return }
        res.setHeader('Content-Type', 'application/json')
        res.end(JSON.stringify(data))
      })
    },
  }
}
