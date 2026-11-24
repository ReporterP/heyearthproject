// «Автоматика» (GET /api/admin/ops/automation, GET /api/admin/ops/actions): the 16 rules with
// their mode or switch, and the K2/K3 console actions that ship in dry-run.

const MODES = {
  off: { label: 'выключено', tone: 'default' },
  'dry-run': { label: 'пробный режим', tone: 'warning' },
  on: { label: 'включено', tone: 'success' },
}

export function modeLabel(mode) {
  return MODES[mode] || { label: mode || '—', tone: 'default' }
}

const ruleNum = (id) => Number(String(id).replace(/^A/, '')) || 0

export function ruleRows(rules = []) {
  return [...rules]
    .sort((a, b) => {
      if (!!a.engine !== !!b.engine) return a.engine ? -1 : 1
      return ruleNum(a.id) - ruleNum(b.id)
    })
    .map(r => {
      let switchText = ''
      if (!r.engine) {
        if (r.note && !r.builtin) switchText = r.note
        else if (r.setting) switchText = `настройка ${r.setting}: ${r.setting_value || '—'}`
        else switchText = 'всегда включено'
      }
      return { ...r, switchable: !!r.engine, modeValue: r.mode, mode: r.engine ? modeLabel(r.mode) : null, switchText }
    })
}

export function actionModeRows(actions = []) {
  return actions
    .filter(a => a.impact === 'K2' || a.impact === 'K3')
    .sort((a, b) => a.id.localeCompare(b.id))
    .map(a => ({ ...a, modeView: modeLabel(a.mode), next: a.mode === 'on' ? 'dry-run' : 'on' }))
}
