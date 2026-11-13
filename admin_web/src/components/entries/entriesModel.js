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
export const healthyRelays = (entries) => (entries || []).filter((e) => e?.kind === 'relay' && e?.health?.external === 'healthy')

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
    text: 'Happ получает обычный список строк: Берёза, Дуб, Бамбук, «· резерв». Пользователь сам выбирает строку.',
  },
  {
    key: 'test',
    title: 'Только тестовый пользователь',
    text: '«⚡ Авто» видит один пользователь (ID ниже) — проверьте на своём телефоне в Wi-Fi и в мобильной сети, прежде чем включать всем.',
  },
  {
    key: 'on',
    title: 'Для всех',
    text: 'Первая строка у всех в Happ — «⚡ Авто»: раз в минуту меряет Hub и каждый вход в РФ и ведёт трафик через самый быстрый живой. Бамбука в этом виде нет.',
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
