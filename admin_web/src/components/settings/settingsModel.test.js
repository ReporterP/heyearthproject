import { test } from 'node:test'
import assert from 'node:assert/strict'
import { SETTINGS_SECTIONS, SETTING_KEYS, checkSetting, shownValue } from './settingsModel.js'

const field = (key) => SETTINGS_SECTIONS.flatMap((s) => s.fields).find((f) => f.key === key)

test('the blocks of «Настройки» (C9), no dead keys', () => {
  assert.deepEqual(SETTINGS_SECTIONS.map((s) => s.title), ['Платежи', 'Почта', 'Юр. данные и поддержка', 'Лимиты', 'Приведи друга', 'Подключение'])
  for (const dead of ['hub_vless_port', 'mtproto_host', 'price_premium', 'price_standard', 'b2b_price_per_user', 'app_build_repo_url', 'donation_enabled']) {
    assert.ok(!SETTING_KEYS.includes(dead), dead)
  }
  assert.ok(SETTING_KEYS.includes('quota_cutoff_enabled'))
  assert.ok(SETTING_KEYS.includes('referral_min_purchase_rub'))
})

test('values are checked like the API checks them', () => {
  assert.deepEqual(checkSetting(field('wallet_min_topup_rub'), ' 150 '), { value: '150', error: '' })
  assert.match(checkSetting(field('wallet_min_topup_rub'), '0').error, /от 1/)
  assert.match(checkSetting(field('early_renewal_days_threshold'), '1.5').error, /Целое/)
  assert.equal(checkSetting(field('receipt_fallback_email'), '').error, '')
  assert.match(checkSetting(field('receipt_fallback_email'), 'nope').error, /email/)
  assert.match(checkSetting(field('hub_main_server_host'), 'https://example.test').error, /без http/)
  assert.equal(checkSetting(field('hub_main_server_host'), '203.0.113.1').error, '')
  assert.match(checkSetting(field('legal_inn'), '123').error, /ИНН/)
  assert.deepEqual(checkSetting(field('b2b_member_plan'), ' All-Inclusive '), { value: 'all-inclusive', error: '' })
  assert.match(checkSetting(field('b2b_member_plan'), 'полный').error, /Слаг/)
  assert.equal(shownValue(field('smtp_port'), {}), '587')
  assert.equal(shownValue(field('smtp_port'), { smtp_port: '465' }), '465')
})
