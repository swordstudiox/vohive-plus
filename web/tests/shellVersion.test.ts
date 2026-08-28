import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'

const shell = await readFile(
  new URL('../src/layouts/AuthenticatedShell.vue', import.meta.url),
  'utf8'
)
const settings = await readFile(
  new URL('../src/views/Settings.vue', import.meta.url),
  'utf8'
)
const versionUtil = await readFile(
  new URL('../src/utils/version.ts', import.meta.url),
  'utf8'
)
const viteConfig = await readFile(
  new URL('../vite.config.ts', import.meta.url),
  'utf8'
)

test('authenticated shell displays backend version next to the VoHive brand', () => {
  assert.match(shell, /__VOHIVE_APP_VERSION__/)
  assert.match(shell, /systemService\.getInfo/)
  assert.match(shell, /backendVersion/)
  assert.match(shell, /if\s*\(\s*info\.ok\s*\)/)
  assert.match(shell, /info\.data\.version/)
  assert.match(shell, /VoHive v\{\{\s*backendVersion\s*\}\}/)
  assert.doesNotMatch(shell, /'Unknown'/)
  assert.doesNotMatch(shell, /'1\.0\.3'/)
})

test('settings page also falls back to the app version instead of Unknown', () => {
  assert.match(settings, /__VOHIVE_APP_VERSION__/)
  assert.match(settings, /normalizeDisplayVersion\(systemInfo\.version,\s*appVersion\)/)
  assert.match(versionUtil, /version\.toLowerCase\(\)\s*===\s*'unknown'/)
  assert.doesNotMatch(settings, /'Unknown'/)
  assert.doesNotMatch(settings, /'1\.0\.3'/)
  assert.doesNotMatch(versionUtil, /return\s+fallbackVersion\s*\/\/.*Unknown/)
})

test('web build injects the shared application version', () => {
  assert.match(viteConfig, /desktop\/package\.json/)
  assert.match(viteConfig, /__VOHIVE_APP_VERSION__/)
})
