// «Входы» as pure helpers (node --test): the mobile check verdict and the user-facing mode texts.

// mobileCheckSummary: one sentence for the owner about all phones tested so far.
// state: none (nothing yet) | pass (every phone got the whole file) | fail (none did) | mixed.
export function mobileCheckSummary(results) {
  const list = Array.isArray(results) ? results : []
  const passed = list.filter((r) => r?.ok).length
  const failed = list.length - passed
  if (list.length === 0) return { state: 'none', passed, failed, text: '' }
  const names = (ok) => [...new Set(list.filter((r) => !!r?.ok === ok).map((r) => r?.operator || 'неизвестный оператор'))].join(', ')
  if (failed === 0) {
    return { state: 'pass', passed, failed, text: `Адрес проходит (${names(true)}). Можно настраивать вход.` }
  }
  if (passed === 0) {
    return {
      state: 'fail', passed, failed,
      text: `Адрес не проходит (${names(false)}). Смените IP у хостера (или возьмите сервер у другого провайдера) и проверьте снова.`,
    }
  }
  return {
    state: 'mixed', passed, failed,
    text: `Проходит у ${names(true)}, не проходит у ${names(false)}. Пользователи этих операторов останутся без мобильного интернета — лучше сменить IP.`,
  }
}

// The three ways users connect (PUT /api/admin/entries/mode).
export const ENTRY_MODES = [
  {
    key: 'relay',
    title: 'Все через резервный вход',
    text: 'Подписка у всех ведёт на выбранный резервный вход. Нужен, когда мобильные операторы режут адрес Hub. Hub остаётся строками «· напрямую».',
  },
  {
    key: 'auto',
    title: 'Hub, а при аварии — резервный вход',
    text: 'Пользователи подключаются к Hub; если он 3 минуты подряд недоступен снаружи, подписка сама переключится на здоровый резервный вход и вернётся после 30 минут стабильной работы.',
  },
  {
    key: 'hub',
    title: 'Только Hub',
    text: 'Подписка всегда ведёт на Hub. Резервные входы остаются отдельными строками «· резерв».',
  },
]

// healthyRelays: reserve entries that answer from outside right now (the only valid «relay» targets).
// A target of «Все через резервный вход» must also be online and in rotation (the API refuses the rest).
export const healthyRelays = (entries) => (entries || []).filter((e) => e?.kind === 'relay' && e?.health?.external === 'healthy'
  && (e?.rotation || 'all') === 'all' && (e?.lifecycle || 'online') === 'online')

// modeBanner: what the owner sees on top of «Входы» for the current state.
export function modeBanner(failover, entries) {
  const fo = failover || {}
  const target = (entries || []).find((e) => e?.id === fo.target)
  const name = target?.name || `вход ${fo.target}`
  if (fo.active && fo.pinned) {
    const dead = target && target.health?.external !== 'healthy'
    return dead
      ? { tone: 'danger', text: `Все пользователи настроены на «${name}», но он сейчас не отвечает снаружи — подписка временно ведёт на Hub. Проверьте сервер или выберите другой вход.` }
      : { tone: 'success', text: `Все пользователи подключаются через «${name}» (${target?.address || '—'}). Hub доступен строками «· напрямую».` }
  }
  if (fo.active) {
    return { tone: 'danger', text: `Авария: Hub недоступен снаружи, подписка временно ведёт на «${name}» и обновляется раз в час. Возврат — после 30 минут стабильной работы Hub.` }
  }
  if (fo.enabled) {
    return { tone: 'default', text: 'Пользователи подключаются к Hub. Если он станет недоступен снаружи, подписка сама переключится на здоровый резервный вход.' }
  }
  return { tone: 'default', text: 'Пользователи подключаются только к Hub. Резервные входы — отдельными строками «· резерв».' }
}

// The JSON «Авто» subscription for Happ (PUT /api/admin/entries/settings auto_mode).
export const AUTO_MODES = [
  {
    key: 'off',
    title: 'Выключено',
    text: 'Happ получает обычный список строк: Берёза, Дуб, Бамбук и «· резерв» для каждого входа. Пользователь выбирает строку сам.',
  },
  {
    key: 'test',
    title: 'Только тестовый пользователь',
    text: '«⚡ Авто» видит один пользователь (ID ниже) — проверьте на своём телефоне в Wi-Fi и в мобильной сети, прежде чем включать всем.',
  },
  {
    key: 'on',
    title: 'Для всех',
    text: 'В Happ у всех три строки: «⚡ Авто» и «🎋 Бамбук 1/2» (только Wi-Fi). Каждая сама проверяет Hub и входы в РФ: трафик идёт через Hub, пока он отвечает стабильно, иначе через вход.',
  },
]

// The name the subscription domain gets when the owner has not typed one.
export const suggestedSubDomain = (site) => `go.${site || 'hiearth.ru'}`

// subscriptionDomainState: one line about the subscription domain (GET /api/admin/entries → subscription).
// tone: default (no domain) | success | warning (DNS to fix) | danger (no certificate / no DNS).
export function subscriptionDomainState(sub) {
  const s = sub || {}
  if (!s.domain) {
    return { tone: 'default', text: 'Домен подписки не задан: ссылки подписки ведут на основной сайт, который мобильные сети режут.' }
  }
  if (s.dns_error || !(s.dns || []).length) {
    return { tone: 'danger', text: `${s.domain} не резолвится: добавьте A-записи на адреса входов.` }
  }
  if (!s.cert_ok) {
    return { tone: 'danger', text: `Hub не отдаёт сертификат для ${s.domain}${s.cert_error ? ` (${s.cert_error})` : ''}: запустите deploy --nginx.` }
  }
  if ((s.dns_foreign || []).length) {
    return { tone: 'warning', text: `У ${s.domain} есть A-записи не на входы (${s.dns_foreign.join(', ')}) — удалите их, иначе телефоны будут иногда попадать мимо входа.` }
  }
  if ((s.dns_missing || []).length) {
    return { tone: 'warning', text: `Работает. Входы без A-записи: ${s.dns_missing.join(', ')} — добавьте, чтобы подписка не зависела от одного входа.` }
  }
  return { tone: 'success', text: `Работает: ссылки подписки выдаются на ${s.domain}, обновление идёт через входы в РФ.` }
}

// ── Entries management (docs/prd/entries-management.prd.md) ──

// The kinds of the stability samples (entry_probe_samples) as «Входы» names them.
export const STABILITY_KINDS = [['tcp443', ':443'], ['tcp2443', ':2443'], ['udp', 'Бамбук']]
export const FEW_CHECKS = 20

// formatPct: a share 0..1 as «99,6 %» (one decimal, truncated: 99,95 % is not «100,0 %»).
export function formatPct(share) {
  const v = Math.floor(share * 1000) / 10
  return `${v.toFixed(1).replace('.', ',')} %`
}

function windowView(win) {
  const kinds = STABILITY_KINDS.filter(([k]) => win?.[k]?.total > 0)
  if (kinds.length === 0) return { share: null, text: '—', tone: 'default', few: true }
  let worst = 1
  let checks = 0
  for (const [k] of kinds) {
    worst = Math.min(worst, win[k].ok / win[k].total)
    checks = Math.max(checks, win[k].checks || 0)
  }
  const few = checks < FEW_CHECKS
  const tone = few ? 'default' : worst >= 0.99 ? 'success' : worst >= 0.95 ? 'warning' : 'danger'
  return { share: worst, text: formatPct(worst), tone, few }
}

// stabilityView: the worst kind over 1 h and 24 h (tones: ≥ 99 % success, ≥ 95 % warning, below danger;
// fewer than FEW_CHECKS checks — «мало данных»), and each kind for the entry panel.
export function stabilityView(st) {
  const byKind = STABILITY_KINDS.filter(([k]) => st?.h1?.[k] || st?.h24?.[k]).map(([kind, label]) => ({
    kind, label, h1: windowView({ [kind]: st?.h1?.[kind] }), h24: windowView({ [kind]: st?.h24?.[kind] }),
  }))
  return { h1: windowView(st?.h1), h24: windowView(st?.h24), byKind }
}

const LIFECYCLE_LABEL = {
  pending: ['ждёт настройки', 'default'], provisioning: ['настраивается', 'info'], failed: ['ошибка настройки', 'danger'],
  deprovisioning: ['отключается', 'warning'], offline: ['отключён', 'default'],
}
const ROTATION_LABEL = { all: ['в ротации', 'success'], test: ['только тест', 'warning'], off: ['вне ротации', 'danger'] }

// rotationView: the «Состояние» cell — a setup state first (an entry not online serves nobody), then
// the rotation with its reason and since when.
export function rotationView(e) {
  if (e?.kind !== 'relay') return { label: 'основной', tone: 'default', reason: '', since: null }
  const life = LIFECYCLE_LABEL[e.lifecycle]
  if (life) return { label: life[0], tone: life[1], reason: '', since: null }
  const [label, tone] = ROTATION_LABEL[e.rotation] || ROTATION_LABEL.all
  return { label, tone, reason: e.rotation_reason || '', since: e.rotation === 'all' ? null : e.rotation_at || null }
}

const DAY_MS = 24 * 3600 * 1000
const on = (enabled, reason = '') => ({ enabled: !!enabled && !reason, reason })

// entryActions: every button of the entry panel with why it is off (the API checks the same).
// ctx: { testUser (the «Тестовый пользователь» of «Входы»), failover, now }.
export function entryActions(e, { testUser = '', failover = {}, now = new Date() } = {}) {
  const relay = e?.kind === 'relay'
  const online = relay && e.lifecycle === 'online'
  const rotation = e?.rotation || 'all'
  const target = !!failover?.active && failover?.target === e?.id
  const checks = e?.checks || {}
  const notOnline = online ? '' : 'вход не настроен — сначала «Переустановить» в карточке сервера'
  const noPath = checks.path_fresh ? '' : 'нужна успешная «Проверить путь» за последний час (и после последней настройки)'
  const early = rotation === 'test' && (!e.rotation_at || now - new Date(e.rotation_at) < DAY_MS)
  return {
    pathcheck: on(online, notOnline),
    tunnelcheck: on(online && !e.tunnel, notOnline || (e?.tunnel ? 'вход уже на туннеле: его путь проверяет «Проверить путь»' : '')),
    out: on(relay && rotation !== 'off', target ? 'на этот вход ведут основные строки подписки — сначала смените режим «Куда подключаются пользователи»' : ''),
    toTest: on(relay && rotation === 'off', notOnline || noPath || (testUser ? '' : 'задайте «Тестовый пользователь» в настройках «Входов»')),
    toAll: { ...on(relay && rotation === 'test', notOnline || noPath), early },
    tunnelOn: on(online && !e.tunnel, notOnline || (checks.tunnel_fresh ? '' : 'сначала «Проверить туннель»: нужен успешный результат за последние сутки')),
    tunnelOff: on(online && e.tunnel, notOnline),
    remove: {
      ...on(relay, target ? 'на этот вход ведут основные строки подписки — сначала смените режим' : ''),
      stage: relay && ['offline', 'pending'].includes(e.lifecycle) ? 'record' : 'deprovision',
    },
  }
}
