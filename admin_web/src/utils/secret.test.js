import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EMPTY_SECRET_ERROR, prepareSecret } from './secret.js'

test('a typed secret is sent without surrounding whitespace', () => {
  // a pasted YooKassa key / SMTP password with a trailing space or newline
  assert.deepEqual(prepareSecret('  live_abc123 \n'), { value: 'live_abc123', error: '' })
  assert.deepEqual(prepareSecret('\tpa ss\t'), { value: 'pa ss', error: '' }) // inner spaces stay
})

test('an all-whitespace secret is refused like an empty one', () => {
  assert.deepEqual(prepareSecret('   '), { value: '', error: EMPTY_SECRET_ERROR })
  assert.deepEqual(prepareSecret('\n\t'), { value: '', error: EMPTY_SECRET_ERROR })
  assert.deepEqual(prepareSecret(''), { value: '', error: EMPTY_SECRET_ERROR })
  assert.deepEqual(prepareSecret(undefined), { value: '', error: EMPTY_SECRET_ERROR })
})
