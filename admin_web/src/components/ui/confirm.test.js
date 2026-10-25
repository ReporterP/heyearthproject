import { test } from 'node:test'
import assert from 'node:assert/strict'
import { confirmMatches } from './confirm.js'

test('typed confirmation equals the name exactly (spaces around are forgiven)', () => {
  assert.equal(confirmMatches('Франкфурт', 'Франкфурт'), true)
  assert.equal(confirmMatches('  Франкфурт ', 'Франкфурт'), true)
  assert.equal(confirmMatches('франкфурт', 'Франкфурт'), false)
  assert.equal(confirmMatches('Франк', 'Франкфурт'), false)
  assert.equal(confirmMatches('', ''), false)
  assert.equal(confirmMatches(undefined, 'x'), false)
})

test('nothing to confirm against never matches', () => {
  assert.equal(confirmMatches('x', undefined), false)
  assert.equal(confirmMatches('   ', '   '), false)
  assert.equal(confirmMatches('Франкфурт 2', 'Франкфурт'), false)
})
