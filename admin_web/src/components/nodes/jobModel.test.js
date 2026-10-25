import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  POLL_MS, stepView, stepTitle, autoOpenStep, openStep, jobHeadline, jobProgress, pathVerdict, nextPollDelay,
} from './jobModel.js'

const step = (idx, name, status, extra = {}) => ({ idx, name, title: '', status, output_tail: '', ...extra })

test('step statuses read as words; the main path skipped is a warning, not «пропущено»', () => {
  assert.deepEqual(stepView(step(0, 'base', 'done')), { label: 'сделано', variant: 'success' })
  assert.equal(stepView(step(0, 'base', 'failed')).variant, 'danger')
  assert.equal(stepView(step(0, 'base', 'rolled_back')).variant, 'danger')
  assert.equal(stepView(step(0, 'base', 'interrupted')).variant, 'warning')
  const hy = stepView(step(11, 'path_hy', 'skipped'))
  assert.equal(hy.variant, 'warning')
  assert.equal(hy.label, 'не отвечает')
  assert.match(hy.hint, /основной путь не отвечает/i)
  // any other skipped step (MTProxy switched off) stays neutral
  assert.equal(stepView(step(7, 'mtproxy', 'skipped')).variant, 'default')
})

test('unknown and built-in names are shown as they are, never as success', () => {
  assert.equal(stepView(step(0, 'x', 'weird')).label, 'weird')
  assert.equal(stepView(step(0, 'x', 'weird')).variant, 'warning')
  assert.equal(stepView(step(0, 'x', 'constructor')).variant, 'warning')
  assert.equal(stepView(step(0, 'x', '')).label, '—')
  stepView(step(0, 'x', 'done')).label = 'broken'
  assert.equal(stepView(step(0, 'x', 'done')).label, 'сделано', 'shared views are not handed out')
})

test('step titles: the server’s Russian title, else the name, else the number', () => {
  assert.equal(stepTitle(step(0, 'path_hy', 'ok', { title: 'Проверка пути: основной туннель' })), 'Проверка пути: основной туннель')
  assert.equal(stepTitle(step(2, 'new_step', 'ok')), 'new_step')
  assert.equal(stepTitle({ idx: 4 }), 'Шаг 5')
})

test('the failed step opens by itself; while running, the running step', () => {
  const job = { status: 'failed', steps: [step(0, 'a', 'done'), step(1, 'b', 'failed'), step(2, 'c', 'pending')] }
  assert.equal(autoOpenStep(job), 1)
  assert.equal(autoOpenStep({ status: 'running', steps: [step(0, 'a', 'done'), step(1, 'b', 'running')] }), 1)
  assert.equal(autoOpenStep({ status: 'interrupted', steps: [step(0, 'a', 'done'), step(1, 'b', 'interrupted')] }), 1)
  assert.equal(autoOpenStep({ status: 'succeeded', steps: [step(0, 'a', 'done')] }), null)
  assert.equal(autoOpenStep({ status: 'succeeded', steps: [step(0, 'a', 'drift'), step(1, 'b', 'ok')] }), 0)
  assert.equal(autoOpenStep(null), null)
})

test('the owner’s own choice of an open step wins over the automatic one', () => {
  const job = { status: 'failed', steps: [step(0, 'a', 'done'), step(1, 'b', 'failed')] }
  assert.equal(openStep(undefined, job), 1)
  assert.equal(openStep(0, job), 0)
  assert.equal(openStep(null, job), null, 'collapsed by hand stays collapsed')
})

test('headline: running, done, failed, interrupted — and a verify with drift is not plain «Готово»', () => {
  assert.equal(jobHeadline({ status: 'running', kind: 'provision' }).variant, 'info')
  assert.equal(jobHeadline({ status: 'running', kind: 'provision' }).label, 'Идёт настройка')
  assert.equal(jobHeadline({ status: 'queued', kind: 'verify' }).label, 'Идёт проверка')
  assert.deepEqual(jobHeadline({ status: 'succeeded', kind: 'provision', steps: [step(0, 'a', 'done')] }), { label: 'Готово', variant: 'success' })
  const drift = jobHeadline({ status: 'succeeded', kind: 'verify', steps: [step(0, 'a', 'ok'), step(1, 'b', 'drift')] })
  assert.equal(drift.variant, 'warning')
  assert.equal(jobHeadline({ status: 'failed', kind: 'provision' }).variant, 'danger')
  const intr = jobHeadline({ status: 'interrupted', kind: 'provision' })
  assert.equal(intr.variant, 'warning')
  assert.match(intr.hint, /Продолжить/)
  assert.match(jobHeadline({ status: 'odd', kind: 'provision' }).label, /odd/)
})

test('progress counts finished steps of the run', () => {
  const job = { steps: [step(0, 'a', 'ok'), step(1, 'b', 'done'), step(2, 'c', 'skipped'), step(3, 'd', 'running'), step(4, 'e', 'pending')] }
  assert.deepEqual(jobProgress(job), { done: 3, total: 5 })
  assert.deepEqual(jobProgress(null), { done: 0, total: 0 })
})

const pathJob = (hy, tcp, status = 'succeeded') => ({ status, steps: [step(0, 'base', 'done'), step(1, 'path_hy', hy), step(2, 'path_tcp', tcp)] })

test('final path check: both paths, the TCP fallback only, nothing', () => {
  const both = pathVerdict(pathJob('done', 'done'))
  assert.equal(both.variant, 'success')
  assert.equal(both.hy.variant, 'success')
  const fallback = pathVerdict(pathJob('skipped', 'done'))
  assert.equal(fallback.variant, 'warning')
  assert.match(fallback.text, /запасному TCP/)
  assert.equal(fallback.hy.variant, 'warning')
  const none = pathVerdict(pathJob('skipped', 'failed', 'failed'))
  assert.equal(none.variant, 'danger')
  const tcpDown = pathVerdict(pathJob('done', 'failed', 'failed'))
  assert.equal(tcpDown.variant, 'warning')
})

test('final path check: a switched-off TCP path is «выключен», not «работает»', () => {
  const v = pathVerdict(pathJob('done', 'done'), { tcpEnabled: false })
  assert.equal(v.tcp.label, 'выключен')
  assert.equal(v.variant, 'success')
  assert.match(v.text, /выключен/)
  assert.equal(pathVerdict(pathJob('failed', 'done', 'failed'), { tcpEnabled: false }).variant, 'danger')
})

test('final path check: only for finished jobs that have path steps; stopped earlier is said so', () => {
  assert.equal(pathVerdict(pathJob('pending', 'pending', 'running')), null)
  assert.equal(pathVerdict({ status: 'succeeded', steps: [step(0, 'exclude', 'done')] }), null, 'deprovision has no paths')
  const early = pathVerdict(pathJob('pending', 'pending', 'failed'))
  assert.equal(early.variant, 'default')
  assert.match(early.text, /не проверялись/)
  // verify job: drift on the main path is a dead path
  assert.equal(pathVerdict(pathJob('drift', 'ok')).hy.variant, 'danger')
})

test('polling: every 2 s, backing off on errors, stopping on 401/403/404', () => {
  assert.equal(POLL_MS, 2000)
  assert.equal(nextPollDelay(0), 2000)
  assert.equal(nextPollDelay(1, 502), 4000)
  assert.equal(nextPollDelay(2, 0), 8000)
  assert.equal(nextPollDelay(10, 500), 15000)
  assert.equal(nextPollDelay(1, 404), null)
  assert.equal(nextPollDelay(1, 403), null)
  assert.equal(nextPollDelay(1, 401), null)
})
