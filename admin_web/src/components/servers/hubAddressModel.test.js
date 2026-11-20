import test from 'node:test'
import assert from 'node:assert/strict'
import { HUB_STEPS, stepView, roleLabel, countdown, transitionBlockers, changeBanner } from './hubAddressModel.js'

const change = (state, extra = {}) => ({ id: 1, state, steps: {}, running: '', ...extra })

test('stepView: only the current step has a primary action', () => {
  for (const [state, key, label] of [
    ['added', 'check', 'Проверить'],
    ['checked', 'nodes', 'Подготовить узлы'],
    ['nodes_ready', 'dns', 'Проверить DNS'],
    ['dns_ready', 'switch', 'Переключить'],
  ]) {
    const v = stepView(change(state))
    const withPrimary = v.filter((s) => s.primary)
    assert.equal(withPrimary.length, 1, state)
    assert.equal(withPrimary[0].key, key, state)
    assert.equal(withPrimary[0].primary.label, label, state)
    assert.equal(v.find((s) => s.key === key).status, 'current')
  }
  assert.equal(HUB_STEPS.length, 7)
})

test('stepView: a running step has no buttons', () => {
  const v = stepView(change('added', { running: 'check' }))
  assert.equal(v.filter((s) => s.primary).length, 0)
  assert.equal(v.find((s) => s.key === 'check').status, 'running')
})

test('stepView: a failed step offers to run it again', () => {
  const v = stepView(change('draft', { steps: { add: { status: 'failed' } } }))
  const add = v.find((s) => s.key === 'add')
  assert.equal(add.status, 'failed')
  assert.equal(add.primary.label, 'Повторить')
  assert.equal(add.primary.action, 'step:add')
})

test('stepView: switching — confirm is disabled until the checks are green, revert is secondary', () => {
  const red = stepView(change('switching', { steps: { switch: { status: 'failed', checks: { green: false } } } })).find((s) => s.key === 'switch')
  assert.equal(red.primary.label, 'Подтвердить')
  assert.equal(red.primary.disabled, true)
  assert.equal(red.secondary.action, 'revert')
  const green = stepView(change('switching', { steps: { switch: { status: 'ok', checks: { green: true } } } })).find((s) => s.key === 'switch')
  assert.equal(green.primary.disabled, false)
})

test('stepView: transition — finish waits for the gate; finished and cancelled have no buttons', () => {
  const t = stepView(change('transition', { transition: { ready: false, blockers: ['прошло 3 ч из 72'] } })).find((s) => s.key === 'transition')
  assert.equal(t.primary.action, 'finish')
  assert.equal(t.primary.disabled, true)
  assert.equal(stepView(change('transition', { transition: { ready: true, blockers: [] } })).find((s) => s.key === 'transition').primary.disabled, false)
  for (const st of ['finished', 'cancelled', 'reverted']) {
    assert.equal(stepView(change(st)).filter((s) => s.primary).length, 0, st)
  }
  assert.ok(stepView(change('finished')).every((s) => s.status === 'done'))
})

test('roleLabel', () => {
  assert.equal(roleLabel('users'), 'для пользователей')
  assert.equal(roleLabel('source'), 'исходящий')
  assert.equal(roleLabel('gateway'), 'шлюз')
  assert.equal(roleLabel('new'), 'новый')
  assert.equal(roleLabel('retiring'), 'выводится')
  assert.equal(roleLabel('other'), 'прочий')
})

test('countdown', () => {
  const now = new Date('2026-11-21T03:00:00Z')
  assert.equal(countdown('2026-11-21T03:02:05Z', now), '02:05')
  assert.equal(countdown('2026-11-21T02:59:00Z', now), '00:00')
  assert.equal(countdown(null, now), '')
})

test('transitionBlockers and changeBanner', () => {
  assert.deepEqual(transitionBlockers({ blockers: ['a', 'b'] }), ['a', 'b'])
  assert.deepEqual(transitionBlockers(null), [])
  assert.match(changeBanner(change('reverting', { revert_reason: 'таймер' })).text, /откатывается.*таймер/)
  assert.equal(changeBanner(change('reverting')).tone, 'danger')
  assert.equal(changeBanner(change('added')), null)
})
