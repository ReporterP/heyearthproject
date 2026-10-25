import { test } from 'node:test'
import assert from 'node:assert/strict'
import { tabKeyTarget, rovingIndex, focusTrapTarget } from './a11y.js'

test('tabs: arrows wrap around, Home/End jump, other keys are ignored', () => {
  assert.equal(tabKeyTarget('ArrowRight', 0, 3), 1)
  assert.equal(tabKeyTarget('ArrowRight', 2, 3), 0)
  assert.equal(tabKeyTarget('ArrowLeft', 0, 3), 2)
  assert.equal(tabKeyTarget('ArrowLeft', 2, 3), 1)
  assert.equal(tabKeyTarget('Home', 2, 3), 0)
  assert.equal(tabKeyTarget('End', 0, 3), 2)
  assert.equal(tabKeyTarget('Enter', 1, 3), -1)
  assert.equal(tabKeyTarget('ArrowDown', 1, 3), -1)
  assert.equal(tabKeyTarget('ArrowRight', 0, 0), -1)
})

test('tabs: the selected tab is the one reachable by Tab; without a match the first one is', () => {
  const tabs = [{ key: 'exit' }, { key: 'relay' }]
  assert.equal(rovingIndex(tabs, 'relay'), 1)
  assert.equal(rovingIndex(tabs, 'nope'), 0)
  assert.equal(rovingIndex([], 'exit'), -1)
})

test('focus trap: Tab wraps at the edges, pulls stray focus back, leaves the middle to the browser', () => {
  assert.equal(focusTrapTarget(3, 2, false), 0) // last → first
  assert.equal(focusTrapTarget(3, 0, true), 2) // first → last (Shift+Tab)
  assert.equal(focusTrapTarget(3, -1, false), 0) // focus outside the dialog
  assert.equal(focusTrapTarget(3, -1, true), 2)
  assert.equal(focusTrapTarget(3, 1, false), null) // browser moves focus itself
  assert.equal(focusTrapTarget(3, 1, true), null)
  assert.equal(focusTrapTarget(1, 0, false), 0)
  assert.equal(focusTrapTarget(0, -1, false), -1) // nothing focusable: stay put
})
