import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'

// Tailwind v4 turns every theme variable into a utility named after it. A variable
// --border-width-default made `border-default` a WIDTH utility (1 px on all four sides),
// while the codebase uses `border-default` / `border-strong` as the hairline COLOUR
// (159 places): every `border-b border-default` drew a box in the text colour.
// Colour utilities for borders read the --border-color-* namespace first.
const css = readFileSync(new URL('./theme.css', import.meta.url), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

function block(selector) {
  const start = css.indexOf(selector)
  assert.notEqual(start, -1, `${selector} block not found`)
  const open = css.indexOf('{', start)
  const close = css.indexOf('}', open)
  return css.slice(open + 1, close)
}

function vars(body) {
  const out = {}
  for (const m of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out[m[1]] = m[2].trim()
  return out
}

test('no theme variable makes border-default or border-strong a width utility', () => {
  for (const sel of ['@theme', ':root[data-theme="brutalist"]', ':root[data-theme="terminal"]']) {
    const v = vars(block(sel))
    assert.equal(v['--border-width-default'], undefined, `${sel} declares --border-width-default`)
    assert.equal(v['--border-width-strong'], undefined, `${sel} declares --border-width-strong`)
  }
})

test('border-default and border-strong are the hairline colour tokens', () => {
  const v = vars(block('@theme'))
  assert.equal(v['--border-color-default'], 'var(--color-border-default)')
  assert.equal(v['--border-color-strong'], 'var(--color-border-strong)')
})

test('no component reads the removed --border-width-default (brutalist would lose its 2 px)', () => {
  const src = new URL('../', import.meta.url)
  const hits = []
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = new URL(e.name + (e.isDirectory() ? '/' : ''), dir)
      if (e.isDirectory()) walk(p)
      else if (/\.jsx?$/.test(e.name) && !e.name.endsWith('.test.js') && readFileSync(p, 'utf8').includes('--border-width-default')) hits.push(e.name)
    }
  }
  walk(src)
  assert.deepEqual(hits, [])
})

test('border width comes from one hairline token, 2 px in the brutalist variant', () => {
  const base = vars(block('@theme'))
  assert.equal(base['--border-width-hairline'], '1px')
  // `border`, `border-b`, `divide-y` … take their default width from --default-border-width.
  assert.equal(base['--default-border-width'], 'var(--border-width-hairline)')
  assert.equal(vars(block(':root[data-theme="brutalist"]'))['--border-width-hairline'], '2px')
})
