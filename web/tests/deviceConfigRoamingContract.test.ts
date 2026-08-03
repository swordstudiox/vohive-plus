import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const apiTypes = await readFile(
  new URL('../src/types/api.ts', import.meta.url),
  'utf8'
)

test('DeviceConfigDTO does not expose roaming_enabled because roaming is card policy state', () => {
  const start = apiTypes.indexOf('export type DeviceConfigDTO')
  assert.ok(start >= 0, 'DeviceConfigDTO not found')
  const end = apiTypes.indexOf('export type PNNRecord', start)
  const block = apiTypes.slice(start, end)
  assert.doesNotMatch(block, /roaming_enabled/)
})
