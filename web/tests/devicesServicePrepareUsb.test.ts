import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const source = await readFile(
  new URL('../src/services/devices.ts', import.meta.url),
  'utf8'
)

test('devices service exposes prepareUSB API call', () => {
  assert.match(source, /PrepareUSBResponse/)
  assert.match(source, /prepareUSB\(\)\s*\{\s*return callService\(async \(\) => \{\s*const res = await api\.post<PrepareUSBResponse>\('\/devices\/actions\/prepare-usb'\)/s)
  assert.match(source, /if \(!data\?\.prepared\)/)
})

test('listDiscovered prepares WSL USB before discovery request', () => {
  const fnStart = source.indexOf('listDiscovered()')
  assert.ok(fnStart >= 0, 'listDiscovered() not found')
  const fnBody = source.slice(fnStart, source.indexOf('refreshInfo(id:', fnStart))
  assert.ok(fnBody.indexOf('await devicesService.prepareUSB()') >= 0, 'prepareUSB call missing')
  assert.ok(
    fnBody.indexOf('await devicesService.prepareUSB()') < fnBody.indexOf("api.get('/devices/discovered'"),
    'prepareUSB should run before /devices/discovered'
  )
})

test('rescanAll prepares WSL USB before rescan request', () => {
  const fnStart = source.indexOf('rescanAll()')
  assert.ok(fnStart >= 0, 'rescanAll() not found')
  const fnBody = source.slice(fnStart, source.indexOf('updateConfig(id:', fnStart))
  assert.ok(fnBody.indexOf('await devicesService.prepareUSB()') >= 0, 'prepareUSB call missing')
  assert.ok(
    fnBody.indexOf('await devicesService.prepareUSB()') < fnBody.indexOf("api.post('/devices/actions/rescan'"),
    'prepareUSB should run before /devices/actions/rescan'
  )
})
