export type ToolStatus = {
  available: boolean
  path?: string
  version?: string
  message?: string
}

export type UsbDevice = {
  busid: string
  vid_pid: string
  device: string
  state: string
  is_target: boolean
}

export type BackendStatus = {
  running: boolean
  pid?: number
  message?: string
}

export type HealthStatus = {
  ok: boolean
  url: string
  message: string
}

export type RuntimeStatus = {
  route: 'wsl2' | 'virtualbox'
  wsl: ToolStatus
  usbipd: ToolStatus
  devices: UsbDevice[]
  backend: BackendStatus
  health: HealthStatus
}

export type ActionResult = {
  ok: boolean
  message: string
  status?: RuntimeStatus
  suggested_admin_command?: string
}
