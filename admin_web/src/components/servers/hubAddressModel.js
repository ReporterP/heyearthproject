// Model of the Hub address wizard («Серверы → Hub → Адрес хаба»): which step is where, which one
// button each step shows, role labels, the revert countdown. Pure; tested with node --test.

export const HUB_STEPS = [
  { key: 'add', title: 'Добавить адрес' },
  { key: 'check', title: 'Проверить' },
  { key: 'nodes', title: 'Подготовить узлы' },
  { key: 'dns', title: 'DNS' },
  { key: 'switch', title: 'Переключить' },
  { key: 'transition', title: 'Переходный период' },
  { key: 'finish', title: 'Вывести старый адрес' },
]

// Index of the step a state waits on (HUB_STEPS.length = all done).
const STATE_STEP = {
  draft: 0, added: 1, checked: 2, nodes_ready: 3, dns_ready: 4,
  switching: 4, transition: 5, reverting: 4, finished: 7, cancelled: -1, reverted: -1,
}

const RUN_LABEL = { add: 'Повторить', check: 'Проверить', nodes: 'Подготовить узлы', dns: 'Проверить DNS', switch: 'Переключить' }

/** stepView: one row per step with its status and at most one primary action (on the current step). */
export function stepView(change) {
  const state = change?.state || 'draft'
  const cur = STATE_STEP[state] ?? 0
  const steps = change?.steps || {}
  const running = change?.running || ''
  return HUB_STEPS.map((s, i) => {
    const rec = steps[s.key] || {}
    let status = i < cur ? 'done' : i === cur ? 'current' : 'pending'
    if (cur < 0) status = 'pending'
    if (running === s.key) status = 'running'
    else if (i === cur && rec.status === 'failed') status = 'failed'
    const row = { key: s.key, title: s.title, status, primary: null, secondary: null }
    if (running || i !== cur) return row
    if (state === 'switching') {
      const green = Boolean(rec.checks?.green)
      row.primary = { label: 'Подтвердить', action: 'confirm', disabled: !green }
      row.secondary = { label: 'Вернуть как было', action: 'revert' }
      return row
    }
    if (state === 'transition') {
      row.primary = { label: 'Вывести старый адрес', action: 'finish', disabled: !change?.transition?.ready }
      row.secondary = { label: 'Вернуть как было', action: 'revert' }
      return row
    }
    if (RUN_LABEL[s.key] && state !== 'reverting') {
      const again = rec.status === 'failed' || rec.status === 'interrupted'
      row.primary = { label: s.key === 'add' ? 'Повторить' : again ? `${RUN_LABEL[s.key]} ещё раз` : RUN_LABEL[s.key], action: `step:${s.key}` }
      if (s.key === 'add' && !again) row.primary = null // step 1 runs by itself when the change starts
    }
    return row
  })
}

const ROLES = {
  users: 'для пользователей', source: 'исходящий', gateway: 'шлюз',
  new: 'новый', retiring: 'выводится', other: 'прочий',
}
export const roleLabel = (r) => ROLES[r] || r

/** countdown: "мм:сс" until deadline ("00:00" once past, "" without one). */
export function countdown(deadline, now = new Date()) {
  if (!deadline) return ''
  const left = Math.max(0, Math.floor((new Date(deadline) - now) / 1000))
  const mm = String(Math.floor(left / 60)).padStart(2, '0')
  const ss = String(left % 60).padStart(2, '0')
  return `${mm}:${ss}`
}

export const transitionBlockers = (view) => view?.blockers || []

/** changeBanner: the line above the wizard for states that are not a step (revert, cancel, done). */
export function changeBanner(change) {
  switch (change?.state) {
    case 'reverting':
      return { tone: 'danger', text: `Смена откатывается: ${change.revert_reason || 'причина не записана'}. Входы возвращаются на старый адрес.` }
    case 'reverted':
      return { tone: 'warning', text: `Смена откатана: ${change.revert_reason || ''}`.trim() }
    case 'cancelled':
      return { tone: 'default', text: 'Смена отменена.' }
    case 'finished':
      return { tone: 'success', text: `Хаб работает на ${change.new_ip}.` }
  }
  return null
}

/** hubAddrActive: a change the page should show (not final). */
export const hubAddrActive = (change) => Boolean(change) && !['finished', 'cancelled', 'reverted'].includes(change.state)
