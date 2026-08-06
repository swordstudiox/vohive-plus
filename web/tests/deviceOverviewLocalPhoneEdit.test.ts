import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const overviewTab = await readFile(
  new URL('../src/components/DeviceOverviewTab.vue', import.meta.url),
  'utf8'
)
const devicesService = await readFile(
  new URL('../src/services/devices.ts', import.meta.url),
  'utf8'
)
const apiTypes = await readFile(
  new URL('../src/types/api.ts', import.meta.url),
  'utf8'
)

test('device overview exposes local phone edit action that refreshes after save', () => {
  assert.match(overviewTab, /ElMessageBox/)
  assert.match(overviewTab, /devicesService\.setLocalPhone\(deviceId, phoneNumber\)/)
  assert.match(overviewTab, /emit\('refresh'\)/)
  assert.match(overviewTab, /编辑本机号码/)
})

test('devices service exposes local phone update endpoint contract', () => {
  assert.match(devicesService, /setLocalPhone\(id: string, phoneNumber: string\)/)
  assert.match(devicesService, /api\.patch<SetLocalPhoneResponse>\(`\/devices\/\$\{id\}\/local-phone`, \{ phone_number: phoneNumber \}\)/)
})

test('api types describe local phone update request and response', () => {
  assert.match(apiTypes, /export type SetLocalPhoneRequest = \{\s+phone_number: string\s+\}/)
  assert.match(apiTypes, /export type SetLocalPhoneResponse = \{\s+local_phone\?: string\s+\}/)
})
