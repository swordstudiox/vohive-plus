export type DeviceTrafficFormatted = {
  tx?: string
  rx?: string
  rate?: string
  connections?: string
  active_conns?: string
}

export type DeviceTrafficRaw = {
  bytes_sent?: number
  bytes_received?: number
  connections?: number
  active_conns?: number
}

export type DeviceTrafficMeta = {
  interface?: string
  period_start?: string
  age_seconds?: number
  status?: 'ok' | 'waiting_sample' | 'stale'
}

export type RealtimeTrafficSnapshot = {
  device_id: string
  interface?: string
  source: 'qmi_wds'
  timestamp: string
  interval_ms: number
  rx_bps: number
  tx_bps: number
  rx_delta_bytes: number
  tx_delta_bytes: number
  total_rx_bytes: number
  total_tx_bytes: number
  status: 'ok' | 'waiting_sample' | 'reset' | 'error'
  error?: string
}

export type VoWiFiRuntimeState = {
  device_id?: string
  dataplane_mode?: string
  sim_ready?: boolean
  access_ready?: boolean
  tunnel_ready?: boolean
  ims_ready?: boolean
  sms_ready?: boolean
  reg_status?: number
  reg_status_text?: string
  network_mode?: string
  last_error_class?: string
  last_error?: string
  last_reason?: string
  updated_at?: string
}

export type DeviceLifecyclePhase =
  | 'offline'
  | 'rebooting'
  | 'usb_wait'
  | 'worker_starting'
  | 'qmi_starting'
  | 'recovering'
  | 'online'
  | 'degraded'
  | 'evicting'

export type DeviceOverviewItem = {
  id: string
  name: string
  running: boolean
  healthy: boolean
  control_online?: boolean
  physical_present?: boolean
  worker_running?: boolean
  data_connected?: boolean
  radio_registered?: boolean
  lifecycle_phase?: DeviceLifecyclePhase
  lifecycle_reason?: string
  network_connected: boolean
  registration_state_label?: 'registered' | 'roaming' | 'searching' | 'denied' | 'unknown'
  private_ip?: string
  private_ipv6?: string
  public_ip: string
  public_ipv6?: string
  interface?: string
  control_device?: string
  esim_transport?: string
  at_port?: string
  usb_path?: string
  local_phone?: string
  e911_setup_available?: boolean
  active_esim_profile_name?: string
  network_enabled: boolean
  vowifi_enabled?: boolean
  vowifi_active?: boolean
  vowifi_runtime?: VoWiFiRuntimeState
  radio_live_ok?: boolean
  modem: ModemStatus
  traffic?: DeviceTrafficFormatted
  traffic_raw?: DeviceTrafficRaw
  traffic_meta?: DeviceTrafficMeta
  backend_mode?: string
}

export type DeviceMgmtListItem = {
  id: string
  name: string
  running: boolean
  healthy: boolean
  control_online?: boolean
  physical_present?: boolean
  worker_running?: boolean
  data_connected?: boolean
  radio_registered?: boolean
  lifecycle_phase?: DeviceLifecyclePhase
  lifecycle_reason?: string
  network_connected: boolean
  registration_state_label?: 'registered' | 'roaming' | 'searching' | 'denied' | 'unknown'
  public_ip: string
  public_ipv6?: string
  interface?: string
  esim_transport?: string
  sms_enabled: boolean
  network_enabled: boolean
  vowifi_enabled?: boolean
  vowifi_runtime?: VoWiFiRuntimeState
  modem?: Pick<ModemStatus, 'operator' | 'native_spn' | 'native_mcc' | 'native_mnc' | 'network_mode' | 'network_duplex' | 'radio_band' | 'radio_channel' | 'signal_dbm' | 'signal_sinr' | 'imei' | 'iccid' | 'reg_status'>
}

export type DeviceConfigDTO = {
  id: string
  name: string
  interface: string
  modem_imei?: string
  usb_path?: string
  apn?: string
  ip_version?: 'v4' | 'v6' | 'v4v6'
  esim_transport?: 'at' | 'qmi'
  network_enabled?: boolean
  at_port: string
  control_device: string
  qmi_use_proxy?: boolean
  qmi_proxy_path?: string
  qmi_proxy_executable?: string
  vowifi_enabled?: boolean
  device_backend?: 'at' | 'qmi' | 'mbim'
  operator_selection_mode?: string
  operator_selection_plmn?: string
  operator_selection_rat?: string
}

export type PNNRecord = {
  record: number
  full_name?: string
  short_name?: string
  raw_hex?: string
}

export type OPLRecord = {
  record: number
  plmn?: string
  lac_start?: number
  lac_end?: number
  pnn_record?: number
  raw_hex?: string
}

export type SIMServiceTable = {
  kind?: string
  raw_hex?: string
  enabled_services?: number[]
}

export type ModemStatus = {
  imei?: string
  iccid?: string
  imsi?: string
  native_spn?: string
  native_mcc?: string
  native_mnc?: string
  gid1?: string
  gid2?: string
  pnn?: PNNRecord[]
  opl?: OPLRecord[]
  sim_service_table?: SIMServiceTable
  firmware?: string
  operator?: string
  network_mode?: string
  network_duplex?: string
  radio_band?: string
  radio_channel?: number
  apn?: string
  ims_status?: number
  signal_dbm?: number
  signal_rsrp?: number
  signal_rsrq?: number
  signal_sinr?: number
  nr5g_signal_sinr?: number
  reg_status?: number
  reg_status_text?: string
  lac?: string
  cell_id?: string
  usbnet_mode?: number
  operating_mode?: number
}

export type EsimSpaceDelta = {
  direction: 'released' | 'consumed'
  bytes: number
}

export type EsimEUICCSpec = 'sgp22' | 'sgp32' | 'sgp02'
export type EsimEUICCSpecGuess = 'unknown' | 'sgp22_compatible' | 'possible_sgp02' | 'possible_sgp32'
export type EsimEUICCSpecConfidence = 'unknown' | 'inferred' | 'verified'

export type EsimEUICCInfo = {
  aid: string
  eid: string
  spec?: EsimEUICCSpec
  spec_guess?: EsimEUICCSpecGuess
  spec_confidence?: EsimEUICCSpecConfidence
  free_nvram_bytes: number
  free_nvram: string
  firmware?: string
  manufacturer?: string
  certificates?: string[]
  info_source?: string
  info_version?: string
  info_error?: string
  sas_accreditation_number?: string
  default_smdp_address?: string
  root_ds_address?: string
}

export type EsimProfileItem = {
  iccid: string
  name: string
  service_provider_name: string
  state: number
  state_text: string
  class_text?: string
}

export type EsimEUICCProfiles = {
  eid: string
  aid_hex: string
  profiles: EsimProfileItem[]
}

export type EsimChipInfo = {
  eids: EsimEUICCInfo[]
  sku_name?: string
  serial_number?: string
  firmware?: string
}

export type EsimOverviewResponse = {
  chip_info: EsimChipInfo | null
  profiles: EsimEUICCProfiles[]
}

export type EsimNotificationItem = {
  sequence_number: number
  event: string
  iccid?: string
  address?: string
  aid_hex?: string
  can_retry: boolean
}

export type DiscoveredDevice = {
  discovery_key: string
  control_path: string
  net_interface: string
  usb_path: string
  imei?: string
  vendor_id: number
  product_id: number
  driver_name: string
  at_ports: string[]
  at_port: string
  mode?: 'qmi' | 'mbim' | 'ecm' | 'rndis' | 'ncm' | 'unknown'
  network_capable?: boolean
  configured: boolean
  configured_id?: string
  degraded?: boolean
  usbnet_mode?: number
}

export type PreparedUSBDevice = {
  usb_path: string
  vendor_id: string
  product_id: string
  control_path?: string
  net_interface?: string
  at_ports?: string[]
  qmi_interface?: string
  driver_name?: string
}

export type PrepareUSBResponse = {
  status: 'ok' | 'error'
  message: string
  supported_device_found: boolean
  prepared: boolean
  actions?: string[]
  devices?: PreparedUSBDevice[]
}

export type DashboardDevice = {
  id: string
  name?: string
  healthy: boolean
  operator?: string
  network_mode?: string
  network_duplex?: string
  signal_dbm: number
  public_ip?: string
  public_ipv6?: string
  vowifi_active?: boolean
  vowifi_runtime?: VoWiFiRuntimeState
}

export type SMSMessage = {
  id: number
  imsi?: string
  iccid?: string
  peer?: string
  sender: string
  recipient?: string
  content: string
  type: number
  status?: number // 0=未读, 1=已读, 2=发送成功, 3=发送失败
  timestamp: string
  device_name?: string
}

export type SMSContact = {
  imsi: string
  iccid?: string
  peer: string
  device_id?: string
  last_sms_id: number
  last_timestamp: string
  last_content: string
  last_type: number
  unread_count: number
  device_name?: string
  local_phone?: string  // 本机号码（收件人手机号）
}

export type CardPolicy = {
  iccid: string
  network_enabled: boolean
  vowifi_enabled: boolean
  airplane_enabled: boolean
  roaming_enabled: boolean
  ip_version: 'v4' | 'v6' | 'v4v6'
  apn: string
  source: 'auto' | 'user'
  updated_at?: string
}

export type NotificationSettings = {
  telegram: {
    enabled: boolean
    bot_token: string
    chat_id: string
  }
  webhook: {
    enabled: boolean
    urls: string[]
    secret: string
  }
  bark: {
    enabled: boolean
    urls: string[]
    group: string
    icon: string
    level: string
  }
}

// ============ 代理管理相关类型 ============
export type ProxyMode = 'socks5' | 'http'

export type ProxyInstance = {
  id: string
  name: string
  device_id: string
  enabled: boolean
  mode: ProxyMode
  listen_addr: string
  listen_port: number
  auth_enabled: boolean
  username: string
  password?: string
}

// 实例运行状态
export type ProxyInstanceStatus = {
  id: string
  mode?: ProxyMode
  running: boolean
  started_at?: string
  last_exit_at?: string
  last_exit_ok?: boolean
  last_error?: string
  listen_addr?: string
  listen_port?: number
  interface?: string
  auth_enabled?: boolean
}

// 设备简要信息（用于绑定选择）
export type ProxyDevice = {
  id: string
  name: string
  interface: string
}

// Overview 响应
export type ProxyOverviewResponse = {
  instances: ProxyInstance[]
  devices: ProxyDevice[]
  status: ProxyInstanceStatus[]
}

// ============ 前置代理（Upstream Proxy / VoWiFi Socks5 前置代理）相关类型 ============

// 前置代理实例
export type UpstreamProxy = {
  id: string
  name: string
  addr: string        // Socks5 服务器地址 (host:port；IPv6 使用 [IPv6]:port)
  username: string
  password?: string   // 列表接口返回脱敏值 "****"
  enabled: boolean
  created_at?: string
  updated_at?: string
}

// MCC/MNC 表中的国家分组
export type UpstreamProxyCountry = {
  country_code: string
  country_name: string
  mccs: string[]
}

// 国家与前置代理的路由规则
export type UpstreamProxyCountryRule = {
  country_code: string
  country_name: string
  mccs: string[]
  upstream_proxy_id: string
  enabled: boolean
  updated_at?: string
}

export type UpstreamProxyCountryRulePayload = {
  upstream_proxy_id: string
  enabled: boolean
}

export type OperatorSelectionMode = 'automatic' | 'manual'
export type OperatorSelectionRAT = '' | 'gsm' | 'lte' | 'wcdma' | 'nr5g'

export type OperatorCandidate = {
  plmn: string
  mcc?: string
  mnc?: string
  mnc_length?: number
  includes_pcs_digit?: boolean
  operator_name: string
  short_name?: string
  status: 'current' | 'available' | 'forbidden' | 'unknown'
  rats?: OperatorSelectionRAT[]
}

export type OperatorScanStatus = 'running' | 'complete' | 'failed'

export type OperatorScanResult = {
  scan_id: string
  status: OperatorScanStatus
  started_at: string
  updated_at: string
  complete: boolean
  retryable: boolean
  message: string
  error?: string
  candidates: OperatorCandidate[]
}

export type OperatorSelection = {
  mode: OperatorSelectionMode
  plmn?: string
  mcc?: string
  mnc?: string
  mnc_length?: number
  includes_pcs_digit?: boolean
  rat?: OperatorSelectionRAT
  operator_name?: string
}

export type SetOperatorSelectionRequest = {
  mode: OperatorSelectionMode
  plmn?: string
  includes_pcs_digit?: boolean
  rat?: OperatorSelectionRAT
}

export type CarrierWebsheetInfo = {
  id: string
  embedUrl: string
  title?: string
  url: string
  method: string
}
