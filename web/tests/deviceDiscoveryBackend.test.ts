import test from 'node:test'
import assert from 'node:assert/strict'
import { defaultBackendForDiscoveredDevice } from '../src/utils/deviceDiscovery'
import type { DiscoveredDevice } from '../src/types/api'

function discovered(overrides: Partial<DiscoveredDevice>): DiscoveredDevice {
  return {
    discovery_key: 'dev',
    control_path: '',
    net_interface: '',
    usb_path: '',
    vendor_id: 0,
    product_id: 0,
    driver_name: '',
    at_ports: [],
    at_port: '',
    configured: false,
    ...overrides
  }
}

test('DJI Baiwang QMI discovery with AT port defaults to AT backend', () => {
  assert.equal(defaultBackendForDiscoveredDevice(discovered({
    vendor_id: 0x2ca3,
    product_id: 0x4006,
    mode: 'qmi',
    control_path: '/dev/cdc-wdm0',
    net_interface: 'wwan0',
    at_port: '/dev/ttyUSB2',
    at_ports: ['/dev/ttyUSB2', '/dev/ttyUSB3']
  })), 'at')
})

test('generic QMI discovery with control path defaults to QMI backend', () => {
  assert.equal(defaultBackendForDiscoveredDevice(discovered({
    mode: 'qmi',
    control_path: '/dev/cdc-wdm0',
    net_interface: 'wwan0',
    at_port: '/dev/ttyUSB2'
  })), 'qmi')
})

test('MBIM discovery defaults to MBIM backend', () => {
  assert.equal(defaultBackendForDiscoveredDevice(discovered({
    mode: 'mbim',
    control_path: '/dev/cdc-wdm1',
    net_interface: 'wwan1'
  })), 'mbim')
})
