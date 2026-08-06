import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'

const shell = await readFile(
  new URL('../src/layouts/AuthenticatedShell.vue', import.meta.url),
  'utf8'
)

test('authenticated shell displays backend version next to the VoHive brand', () => {
  assert.match(shell, /systemService\.getInfo/)
  assert.match(shell, /backendVersion/)
  assert.match(shell, /if\s*\(\s*info\.ok\s*\)/)
  assert.match(shell, /info\.data\.version/)
  assert.match(shell, /VoHive v\{\{\s*backendVersion\s*\}\}/)
})
