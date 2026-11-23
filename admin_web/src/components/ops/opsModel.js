// Pure helpers of the «Операции» page (no React): tones, labels, sorting, the probes matrix.

// pathTone: a path's state as a colour role. Anything unknown never looks healthy.
export function pathTone(state) {
  switch (state) {
    case 'ok': return 'ok'
    case 'warn': return 'warn'
    case 'down': return 'danger'
    default: return 'muted' // off, unknown, and whatever comes later
  }
}

// ageLabel: how long ago, short — 42 с, 3 м, 2 ч, 5 д.
export function ageLabel(sec) {
  if (sec === undefined || sec === null || Number.isNaN(Number(sec))) return '—'
  const s = Math.max(0, Math.floor(Number(sec)))
  if (s < 60) return `${s} с`
  if (s < 3600) return `${Math.floor(s / 60)} м`
  if (s < 86400) return `${Math.floor(s / 3600)} ч`
  return `${Math.floor(s / 86400)} д`
}

// sortAlerts: critical before warn; an alert explained by its cause goes last; newest first inside.
export function sortAlerts(alerts) {
  const rank = (a) => (a.inhibited_by ? 2 : a.level === 'critical' ? 0 : 1)
  return [...(alerts || [])].sort((a, b) => {
    const d = rank(a) - rank(b)
    if (d !== 0) return d
    return String(b.since || '').localeCompare(String(a.since || ''))
  })
}

const IMPACT = {
  K0: 'К0 · никого не заденет',
  K1: 'К1 · секунды: сайт и новые входы',
  K2: 'К2 · рвёт соединения части пользователей',
  K3: 'К3 · рвёт всех',
}

// impactLabel: the impact class of an action or an event, in words.
export function impactLabel(code) {
  return IMPACT[code] ?? (code || '')
}

export function levelLabel(level) {
  return level === 'critical' ? 'критично' : 'внимание'
}

// wayLabel: a way tag for a person — p-r5-tcp → «вход 5 · Берёза».
export function wayLabel(tag) {
  const parts = String(tag || '').split('-')
  if (parts.length < 2) return tag
  let where
  if (parts[1] === 'hub') where = 'хаб'
  else if (/^r\d+$/.test(parts[1])) where = `вход ${parts[1].slice(1)}`
  else return tag
  if (parts[0] === 'b' && parts.length === 2) return `${where} · Бамбук`
  if (parts[0] === 'p' && parts[2] === 'tcp') return `${where} · Берёза`
  if (parts[0] === 'p' && parts[2] === 'xhttp') return `${where} · Дуб`
  return tag
}

const TARGET = {
  blocked: 'заблокированный сайт',
  ru: 'российский сайт',
  lint: 'сборка конфига',
  bulk: 'объект 64 КБ',
}

// groupProbes turns runs (newest first or not) into the matrix: one row per check
// (line / way / target) of the given vantage with its history, newest first. A run that did not
// make a check gives that check no point — it is not a failure.
export function groupProbes(runs, vantage) {
  const mine = (runs || []).filter(r => r.vantage === vantage)
  const ordered = [...mine].sort((a, b) => String(b.at).localeCompare(String(a.at)))
  const rows = new Map()
  for (const run of ordered) {
    for (const r of run.results || []) {
      const key = `${r.line}/${r.way || ''}/${r.target}`
      if (!rows.has(key)) {
        rows.set(key, {
          key, line: r.line, way: r.way || '', target: r.target,
          label: r.way ? wayLabel(r.way) : (TARGET[r.target] || r.target),
          history: [], last: null, fails: 0,
        })
      }
      const row = rows.get(key)
      const point = { runId: run.id, at: run.at, ok: !!r.ok, ms: r.ms, bytes: r.bytes, error: r.error || '' }
      row.history.push(point)
      if (!row.last) row.last = point
      if (!point.ok) row.fails += 1
    }
  }
  const lineOrder = []
  for (const row of rows.values()) if (!lineOrder.includes(row.line)) lineOrder.push(row.line)
  const kind = (row) => (row.way ? 0 : 1) // ways first, then the line's own targets
  return [...rows.values()].sort((a, b) => {
    const l = lineOrder.indexOf(a.line) - lineOrder.indexOf(b.line)
    if (l !== 0) return l
    const k = kind(a) - kind(b)
    if (k !== 0) return k
    return a.key.localeCompare(b.key)
  })
}

// probeSummary counts the runs of a vantage by state.
export function probeSummary(runs, vantage) {
  const out = { total: 0, pass: 0, fail: 0, blind: 0 }
  for (const r of runs || []) {
    if (r.vantage !== vantage) continue
    out.total += 1
    if (r.state in out) out[r.state] += 1
  }
  return out
}

// feedIcon: the icon name of a feed record by its source and kind.
export function feedIcon(item) {
  if (item.source === 'alert') return item.kind === 'raise' ? 'alert' : 'resolved'
  if (item.source === 'audit') return 'admin'
  if (item.source === 'ops') {
    switch (item.kind) {
      case 'restart': return 'restart'
      case 'start': return 'deploy'
      case 'boot': return 'power'
      case 'auto': return 'auto'
      case 'action': return 'admin'
      case 'probe': return 'probe'
      case 'maintenance': return 'maintenance'
      default: return 'note'
    }
  }
  return 'note'
}

const ACTIONS = {
  'entry.pathcheck': 'Проверить путь входа',
  'entry.rotation-off': 'Вывести вход из ротации',
  'entry.rotation-test': 'Вернуть вход через тест',
  'exit.verify': 'Проверить выход',
  'exit.reprovision': 'Переустановить выход (К2)',
  'exit.out-of-service': 'Убрать выход из работы',
  'hub.status': 'Посмотреть состояние хаба',
  'hub.restart-xray': 'Перезапустить xray (К3)',
  'hub.restart-bamboo': 'Перезапустить сервер Бамбука (К2)',
  'hub.restart-api': 'Перезапустить API (К1)',
  'hub.reload-nginx': 'Перечитать конфиг nginx',
  'hub.address': 'Сменить адрес хаба (К3)',
  'subscription.urgent': 'Включить «Срочное обновление»',
  'subscription.rollback': 'Откатить флаг подписки',
  'probes.run': 'Запустить проверку сейчас',
  'backup.run': 'Сделать бэкап',
  'node.reboot': 'Перезагрузить узел в окно',
  'payments.open': 'Открыть «Платежи»',
  'servers.capacity': 'Открыть «Мощность»',
  'settings.xray_revoke_restart': 'Настройка отсечки: когда перезапускать xray',
}

// actionLabel: an action id from the alert catalogue, in words.
export function actionLabel(id) {
  return ACTIONS[id] ?? id
}

// buildLine: «сборка abc123 · запущена 20.11 20:41».
export function buildLine(build, formatTime) {
  if (!build || !build.sha) return ''
  const parts = [`сборка ${build.sha}`]
  if (build.started_at) parts.push(`запущена ${formatTime(build.started_at)}`)
  return parts.join(' · ')
}

// ruViewLine: what the look from RU networks says, for the «Проверки» tab. Without an alert it
// says only that: a look that failed (a limit, the provider down) raises nothing, so «no alert»
// is never worded as «everything opens».
export function ruViewLine(ruAlerts) {
  const list = ruAlerts || []
  if (list.length === 0) {
    return 'сейчас нет тревог о недоступности из РФ. Тревога поднимается после двух недоступностей подряд; проверка, которая не удалась, ничего не поднимает и не снимает.'
  }
  return `активны тревоги — ${list.map(a => a.title).join('; ')}.`
}
