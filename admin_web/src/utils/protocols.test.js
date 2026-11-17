import { test } from 'node:test'
import assert from 'node:assert/strict'
import { protocolName, paymentStatusName } from './protocols.js'

test('protocols and payment statuses in words (C12)', () => {
  assert.equal(protocolName('vless'), 'Берёза')
  assert.equal(protocolName('HYSTERIA2'), 'Бамбук')
  assert.equal(protocolName('xhttp'), 'Дуб')
  assert.equal(protocolName('all'), 'Все')
  assert.equal(protocolName('other'), 'other')
  assert.equal(paymentStatusName('succeeded'), 'оплачен')
  assert.equal(paymentStatusName('weird'), 'weird')
})
