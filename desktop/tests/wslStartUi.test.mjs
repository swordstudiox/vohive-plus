import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('desktop runtime service exposes explicit WSL startup command', () => {
  const source = readFileSync(new URL('../src/services/runtime.ts', import.meta.url), 'utf8')

  assert.match(source, /startWsl\(\)/)
  assert.match(source, /invoke<ActionResult>\('start_wsl'\)/)
})

test('desktop runtime service exposes explicit WSL stop command', () => {
  const source = readFileSync(new URL('../src/services/runtime.ts', import.meta.url), 'utf8')

  assert.match(source, /stopWsl\(\)/)
  assert.match(source, /invoke<ActionResult>\('stop_wsl'\)/)
})

test('desktop UI offers a start WSL action in the runtime panel', () => {
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8')

  assert.match(source, /启动 WSL/)
  assert.match(source, /runtimeService\.startWsl/)
})

test('desktop UI offers a stop WSL action in the runtime panel', () => {
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8')

  assert.match(source, /停止 WSL/)
  assert.match(source, /runtimeService\.stopWsl/)
})

test('desktop title displays the Tauri application version', () => {
  const source = readFileSync(new URL('../src/App.vue', import.meta.url), 'utf8')

  assert.match(source, /getVersion/)
  assert.match(source, /VoHive Plus v\{\{\s*appVersion\s*\}\}/)
})
