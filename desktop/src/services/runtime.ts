import { invoke } from '@tauri-apps/api/core'
import type { ActionResult, RuntimeStatus } from '../types/runtime'

export const runtimeService = {
  detect() {
    return invoke<RuntimeStatus>('detect')
  },
  attachUsb() {
    return invoke<ActionResult>('attach_usb')
  },
  prepareUsb() {
    return invoke<ActionResult>('prepare_usb')
  },
  start() {
    return invoke<ActionResult>('start_backend')
  },
  stop() {
    return invoke<ActionResult>('stop_backend')
  },
  status() {
    return invoke<RuntimeStatus>('status')
  },
  logs() {
    return invoke<string[]>('logs')
  },
  openWeb() {
    return invoke<ActionResult>('open_web')
  }
}
