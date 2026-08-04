import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(
  new URL('../../.github/workflows/binary-release.yml', import.meta.url),
  'utf8'
)

test('release workflow builds portable desktop zip with runtime resources', () => {
  assert.match(workflow, /version:\s*\$\{\{\s*steps\.vars\.outputs\.version\s*\}\}/)
  assert.match(workflow, /vohive-plus-desktop_\$\{version\}_windows_x64/)
  assert.match(workflow, /desktop-windows-x64/)
  assert.match(workflow, /resources\\vohive/)
  assert.match(workflow, /vohive-open_linux_amd64/)
})

test('release workflow publishes versioned release notes and Linux runtime assets', () => {
  assert.match(workflow, /\.github\/release-notes\/\$\{\{\s*needs\.vars\.outputs\.tag\s*\}\}\.md/)
  assert.match(workflow, /softprops\/action-gh-release@v2/)
  assert.match(workflow, /vohive-plus-firmware_\$\{\{\s*needs\.vars\.outputs\.version\s*\}\}_linux_/)
  assert.ok(!workflow.includes('nsis'))
})
