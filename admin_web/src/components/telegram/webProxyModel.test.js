import test from 'node:test'
import assert from 'node:assert/strict'
import {
  RUN_POLL_MS, IDLE_POLL_MS, isRunning, pollDelay, overallState, formFrom, effectiveHost, payloadFrom, isDirty, hostError,
  saveCheck, saveMessage, fmtInt, humanBytes, plural, daysText, agoText, probeView, servicesList, disconnectsView, certView,
  dnsView, runView, stepView, autoOpenStep, stepKey, exitsView,
} from './webProxyModel.js'

const NB = ' '
const base = {
  enabled: true, host: 'tg.hiearth.ru', default_host: 'tg.hiearth.ru', live_host: 'tg.hiearth.ru', secret_set: true,
  link_ready: true, artifact: { present: true, commit: 'c8adb8b' },
  status: { checked_at: '2026-11-16T12:00:00Z', healthz: true, readyz: true, readyz_error: '', services: {}, metrics: null, middle_end_disconnects_5m: null, cert: null, dns: null },
  run: null,
}

test('polling: 3 s while a run is in progress, 30 s otherwise', () => {
  assert.equal(isRunning({ run: { status: 'running' } }), true)
  assert.equal(isRunning({ run: null }), false)
  assert.equal(pollDelay({ run: { status: 'running' } }), RUN_POLL_MS)
  assert.equal(pollDelay({ run: { status: 'failed' } }), IDLE_POLL_MS)
  assert.equal(RUN_POLL_MS, 3000)
  assert.equal(IDLE_POLL_MS, 30000)
})

test('overallState: working only with the link out and readyz; off; not ready with a reason', () => {
  assert.deepEqual(overallState(base), { label: 'Работает', variant: 'success' })
  assert.equal(overallState({ ...base, enabled: false, link_ready: false }).label, 'Выключен')
  assert.equal(overallState(null).variant, 'default')
  const notReady = overallState({ ...base, status: { ...base.status, readyz: false, readyz_error: 'backend dial' } })
  assert.equal(notReady.variant, 'warning')
  assert.match(notReady.hint, /backend dial/)
  assert.match(overallState({ ...base, link_ready: false }).hint, /Установить \/ обновить/)
  assert.match(overallState({ ...base, link_ready: false, run: { status: 'running' } }).hint, /Идёт установка/)
  assert.match(overallState({ ...base, link_ready: false, artifact: { present: false } }).hint, /нет сборки/)
  assert.match(overallState({ ...base, status: null }).hint, /Проверки ещё не было/)
})

test('form: an empty host means the default one; dirty compares effective hosts', () => {
  assert.deepEqual(formFrom(base), { enabled: true, host: 'tg.hiearth.ru' })
  assert.deepEqual(formFrom(null), { enabled: false, host: '' })
  assert.equal(effectiveHost('  TG.Example.RU ', 'tg.hiearth.ru'), 'tg.example.ru')
  assert.equal(effectiveHost('', 'tg.hiearth.ru'), 'tg.hiearth.ru')
  assert.deepEqual(payloadFrom({ enabled: true, host: '' }, base), { enabled: true, host: 'tg.hiearth.ru' })
  assert.equal(isDirty({ enabled: true, host: '' }, base), false)
  assert.equal(isDirty({ enabled: true, host: 'TG.hiearth.ru ' }, base), false)
  assert.equal(isDirty({ enabled: true, host: 'tg.other.ru' }, base), true)
  assert.equal(isDirty({ enabled: false, host: 'tg.hiearth.ru' }, base), true)
  assert.equal(isDirty(null, base), false)
})

test('hostError: plain DNS names only', () => {
  assert.equal(hostError(''), '')
  assert.equal(hostError('tg.hiearth.ru'), '')
  assert.equal(hostError('tg-1.sub.example.com'), '')
  assert.match(hostError('https://tg.hiearth.ru'), /без https/)
  assert.match(hostError('tg.hiearth.ru/path'), /без https/)
  assert.match(hostError('tg.hiearth.ru:443'), /Без порта/)
  assert.match(hostError('localhost'), /Не похоже/)
  assert.match(hostError('-bad.example.ru'), /Не похоже/)
  assert.match(hostError('203.0.113.10'), /Не похоже/)
})

test('saveCheck: permission, no changes, a run in progress, a bad host; turning off needs a confirm', () => {
  const on = { enabled: true, host: 'tg.other.ru' }
  assert.deepEqual(saveCheck(on, base, { canEdit: true }), { dirty: true, canSave: true, reason: '', confirmOff: false })
  assert.equal(saveCheck(on, base).canSave, false)
  assert.match(saveCheck(on, base).reason, /settings\.write/)
  assert.equal(saveCheck(formFrom(base), base, { canEdit: true }).canSave, false)
  assert.match(saveCheck(on, { ...base, run: { status: 'running' } }, { canEdit: true }).reason, /Дождитесь/)
  assert.match(saveCheck({ enabled: true, host: 'http://x.ru' }, base, { canEdit: true }).reason, /без https/)
  const off = saveCheck({ enabled: false, host: 'tg.hiearth.ru' }, base, { canEdit: true })
  assert.equal(off.canSave, true)
  assert.equal(off.confirmOff, true)
  assert.equal(saveCheck({ enabled: true, host: '' }, { ...base, enabled: false }, { canEdit: true }).confirmOff, false)
})

test('saveMessage: off, on, a new host while on or off', () => {
  assert.match(saveMessage({ enabled: true }, { enabled: false }), /Выключается/)
  assert.match(saveMessage({ enabled: false }, { enabled: true }), /запущена установка/)
  assert.match(saveMessage({ enabled: true }, { enabled: true }), /новом адресе/)
  assert.match(saveMessage({ enabled: false }, { enabled: false }), /при включении/)
})

test('numbers, bytes, days in Russian', () => {
  assert.equal(fmtInt(null), '—')
  assert.equal(fmtInt(0), '0')
  assert.equal(fmtInt(1234567), `1${NB}234${NB}567`)
  assert.equal(humanBytes(undefined), '—')
  assert.equal(humanBytes(0), `0${NB}Б`)
  assert.equal(humanBytes(512), `512${NB}Б`)
  assert.equal(humanBytes(123456), `121${NB}КБ`)
  assert.equal(humanBytes(9876543), `9,4${NB}МБ`)
  assert.equal(humanBytes(5 * 1024 ** 4 * 2048), `10240${NB}ТБ`)
  assert.deepEqual([1, 2, 5, 11, 21, 22, 25, 111].map((n) => plural(n, ['день', 'дня', 'дней'])),
    ['день', 'дня', 'дней', 'дней', 'день', 'дня', 'дней', 'дней'])
  assert.equal(daysText(89), `89${NB}дней`)
})

test('agoText: just now, minutes, hours, else the date; empty and garbage', () => {
  const now = Date.parse('2026-11-16T12:00:00Z')
  assert.equal(agoText('', now), '—')
  assert.equal(agoText('nonsense', now), 'nonsense')
  assert.equal(agoText('2026-11-16T11:59:30Z', now), 'только что')
  assert.equal(agoText('2026-11-16T11:55:00Z', now), '5 мин назад')
  assert.equal(agoText('2026-11-16T09:00:00Z', now), '3 ч назад')
  assert.doesNotMatch(agoText('2026-11-10T09:00:00Z', now), /назад/)
})

test('probe, services, middle-end disconnects', () => {
  assert.deepEqual(probeView(true), { text: 'ok', tone: 'success' })
  assert.deepEqual(probeView(false), { text: 'нет', tone: 'danger' })
  assert.equal(probeView(undefined).text, '—')
  const list = servicesList({ 'heyearth-tgfwd': 'active', 'tproxy-server': 'failed', 'zz.unit': 'inactive' }, true)
  assert.deepEqual(list.map((s) => s.unit), ['tproxy-server', 'heyearth-tgfwd', 'zz.unit'])
  assert.deepEqual(list.map((s) => s.tone), ['danger', 'success', 'warning'])
  assert.equal(servicesList({ 'heyearth-tgfwd': 'inactive' }, false)[1].tone, 'default')
  assert.equal(servicesList(null, true).length, 2)
  assert.deepEqual(disconnectsView(null), { text: '—', tone: 'default' })
  assert.deepEqual(disconnectsView(0), { text: '0', tone: 'default' })
  assert.equal(disconnectsView(4).tone, 'warning')
})

test('certView: none, expired, soon, fine', () => {
  assert.equal(certView(null).text, '—')
  assert.deepEqual(certView({ present: false, error: 'no A record' }), { text: 'нет', tone: 'warning', detail: 'no A record' })
  assert.equal(certView({ present: true, days_left: 0 }).tone, 'danger')
  assert.equal(certView({ present: true, days_left: -3 }).text, 'истёк')
  assert.equal(certView({ present: true, days_left: 13 }).tone, 'warning')
  assert.equal(certView({ present: true, days_left: 14 }).tone, 'success')
  assert.match(certView({ present: true, days_left: 89 }).text, /89.дней до конца/)
})

test('dnsView: missing and extra A records, the problem text only when not ok', () => {
  assert.equal(dnsView(null), null)
  const v = dnsView({ a: ['203.0.113.10', '192.0.2.1'], aaaa: ['2001:db8::1'], expected: ['203.0.113.10', '203.0.113.11'], ok: false, problem: 'Добавьте A 203.0.113.11' })
  assert.deepEqual(v.missing, ['203.0.113.11'])
  assert.deepEqual(v.extra, ['192.0.2.1'])
  assert.equal(v.problem, 'Добавьте A 203.0.113.11')
  assert.match(dnsView({ a: [], expected: ['1.2.3.4'], ok: false }).problem, /не совпадают/)
  assert.equal(dnsView({ a: ['1.2.3.4'], expected: ['1.2.3.4'], ok: true, problem: 'stale' }).problem, '')
})

test('runView and stepView: known kinds and statuses, unknown ones as warning', () => {
  assert.equal(runView(null), null)
  assert.deepEqual(runView({ kind: 'install', status: 'running' }), { kind: 'Установка', label: 'Выполняется', variant: 'info' })
  assert.deepEqual(runView({ kind: 'disable', status: 'succeeded' }), { kind: 'Выключение', label: 'Готово', variant: 'success' })
  assert.equal(runView({ kind: 'install', status: 'failed' }).variant, 'danger')
  assert.equal(runView({ kind: 'x', status: 'weird' }).variant, 'warning')
  for (const [st, variant] of [['pending', 'default'], ['running', 'info'], ['ok', 'success'], ['done', 'success'], ['skipped', 'default'], ['failed', 'danger'], ['rolled_back', 'danger']]) {
    assert.equal(stepView(st).variant, variant, st)
  }
  assert.deepEqual(stepView('odd'), { label: 'odd', variant: 'warning' })
})

test('autoOpenStep: the first problem, else the running step, else none', () => {
  const steps = [{ name: 'tg_mtproxy', status: 'done' }, { name: 'tg_relay', status: 'running' }, { name: 'tg_nginx', status: 'pending' }]
  assert.equal(autoOpenStep({ steps }), 'tg_relay')
  assert.equal(autoOpenStep({ steps: [...steps.slice(0, 2), { name: 'tg_cert', status: 'failed' }] }), 'tg_cert')
  assert.equal(autoOpenStep({ steps: [{ status: 'rolled_back' }] }), 'step-0')
  assert.equal(autoOpenStep({ steps: [{ name: 'a', status: 'done' }] }), null)
  assert.equal(autoOpenStep(null), null)
  assert.equal(stepKey({}, 2), 'step-2')
})

test('exitsView: installed and reachable exits, by path', () => {
  assert.equal(exitsView(null), null)
  const v = exitsView([
    { id: 2, name: 'NL', installed: true, reachable: true, path: 'stls' },
    { id: 3, installed: true, reachable: false },
    { id: 4, installed: false },
  ])
  assert.equal(v.summary, '1 из 3')
  assert.equal(v.tone, 'warning')
  assert.match(v.rows[0].text, /ShadowTLS/)
  assert.equal(v.rows[1].tone, 'danger')
  assert.equal(v.rows[2].tone, 'warning')
  assert.equal(exitsView([{ id: 3, installed: true, reachable: false }]).tone, 'danger')
})
