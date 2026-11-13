import { test } from 'node:test'
import assert from 'node:assert/strict'
import { hotkeyMatches, isTyping } from './useHotkey.js'

const ev = (key, extra = {}) => ({ key, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, target: { tagName: 'BODY' }, ...extra })

test('plain keys never fire while typing in a field (C3)', () => {
  assert.equal(hotkeyMatches(ev('j'), { key: 'j' }), true)
  assert.equal(hotkeyMatches(ev('j', { target: { tagName: 'INPUT' } }), { key: 'j' }), false)
  assert.equal(hotkeyMatches(ev('Enter', { target: { tagName: 'TEXTAREA' } }), { key: 'Enter' }), false)
  assert.equal(hotkeyMatches(ev('k', { target: { tagName: 'DIV', isContentEditable: true } }), { key: 'k' }), false)
  assert.equal(isTyping({ tagName: 'select' }), true)
})

test('modifiers: ⌘K works from a field, plain K does not fire on ⌘K', () => {
  assert.equal(hotkeyMatches(ev('k', { metaKey: true, target: { tagName: 'INPUT' } }), { key: 'k', meta: true }), true)
  assert.equal(hotkeyMatches(ev('k', { ctrlKey: true }), { key: 'k', meta: true }), true)
  assert.equal(hotkeyMatches(ev('k', { metaKey: true }), { key: 'k' }), false, 'row navigation must not fire on ⌘K')
  assert.equal(hotkeyMatches(ev('k'), { key: 'k', meta: true }), false)
  assert.equal(hotkeyMatches(ev('j', { altKey: true }), { key: 'j' }), false)
  assert.equal(hotkeyMatches(ev('J', { shiftKey: true }), { key: 'j' }), false)
})
