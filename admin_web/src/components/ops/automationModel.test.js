import { test } from 'node:test'
import assert from 'node:assert/strict'
import { modeLabel, ruleRows, actionModeRows } from './automationModel.js'

test('modeLabel and its tone', () => {
  assert.deepEqual(modeLabel('off'), { label: 'выключено', tone: 'default' })
  assert.deepEqual(modeLabel('dry-run'), { label: 'пробный режим', tone: 'warning' })
  assert.deepEqual(modeLabel('on'), { label: 'включено', tone: 'success' })
})

test('ruleRows: the engine rules first, a builtin shows its switch, A2 its note', () => {
  const rules = [
    { id: 'A3', title: 'Канарейка', builtin: true, setting: 'canary_auto_rollback', setting_value: 'on' },
    { id: 'A2', title: 'Выход мёртв', builtin: false, engine: false, note: 'только витрина' },
    { id: 'A1', title: 'Вход теряет потоки', engine: true, mode: 'dry-run', fired: 2, last_summary: 'сделал бы: …' },
    { id: 'A4', title: 'Сервис упал', builtin: true, setting: '' },
    { id: 'A12', title: 'node-agent', engine: true, mode: 'on', fired: 0 },
  ]
  const rows = ruleRows(rules)
  assert.deepEqual(rows.map(r => r.id), ['A1', 'A12', 'A2', 'A3', 'A4'])
  assert.equal(rows[0].switchable, true)
  assert.equal(rows[0].mode.label, 'пробный режим')
  assert.equal(rows[0].modeValue, 'dry-run')
  assert.equal(rows[2].switchText, 'только витрина')
  assert.equal(rows[3].switchText, 'настройка canary_auto_rollback: on')
  assert.equal(rows[4].switchText, 'всегда включено')
})

test('actionModeRows: only K2/K3 actions, armed or in dry-run', () => {
  const actions = [
    { id: 'backup.run', impact: 'K0', mode: 'on' },
    { id: 'hub.restart-xray', impact: 'K3', mode: 'dry-run', title: 'Перезапустить xray' },
    { id: 'hub.restart-bamboo', impact: 'K2', mode: 'on', title: 'Перезапустить сервер Бамбука' },
  ]
  const rows = actionModeRows(actions)
  assert.deepEqual(rows.map(r => r.id), ['hub.restart-bamboo', 'hub.restart-xray'])
  assert.equal(rows[1].next, 'on')
  assert.equal(rows[0].next, 'dry-run')
})
