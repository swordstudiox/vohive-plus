import { api } from '../stores/auth'
import { callService } from './http'
import type { CarrierWebsheetInfo, DeviceConfigDTO, DiscoveredDevice, EsimNotificationItem, EsimOverviewResponse, EsimSpaceDelta, PrepareUSBResponse } from '../types/api'
import type { DeviceDetailVM, DeviceListVM } from '../types/view-model'
import axios from 'axios'

type UpdateConfigResponse = {
  requires_restart?: boolean
  warning?: string
}

type AddDeviceResponse = {
  warning?: string
  started?: boolean
}

type DeleteEsimProfileResponse = {
  warning?: string
  warning_code?: string
  space_delta?: EsimSpaceDelta
}

type NetworkControlResponse = {
  network_connected?: boolean
  private_ip?: string
  public_ip?: string
}

type FlightModeResponse = {
  operating_mode?: number
  flight_mode?: boolean
  message?: string
}

type AtCommandResponse = {
  ok?: boolean
  response?: string
  result?: string
}

type UssdResult = {
  status?: number
  text?: string
  raw_text?: string
  raw_xml?: string
  dcs?: number
  session_id?: string
}

type UssdResponse = {
  result?: UssdResult
  channel?: string
}

const ESIM_BUSY_CODE = 'ESIM_BUSY'
const ESIM_BUSY_RETRY_DELAYS = [300, 600, 1200]

type EsimBusyErrorMeta = {
  busy: boolean
  retryAfterMs: number
}

function sleep(ms: number) {
  return new Promise<void>((resolve) => {
    window.setTimeout(resolve, ms)
  })
}

function parseEsimBusyError(err: unknown): EsimBusyErrorMeta {
  if (!axios.isAxiosError(err)) return { busy: false, retryAfterMs: 0 }
  if (err.response?.status !== 409) return { busy: false, retryAfterMs: 0 }
  const data = err.response?.data
  if (!data || typeof data !== 'object') return { busy: false, retryAfterMs: 0 }
  const payload = data as Record<string, unknown>
  const code = typeof payload.code === 'string' ? payload.code : ''
  const busy = payload.busy === true || code === ESIM_BUSY_CODE
  if (!busy) return { busy: false, retryAfterMs: 0 }
  const retryAfterMs = typeof payload.retryAfterMs === 'number' && payload.retryAfterMs > 0
    ? Math.floor(payload.retryAfterMs)
    : 0
  return { busy: true, retryAfterMs }
}

async function withEsimBusyRetry<T>(fn: () => Promise<T>) {
  let lastErr: unknown
  for (let attempt = 0; attempt <= ESIM_BUSY_RETRY_DELAYS.length; attempt++) {
    try {
      return await fn()
    } catch (err) {
      const busy = parseEsimBusyError(err)
      if (!busy.busy || attempt === ESIM_BUSY_RETRY_DELAYS.length) {
        throw err
      }
      lastErr = err
      const fallbackDelay = ESIM_BUSY_RETRY_DELAYS[attempt]
      const delay = busy.retryAfterMs > 0 ? busy.retryAfterMs : fallbackDelay
      await sleep(delay)
    }
  }
  throw lastErr instanceof Error ? lastErr : new Error('eSIM operation failed after retries')
}

export const devicesService = {
  listManaged(signal?: AbortSignal) {
    return callService(async () => {
      const res = await api.get('/devices', { signal })
      return {
        devices: (res.data?.devices || []) as DeviceListVM[],
        deviceLimit: (typeof res.data?.device_limit === 'number' ? res.data.device_limit : 0) as number,
      }
    })
  },
  getOverviewLite(id: string, signal?: AbortSignal) {
    return callService(async () => {
      const res = await api.get(`/devices/${id}/overview`, { signal })
      return ((res.data?.devices || [])[0] || null) as DeviceDetailVM | null
    })
  },
  getConfig(id: string) {
    return callService(async () => {
      const res = await api.get(`/devices/${id}/config`)
      return (res?.data?.config || null) as DeviceConfigDTO | null
    })
  },
  prepareUSB() {
    return callService(async () => {
      const res = await api.post<PrepareUSBResponse>('/devices/actions/prepare-usb')
      const data = res.data
      if (!data?.prepared) {
        throw new Error(data?.message || 'WSL USB 尚未准备完成')
      }
      return data
    })
  },
  listDiscovered() {
    return callService(async () => {
      const prepared = await devicesService.prepareUSB()
      if (!prepared.ok) throw new Error(prepared.error.message || 'WSL USB 准备失败')
      const res = await api.get('/devices/discovered', { params: { with_imei: 1 } })
      return (res.data?.devices || []) as DiscoveredDevice[]
    })
  },
  refreshInfo(id: string) {
    return callService(async () => {
      await api.post(`/devices/${id}/actions/refresh`)
      return true
    })
  },
  setFlightMode(id: string, flightModeEnabled: boolean) {
    return callService(async () => {
      const res = await api.patch<FlightModeResponse>(`/devices/${id}/flight-mode`, { enabled: flightModeEnabled })
      return {
        operatingMode: res.data?.operating_mode,
        flightMode: res.data?.flight_mode === true,
        message: res.data?.message || ''
      }
    })
  },
  setRoaming(id: string, roamingEnabled: boolean) {
    return callService(async () => {
      await api.patch(`/devices/${id}/roaming`, { enabled: roamingEnabled })
      return true
    })
  },
  rotateIP(id: string) {
    return callService(async () => {
      await api.post('/rotateip', { device_id: id })
      return true
    })
  },
  startNetwork(id: string, opts?: { ip_version?: string; apn?: string }) {
    return callService(async () => {
      const res = await api.patch<NetworkControlResponse>(`/devices/${id}/network`, {
        enabled: true,
        ip_version: opts?.ip_version,
        apn: opts?.apn
      })
      return {
        networkConnected: res.data?.network_connected === true,
        privateIP: res.data?.private_ip || '',
        publicIP: res.data?.public_ip || ''
      }
    })
  },
  stopNetwork(id: string) {
    return callService(async () => {
      const res = await api.patch<NetworkControlResponse>(`/devices/${id}/network`, { enabled: false })
      return {
        networkConnected: res.data?.network_connected === true,
        privateIP: res.data?.private_ip || '',
        publicIP: res.data?.public_ip || ''
      }
    })
  },
  enableVoWiFi(id: string) {
    return callService(async () => {
      await api.patch(`/devices/${id}/vowifi`, { enabled: true })
      return true
    })
  },
  disableVoWiFi(id: string) {
    return callService(async () => {
      await api.patch(`/devices/${id}/vowifi`, { enabled: false })
      return true
    })
  },
  reconnectVoWiFi(id: string) {
    return callService(async () => {
      await api.post(`/devices/${id}/vowifi/actions/reconnect`)
      return true
    })
  },
  startE911Websheet(id: string) {
    return callService(async () => {
      const res = await api.post<CarrierWebsheetInfo>(`/devices/${id}/vowifi/e911/websheet`)
      return res.data
    })
  },
  rebootModem(id: string) {
    return callService(async () => {
      await api.post(`/devices/${id}/actions/reboot`)
      return true
    })
  },
  rescanAll() {
    return callService(async () => {
      const prepared = await devicesService.prepareUSB()
      if (!prepared.ok) throw new Error(prepared.error.message || 'WSL USB 准备失败')
      await api.post('/devices/actions/rescan')
      return true
    })
  },
  updateConfig(id: string, config: DeviceConfigDTO) {
    return callService(async () => {
      const res = await api.put<UpdateConfigResponse>(`/devices/${id}`, { config })
      return {
        requiresRestart: !!res.data?.requires_restart,
        warning: typeof res.data?.warning === 'string' ? res.data.warning : ''
      }
    })
  },
  deleteManaged(id: string) {
    return callService(async () => {
      await api.delete(`/devices/${id}`)
      return true
    })
  },
  setUSBNetMode(id: string, mode: number) {
    return callService(async () => {
      await api.patch(`/devices/${id}/usbnet-mode`, { mode })
      return true
    })
  },
  fixDiscoveredUSBNet(atPort: string) {
    return callService(async () => {
      await api.post('/device-mgmt/discovered/fix-usbnet', { at_port: atPort })
      return true
    })
  },
  addManaged(config: DeviceConfigDTO) {
    return callService(async () => {
      const {
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        apn: _apn,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        ip_version: _ipVersion,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        network_enabled: _networkEnabled,
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        vowifi_enabled: _voWiFiEnabled,
        ...deviceConfig
      } = config
      const res = await api.post<AddDeviceResponse>('/devices', { config: deviceConfig })
      return {
        warning: typeof res.data?.warning === 'string' ? res.data.warning : '',
        started: res.data?.started === true
      }
    })
  },
  sendAT(id: string, payload: { cmd: string; timeout_ms: number }) {
    return callService(async () => {
      const res = await api.post<AtCommandResponse>(`/devices/${id}/actions/at`, payload)
      return {
        ok: res.data?.ok ?? true,
        response: res.data?.response ?? res.data?.result ?? JSON.stringify(res.data || {})
      }
    })
  },
  sendUSSD(id: string, payload: { command: string; timeout_ms: number }, timeoutMs: number) {
    return callService(async () => {
      const res = await api.post<UssdResponse>(`/devices/${id}/actions/ussd`, payload, {
        timeout: timeoutMs
      })
      const result = res.data?.result || {}
      return {
        status: result.status,
        text: result.text || '',
        rawText: result.raw_text || result.raw_xml || '',
        dcs: result.dcs,
        sessionId: result.session_id || '',
        channel: res.data?.channel || ''
      }
    })
  },
  continueUSSD(id: string, payload: { session_id: string; input: string; timeout_ms: number }, timeoutMs: number) {
    return callService(async () => {
      const res = await api.post<UssdResponse>(`/devices/${id}/actions/ussd/continue`, payload, {
        timeout: timeoutMs
      })
      const result = res.data?.result || {}
      return {
        status: result.status,
        text: result.text || '',
        rawText: result.raw_text || result.raw_xml || '',
        dcs: result.dcs,
        sessionId: result.session_id || '',
        channel: res.data?.channel || ''
      }
    })
  },
  cancelUSSD(id: string, sessionId: string) {
    return callService(async () => {
      await api.post(`/devices/${id}/actions/ussd/cancel`, { session_id: sessionId })
      return true
    })
  },
  getEsimOverview(id: string, options?: { refresh?: boolean; signal?: AbortSignal }) {
    return callService(async () => {
      const res = await withEsimBusyRetry(() => api.get<EsimOverviewResponse>(`/devices/${id}/esim`, {
        signal: options?.signal,
        params: options?.refresh ? { refresh: true } : undefined
      }))
      return {
        chipInfo: res.data?.chip_info || null,
        profiles: res.data?.profiles || []
      }
    })
  },
  getEsimProfiles(id: string, options?: { refresh?: boolean; signal?: AbortSignal }) {
    return callService(async () => {
      const res = await withEsimBusyRetry(() => api.get(`/devices/${id}/esim/profiles`, {
        signal: options?.signal,
        params: options?.refresh ? { refresh: true } : undefined
      }))
      return (res.data || [])
    })
  },
  getEsimNotifications(id: string, options?: { aidHex?: string; signal?: AbortSignal }) {
    return callService(async () => {
      const res = await withEsimBusyRetry(() => api.get<{ items?: EsimNotificationItem[] }>(`/devices/${id}/esim/notifications`, {
        signal: options?.signal,
        params: options?.aidHex ? { aid_hex: options.aidHex } : undefined
      }))
      return res.data?.items || []
    })
  },
  retryEsimNotification(id: string, sequenceNumber: number, aidHex?: string) {
    return callService(async () => {
      const res = await withEsimBusyRetry(() => api.post<{ status?: string; message?: string }>(`/devices/${id}/esim/notifications/${sequenceNumber}/actions/retry`, null, {
        params: aidHex ? { aid_hex: aidHex } : undefined
      }))
      return {
        status: typeof res.data?.status === 'string' ? res.data.status : 'ok',
        message: typeof res.data?.message === 'string' ? res.data.message : '通知重试发送成功'
      }
    })
  },
  switchEsimProfile(id: string, payload: { iccid: string; aid_hex: string }) {
    return callService(async () => {
      await withEsimBusyRetry(() => api.post(`/devices/${id}/esim/actions/switch`, payload))
      return true
    })
  },
  renameEsimProfile(id: string, iccid: string, payload: { name: string; aid_hex: string }) {
    return callService(async () => {
      await withEsimBusyRetry(() => api.patch(`/devices/${id}/esim/profiles/${iccid}`, payload))
      return true
    })
  },
  deleteEsimProfile(id: string, iccid: string, aidHex: string) {
    return callService(async () => {
      const res = await withEsimBusyRetry(() => api.delete<DeleteEsimProfileResponse>(`/devices/${id}/esim/profiles/${iccid}`, {
        params: { aid_hex: aidHex }
      }))
      return {
        warning: typeof res.data?.warning === 'string' ? res.data.warning : '',
        warningCode: typeof res.data?.warning_code === 'string' ? res.data.warning_code : '',
        space_delta: res.data?.space_delta
      }
    })
  },
  downloadEsimProfile(id: string, payload: { smdp: string; matching_id?: string; confirmation_code?: string; aid_hex?: string; imei?: string }) {
    return callService(async () => {
      await api.get(`/devices/${id}/esim/actions/download`, { params: payload })
      return true
    })
  },
  scanOperators(id: string, signal?: AbortSignal) {
    return callService(async () => {
      const res = await api.get(`/devices/${id}/operator_selection/scan`, { signal })
      return res.data as import('../types/api').OperatorScanResult
    })
  },
  getOperatorSelection(id: string) {
    return callService(async () => {
      const res = await api.get(`/devices/${id}/operator_selection`)
      return res.data as import('../types/api').OperatorSelection
    })
  },
  setOperatorSelection(id: string, payload: import('../types/api').SetOperatorSelectionRequest) {
    return callService(async () => {
      const res = await api.post(`/devices/${id}/operator_selection`, payload)
      return res.data as import('../types/api').OperatorSelection
    })
  }
}
