import type { DeviceConfigDTO, DiscoveredDevice } from '../types/api'
import { isWwanQmiControlPath } from './deviceBackend'

function hasATPort(d: DiscoveredDevice): boolean {
  return !!String(d.at_port || '').trim() || (Array.isArray(d.at_ports) && d.at_ports.length > 0)
}

export function isDjiBaiwangDiscovery(d: DiscoveredDevice): boolean {
  return Number(d.vendor_id) === 0x2ca3 && Number(d.product_id) === 0x4006
}

export function defaultBackendForDiscoveredDevice(d: DiscoveredDevice): DeviceConfigDTO['device_backend'] {
  const mode = String(d.mode || '').toLowerCase()
  if (mode === 'mbim') {
    return 'mbim'
  }
  if (mode === 'qmi' && isDjiBaiwangDiscovery(d) && hasATPort(d)) {
    return 'at'
  }
  if (isWwanQmiControlPath(d.control_path) || (mode === 'qmi' && d.control_path)) {
    return 'qmi'
  }
  return 'at'
}
