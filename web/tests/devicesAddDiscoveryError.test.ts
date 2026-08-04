import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const source = await readFile(
  new URL('../src/views/Devices.vue', import.meta.url),
  'utf8'
)

test('add-device discovery surfaces prepare or discovery errors', () => {
  const fnStart = source.indexOf('async function refreshDiscoveredForAdd()')
  assert.ok(fnStart >= 0, 'refreshDiscoveredForAdd() not found')
  const fnBody = source.slice(fnStart, source.indexOf('function applyDiscoveredToAddConfig', fnStart))
  assert.match(fnBody, /else\s*\{[\s\S]*ElMessage\.(?:warning|error)\(result\.error\.message/)
})

test('add-device save preserves backend selected in dialog', () => {
  const fnStart = source.indexOf('async function addDevice()')
  assert.ok(fnStart >= 0, 'addDevice() not found')
  const fnBody = source.slice(fnStart, source.indexOf('function refreshCurrentDeviceTrafficAnalysis', fnStart))
  assert.doesNotMatch(
    fnBody,
    /applyDiscoveredToAddConfig\(addSelected\.value\)/,
    'saving must not reset the user-selected backend to the discovery default'
  )
  assert.match(fnBody, /applyDiscoveredToAddConfig\(addSelected\.value,\s*\{\s*preserveBackend:\s*true\s*\}\)/)
})
