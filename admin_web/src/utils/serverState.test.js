import { test } from 'node:test'
import assert from 'node:assert/strict'
import { lifecycleView, pathView, inWork, LIFECYCLE_FILTER, tunnelView } from './serverState.js'

test('a missing or unknown lifecycle state is never shown as «В работе»', () => {
  assert.equal(lifecycleView('online').label, 'В работе')
  assert.notEqual(lifecycleView(undefined).label, 'В работе')
  assert.match(lifecycleView('weird').label, /weird/)
})

test('«в работе» = lifecycle online and a healthy tunnel; node-agent status does not count', () => {
  assert.equal(inWork({ lifecycle_state: 'online', tunnel_status: 'healthy' }), true)
  assert.equal(inWork({ lifecycle_state: 'online', tunnel_status: 'unhealthy' }), false)
  assert.equal(inWork({ status: 'online' }), false)
})

test('the list filter offers exactly the states the backend accepts', () => {
  assert.deepEqual(LIFECYCLE_FILTER.map((o) => o.value),
    ['', 'pending', 'provisioning', 'online', 'failed', 'deprovisioning', 'offline'])
})

test('path states read as words', () => {
  assert.equal(pathView('unhealthy').label, 'не работает')
  assert.equal(pathView(undefined).label, 'проверяется')
})

test('tunnel state is shown only for online exits (the monitor leaves others as they were)', () => {
  assert.equal(tunnelView({ is_exit_node: true, lifecycle_state: 'online', tunnel_status: 'healthy' }).label, 'работает')
  assert.equal(tunnelView({ is_exit_node: true, lifecycle_state: 'offline', tunnel_status: 'healthy' }), null)
  assert.equal(tunnelView({ is_exit_node: true, lifecycle_state: 'deprovisioning', tunnel_status: 'unhealthy' }), null)
  assert.equal(tunnelView({ is_exit_node: false, lifecycle_state: 'online', tunnel_status: 'healthy' }), null)
})
