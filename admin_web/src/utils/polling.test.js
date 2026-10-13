import { test, mock } from 'node:test'
import assert from 'node:assert/strict'
import { startPolling, requestSequencer } from './polling.js'

test('each tick runs the callback current at that moment, not the one from the start', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const first = mock.fn()
  const second = mock.fn()
  const ref = { current: first }
  const stop = startPolling(ref, 10000)
  t.mock.timers.tick(10000)
  assert.equal(first.mock.callCount(), 1)
  ref.current = second // the page re-rendered with new filters
  t.mock.timers.tick(10000)
  assert.equal(first.mock.callCount(), 1, 'a stale callback ran after the filters changed')
  assert.equal(second.mock.callCount(), 1)
  stop()
  t.mock.timers.tick(30000)
  assert.equal(second.mock.callCount(), 1, 'ticks continue after stop')
})

test('no tick while the tab is hidden', (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  const fn = mock.fn()
  let visible = false
  const stop = startPolling({ current: fn }, 1000, () => visible)
  t.mock.timers.tick(3000)
  assert.equal(fn.mock.callCount(), 0)
  visible = true
  t.mock.timers.tick(1000)
  assert.equal(fn.mock.callCount(), 1)
  stop()
})

test('only the latest request may apply its answer', () => {
  const next = requestSequencer()
  const older = next()
  const newer = next()
  assert.equal(older(), false, 'an older answer arriving late would overwrite the new filter')
  assert.equal(newer(), true)
})
