import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('tauri config disables installer bundling while keeping runtime resources', () => {
  const config = JSON.parse(
    readFileSync(new URL('../src-tauri/tauri.conf.json', import.meta.url), 'utf8')
  )

  assert.equal(config.bundle.active, false)
  assert.deepEqual(config.bundle.resources, ['resources/vohive/*'])
  assert.ok(!JSON.stringify(config.bundle).includes('nsis'))
})
