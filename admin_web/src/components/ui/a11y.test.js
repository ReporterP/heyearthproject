import { test } from 'node:test'
import assert from 'node:assert/strict'
import { tabKeyTarget, rovingIndex, focusTrapTarget, escapeAction, copyAnnouncement, createModalKeyHandler } from './a11y.js'

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

test('escape in a modal: closes it, but never while its action runs (the error must stay visible)', () => {
  assert.equal(escapeAction(false), 'close')
  assert.equal(escapeAction(true), 'ignore')
})

test('copy feedback: words for each state; a repeated result reads differently so it is announced again', () => {
  assert.equal(copyAnnouncement('idle', 0), '')
  assert.match(copyAnnouncement('done', 1), /^Скопировано/)
  assert.match(copyAnnouncement('failed', 1), /^Не удалось скопировать/)
  assert.notEqual(copyAnnouncement('done', 1), copyAnnouncement('done', 2))
  assert.notEqual(copyAnnouncement('failed', 3), copyAnnouncement('failed', 4))
  assert.equal(copyAnnouncement('done', 1).trim(), copyAnnouncement('done', 2).trim())
})

function fakeKey(key, shiftKey = false) {
  const e = { key, shiftKey, stopped: false, prevented: false }
  e.stopPropagation = () => { e.stopped = true }
  e.preventDefault = () => { e.prevented = true }
  return e
}

test('modal keys: Escape is stopped before window listeners (Drawer underneath) and handed to onEscape', () => {
  let escapes = 0
  const on = createModalKeyHandler({ focusables: () => [], active: () => null, onEscape: () => () => { escapes++ } })
  const esc = fakeKey('Escape')
  on(esc)
  assert.deepEqual([esc.stopped, esc.prevented, escapes], [true, true, 1])

  const plain = createModalKeyHandler({ focusables: () => [], active: () => null, onEscape: () => undefined })
  const passthrough = fakeKey('Escape')
  plain(passthrough) // no owner: the base Dialog keeps handling Escape itself
  assert.deepEqual([passthrough.stopped, passthrough.prevented], [false, false])
})

test('modal keys: Tab wraps inside the modal, the middle is left to the browser, other keys untouched', () => {
  const focused = []
  const item = (name) => ({ name, focus: () => focused.push(name) })
  const items = [item('input'), item('cancel'), item('action')]
  let active = items[2]
  const on = createModalKeyHandler({ focusables: () => items, active: () => active, onEscape: () => undefined })

  const wrap = fakeKey('Tab')
  on(wrap)
  assert.deepEqual([wrap.prevented, wrap.stopped, focused], [true, false, ['input']])

  active = items[1]
  const middle = fakeKey('Tab')
  on(middle)
  assert.deepEqual([middle.prevented, focused.length], [false, 1])

  const enter = fakeKey('Enter')
  on(enter)
  assert.deepEqual([enter.prevented, enter.stopped], [false, false])
})
