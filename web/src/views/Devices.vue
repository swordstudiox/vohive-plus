<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import PageHeader from '../components/PageHeader.vue'
import ErrorState from '../components/ErrorState.vue'
import RefreshButton from '../components/RefreshButton.vue'
import DeviceListPanel from '../components/DeviceListPanel.vue'
import DeviceDetailLoading from '../components/DeviceDetailLoading.vue'
import DeviceDetailHeader from '../components/DeviceDetailHeader.vue'
import DeviceOverviewTab from '../components/DeviceOverviewTab.vue'
import DeviceEsimTab from '../components/DeviceEsimTab.vue'
import DeviceAtTab from '../components/DeviceAtTab.vue'
import DeviceUssdTab from '../components/DeviceUssdTab.vue'
import DeviceConfigTab from '../components/DeviceConfigTab.vue'
import CardPolicyPanel from '../components/CardPolicyPanel.vue'
import DeviceAddDialog from '../components/DeviceAddDialog.vue'
import CarrierWebsheetDialog from '../components/CarrierWebsheetDialog.vue'
import TrafficAnalysisPanel from '../components/TrafficAnalysisPanel.vue'
import { usePollingScheduler } from '../composables/usePollingScheduler'
import { useEventStream } from '../composables/useEventStream'
import { useDevicesStore } from '../stores/devices'
import { debugCollector } from '../debug/collector'
import { copyToClipboard } from '../utils/clipboard'
import { isWwanQmiControlPath } from '../utils/deviceBackend'
import { defaultBackendForDiscoveredDevice } from '../utils/deviceDiscovery'
import { isControlOnline, isRecoveryPhase } from '../utils/deviceLifecycle'
import { getMccMncIndex, isoToFlagEmoji, type MccMncRow } from '../utils/mcc-mnc'
import type { CardPolicy, CarrierWebsheetInfo, DeviceConfigDTO, DeviceMgmtListItem, DeviceOverviewItem, DiscoveredDevice, ModemStatus, PNNRecord, RealtimeTrafficSnapshot } from '../types/api'
import type { AppError } from '../types/domain'
import { toAppError } from '../services/http'
import { devicesService } from '../services/devices'
import { cardsService } from '../services/cards'
import { createEmptyTrafficAnalysis, trafficService, type TrafficRange } from '../services/traffic'
import {
  ArrowSync24Regular,
  Add24Regular
} from '@vicons/fluent'

const router = useRouter()
const route = useRoute()
const devicesStore = useDevicesStore()
const { list: storeList, detail: storeDetail, discovered: storeDiscovered, config: storeConfig, deviceLimit } = storeToRefs(devicesStore)

let listAbort: AbortController | null = null
let detailAbort: AbortController | null = null
let trafficAbort: AbortController | null = null

const detailPollFailCount = ref(0)
const listPollFailCount = ref(0)
const detailPollWarned = ref(false)
const listPollWarned = ref(false)

const loading = ref(true)
const activeTab = ref('overview')
const loadLastOkAt = ref<number | null>(null)
const loadError = ref<{ message: string; status?: number; method?: string; url?: string } | null>(null)
const devices = ref<DeviceMgmtListItem[]>([])
const discovered = ref<DiscoveredDevice[]>([])
const query = ref('')
const statusFilter = ref<'all' | 'online' | 'offline'>('all')
const sortKey = ref<'name' | 'signal'>('name')
const sortDir = ref<'asc' | 'desc'>('asc')
const selectedId = ref('')
const selectedDetail = ref<DeviceOverviewItem | null>(null)
const hasAutoSelected = ref(false)
const deviceTabs = new Set(['overview', 'esim', 'at', 'ussd', 'config', 'card'])

const editConfig = ref<DeviceConfigDTO | null>(null)
const editBaseline = ref('')
const editDirty = ref(false)
const saving = ref(false)
const rotating = ref(false)
const reconnectingVoWiFi = ref(false)
const e911Starting = ref(false)
const e911WebsheetOpen = ref(false)
const e911Websheet = ref<CarrierWebsheetInfo | null>(null)
const deleting = ref(false)
const rescanning = ref(false)

// 卡策略（跟当前选中设备的 ICCID 绑定）
const cardPolicy = ref<CardPolicy | null>(null)
const cardPolicyRequestSeq = ref(0)

const trafficSpeedRx = ref('')
const trafficSpeedTx = ref('')
const rollingMinuteRx = ref('')
const rollingMinuteTx = ref('')
const realtimeTrafficActiveUntil = ref(0)
const deviceAnalysis = ref(createEmptyTrafficAnalysis())
const deviceAnalysisLoading = ref(false)
const deviceAnalysisLastOkAt = ref<number | null>(null)
const deviceAnalysisError = ref<AppError | null>(null)
const deviceAnalysisRange = ref<TrafficRange>('day')

const addableDiscovered = computed(() => discovered.value.filter((item) => !item.configured))

const addDialogOpen = ref(false)
const addSelected = ref<DiscoveredDevice | null>(null)
const addSaving = ref(false)
const addConfig = ref<DeviceConfigDTO>({
  id: '',
  name: '',
  interface: '',
  modem_imei: '',
  usb_path: '',
  esim_transport: 'at',
  at_port: '',
  control_device: '',
  device_backend: 'at'
})

const discovering = ref(false)

type RollingTrafficSample = {
  at: number
  rxBytes: number
  txBytes: number
}

const realtimeTrafficWindowMs = 60_000
let rollingTrafficWindow: RollingTrafficSample[] = []

const filteredDevices = computed<DeviceMgmtListItem[]>(() => {
  const q = String(query.value || '').trim().toLowerCase()
  let list = devices.value.slice()

  if (statusFilter.value === 'online') {
    list = list.filter(d => isControlOnline(d))
  } else if (statusFilter.value === 'offline') {
    list = list.filter(d => !d?.running && !isRecoveryPhase(d.lifecycle_phase))
  }

  if (q) {
    list = list.filter(d => {
      const hay = `${d?.id || ''} ${d?.name || ''} ${d?.modem?.iccid || ''} ${d?.modem?.imei || ''} ${d?.interface || ''}`.toLowerCase()
      return hay.includes(q)
    })
  }

  list.sort((a, b) => {
    let av = 0
    let bv = 0
    if (sortKey.value === 'name') {
      const an = (a.name || a.id || '').toLowerCase()
      const bn = (b.name || b.id || '').toLowerCase()
      if (an < bn) return sortDir.value === 'asc' ? -1 : 1
      if (an > bn) return sortDir.value === 'asc' ? 1 : -1
      return 0
    }
    if (sortKey.value === 'signal') {
      av = Number(a?.modem?.signal_dbm ?? -999)
      bv = Number(b?.modem?.signal_dbm ?? -999)
      return sortDir.value === 'asc' ? av - bv : bv - av
    }
    return 0
  })

  return list
})

const selectedListItem = computed<DeviceMgmtListItem | null>(() => {
  return devices.value.find(d => d.id === selectedId.value) || null
})

const selectedDevice = computed<DeviceOverviewItem | null>(() => {
  return selectedDetail.value
})

const RADIO_LIVE_GRACE_MS = 3000

type LiveRadioFields = Pick<
  ModemStatus,
  'operator' | 'signal_dbm' | 'signal_rsrp' | 'signal_rsrq' | 'signal_sinr' | 'nr5g_signal_sinr' | 'radio_band' | 'radio_channel' | 'reg_status' | 'reg_status_text' | 'network_mode' | 'network_duplex' | 'operating_mode'
>

type LiveRadioCacheEntry = {
  capturedAt: number
  radio: LiveRadioFields
}

const liveRadioCache = reactive(new Map<string, LiveRadioCacheEntry>())
const pendingRawSelectedDetail = ref<DeviceOverviewItem | null>(null)
const mccMncIndex = ref<Map<string, MccMncRow> | null>(null)
let liveRadioFallbackTimer: number | null = null

function normalizeSPN(v: unknown): string {
  return String(v ?? '').trim()
}

function firstQueryValue(value: unknown): string {
  if (Array.isArray(value)) return String(value[0] ?? '')
  return String(value ?? '')
}

function applyRouteSelection(): boolean {
  const tab = firstQueryValue(route.query.tab).trim()
  if (deviceTabs.has(tab)) {
    activeTab.value = tab
  }

  const deviceID = firstQueryValue(route.query.device).trim()
  if (!deviceID || selectedId.value === deviceID) {
    return false
  }

  selectedId.value = deviceID
  hasAutoSelected.value = true
  return true
}

function nativeMccMnc(modem: ModemStatus | undefined): string {
  const mcc = String(modem?.native_mcc ?? '').trim()
  const mnc = String(modem?.native_mnc ?? '').trim()
  return mcc && mnc ? `${mcc}${mnc}` : ''
}

function pnnDisplayName(record: PNNRecord | undefined): string {
  return normalizeSPN(record?.full_name) || normalizeSPN(record?.short_name)
}

function firstPNNName(records: PNNRecord[] | undefined): string {
  if (!Array.isArray(records)) return ''
  for (const r of records) {
    const name = pnnDisplayName(r)
    if (name) return name
  }
  return ''
}

function oplMatchesNativePLMN(oplPLMN: string | undefined, nativePLMN: string): boolean {
  const pattern = String(oplPLMN ?? '').trim().toLowerCase()
  if (!pattern || !nativePLMN) return false
  if (pattern === nativePLMN) return true
  if (!pattern.includes('x')) return pattern.length < nativePLMN.length && nativePLMN.startsWith(pattern)
  if (pattern.length !== nativePLMN.length) return false
  for (let i = 0; i < pattern.length; i++) {
    if (pattern[i] !== 'x' && pattern[i] !== nativePLMN[i]) return false
  }
  return true
}

function pnnNameFromOPL(modem: ModemStatus | undefined): string {
  const nativePLMN = nativeMccMnc(modem)
  if (!nativePLMN || !Array.isArray(modem?.opl) || !Array.isArray(modem?.pnn)) return ''
  for (const opl of modem.opl) {
    if (!oplMatchesNativePLMN(opl?.plmn, nativePLMN)) continue
    const pnnRecord = Number(opl?.pnn_record ?? 0)
    if (!pnnRecord) continue
    const name = pnnDisplayName(modem.pnn.find((record) => record.record === pnnRecord))
    if (name) return name
  }
  return ''
}

function flagForMccMnc(code: string): string {
  const row = mccMncIndex.value?.get(code)
  return row ? isoToFlagEmoji(row.iso) : ''
}

function formatNamedOperator(name: string, code: string): string {
  const flag = flagForMccMnc(code)
  if (!code) return flag ? `${flag} ${name}` : name
  return `${flag ? flag + ' ' : ''}${name} (${code})`
}

function formatMccMncOperator(code: string): string {
  const index = mccMncIndex.value
  if (!index || !code) return code
  const row = index.get(code)
  if (!row) return code
  const name = normalizeSPN(row.network) || normalizeSPN(row.country)
  return name ? formatNamedOperator(name, code) : code
}

function extractLiveRadioFields(detail: DeviceOverviewItem): LiveRadioFields {
  return {
    operator: detail.modem?.operator,
    signal_dbm: detail.modem?.signal_dbm,
    signal_rsrp: detail.modem?.signal_rsrp,
    signal_rsrq: detail.modem?.signal_rsrq,
    signal_sinr: detail.modem?.signal_sinr,
    nr5g_signal_sinr: detail.modem?.nr5g_signal_sinr,
    radio_band: detail.modem?.radio_band,
    radio_channel: detail.modem?.radio_channel,
    reg_status: detail.modem?.reg_status,
    reg_status_text: detail.modem?.reg_status_text,
    network_mode: detail.modem?.network_mode,
    network_duplex: detail.modem?.network_duplex,
    operating_mode: detail.modem?.operating_mode
  }
}

function mergeLiveRadioFields(detail: DeviceOverviewItem, radio: LiveRadioFields): DeviceOverviewItem {
  return {
    ...detail,
    modem: {
      ...detail.modem,
      ...radio
    }
  }
}

function clearLiveRadioFallbackTimer() {
  if (liveRadioFallbackTimer !== null) {
    window.clearTimeout(liveRadioFallbackTimer)
    liveRadioFallbackTimer = null
  }
  pendingRawSelectedDetail.value = null
}

function scheduleLiveRadioFallback(detail: DeviceOverviewItem, remainingMs: number) {
  clearLiveRadioFallbackTimer()
  pendingRawSelectedDetail.value = detail
  if (remainingMs <= 0) {
    selectedDetail.value = detail
    pendingRawSelectedDetail.value = null
    updateTrafficSpeedFromSelected()
    return
  }
  const expectedID = detail.id
  liveRadioFallbackTimer = window.setTimeout(() => {
    liveRadioFallbackTimer = null
    const pending = pendingRawSelectedDetail.value
    pendingRawSelectedDetail.value = null
    if (!pending || selectedId.value !== expectedID || pending.id !== expectedID) return
    selectedDetail.value = pending
    updateTrafficSpeedFromSelected()
  }, remainingMs)
}

function resolveDetailForDisplay(detail: DeviceOverviewItem | null): DeviceOverviewItem | null {
  if (!detail) {
    clearLiveRadioFallbackTimer()
    return null
  }

  if (detail.radio_live_ok === true) {
    liveRadioCache.set(detail.id, {
      capturedAt: Date.now(),
      radio: extractLiveRadioFields(detail)
    })
    clearLiveRadioFallbackTimer()
    return detail
  }

  if (detail.radio_live_ok !== false) {
    clearLiveRadioFallbackTimer()
    return detail
  }

  const cached = liveRadioCache.get(detail.id)
  if (!cached) {
    clearLiveRadioFallbackTimer()
    return detail
  }

  const age = Date.now() - cached.capturedAt
  if (age >= RADIO_LIVE_GRACE_MS) {
    clearLiveRadioFallbackTimer()
    return detail
  }

  scheduleLiveRadioFallback(detail, RADIO_LIVE_GRACE_MS - age)
  return mergeLiveRadioFields(detail, cached.radio)
}

const selectedSimOperatorDisplay = computed(() => {
  const d = selectedDevice.value
  if (!d) return '--'
  const spn = normalizeSPN(d?.modem?.native_spn)
  const pnn = pnnNameFromOPL(d?.modem) || firstPNNName(d?.modem?.pnn)
  const mccmnc = nativeMccMnc(d?.modem)
  if (spn) return formatNamedOperator(spn, mccmnc)
  if (pnn) return formatNamedOperator(pnn, mccmnc)
  return mccmnc ? formatMccMncOperator(mccmnc) : '--'
})

function formatBytesPerSecond(bps: unknown) {
  const v = Number(bps) || 0
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let val = v
  let i = 0
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024
    i++
  }
  return `${val.toFixed(i === 0 ? 0 : 1)}${units[i]}`
}

function formatBytes(bytes: unknown) {
  const v = Number(bytes) || 0
  const units = ['B', 'KB', 'MB', 'GB']
  let val = v
  let i = 0
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024
    i++
  }
  return `${val.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function resetRollingTrafficWindow() {
  rollingTrafficWindow = []
  rollingMinuteRx.value = ''
  rollingMinuteTx.value = ''
}

function setRollingTrafficWindowStatus(value: string) {
  rollingTrafficWindow = []
  rollingMinuteRx.value = value
  rollingMinuteTx.value = value
}

function updateRollingTrafficWindow(rxDeltaBytes: unknown, txDeltaBytes: unknown, at = Date.now()) {
  const cutoff = at - realtimeTrafficWindowMs
  rollingTrafficWindow = rollingTrafficWindow.filter(sample => sample.at >= cutoff)
  rollingTrafficWindow.push({
    at,
    rxBytes: Math.max(0, Number(rxDeltaBytes) || 0),
    txBytes: Math.max(0, Number(txDeltaBytes) || 0)
  })

  let rxBytes = 0
  let txBytes = 0
  for (const sample of rollingTrafficWindow) {
    rxBytes += sample.rxBytes
    txBytes += sample.txBytes
  }
  rollingMinuteRx.value = formatBytes(rxBytes)
  rollingMinuteTx.value = formatBytes(txBytes)
}

function updateTrafficSpeedFromSelected() {
  const d = selectedDetail.value
  if (!d || !d.network_connected || !d.traffic_raw || d.traffic_meta?.status !== 'ok') {
    trafficSpeedRx.value = ''
    trafficSpeedTx.value = ''
    resetRollingTrafficWindow()
    return
  }
  if (Date.now() < realtimeTrafficActiveUntil.value) {
    return
  }
  const rx = Number(d.traffic_raw?.bytes_received ?? 0)
  const tx = Number(d.traffic_raw?.bytes_sent ?? 0)
  trafficSpeedRx.value = formatBytesPerSecond(Math.max(0, rx) / 60)
  trafficSpeedTx.value = formatBytesPerSecond(Math.max(0, tx) / 60)
  resetRollingTrafficWindow()
}

function handleRealtimeTrafficEvent(data: RealtimeTrafficSnapshot) {
  if (!data || data.device_id !== selectedId.value) return

  realtimeTrafficActiveUntil.value = Date.now() + 2500
  if (data.status === 'ok') {
    trafficSpeedRx.value = formatBytesPerSecond(Math.max(0, Number(data.rx_bps) || 0))
    trafficSpeedTx.value = formatBytesPerSecond(Math.max(0, Number(data.tx_bps) || 0))
    updateRollingTrafficWindow(data.rx_delta_bytes, data.tx_delta_bytes)
    return
  }
  if (data.status === 'waiting_sample') {
    trafficSpeedRx.value = '等待采样'
    trafficSpeedTx.value = '等待采样'
    setRollingTrafficWindowStatus('等待采样')
    return
  }
  if (data.status === 'reset') {
    trafficSpeedRx.value = formatBytesPerSecond(0)
    trafficSpeedTx.value = formatBytesPerSecond(0)
    setRollingTrafficWindowStatus(formatBytes(0))
    return
  }
  trafficSpeedRx.value = '采样中断'
  trafficSpeedTx.value = '采样中断'
  setRollingTrafficWindowStatus('采样中断')
}

function resetDeviceTrafficAnalysis() {
  if (trafficAbort) {
    trafficAbort.abort()
    trafficAbort = null
  }
  deviceAnalysis.value = createEmptyTrafficAnalysis()
  deviceAnalysisLoading.value = false
  deviceAnalysisError.value = null
  deviceAnalysisLastOkAt.value = null
}

async function fetchDeviceTrafficAnalysis(id = selectedDetail.value?.id || selectedId.value) {
  const detail = selectedDetail.value
  const deviceID = String(id || '').trim()
  if (!deviceID) {
    resetDeviceTrafficAnalysis()
    return
  }
  if (detail?.id === deviceID && !detail.network_connected) {
    resetDeviceTrafficAnalysis()
    return
  }

  if (trafficAbort) trafficAbort.abort()
  const controller = new AbortController()
  trafficAbort = controller
  deviceAnalysisLoading.value = true
  deviceAnalysisError.value = null

  const result = await trafficService.getAnalysis(deviceAnalysisRange.value, deviceID, controller.signal)
  if (trafficAbort !== controller) return

  if (!result.ok) {
    if (result.error.code !== 'ERR_CANCELED') {
      deviceAnalysis.value = createEmptyTrafficAnalysis()
      deviceAnalysisError.value = result.error
    }
    trafficAbort = null
    deviceAnalysisLoading.value = false
    return
  }

  deviceAnalysis.value = result.data
  deviceAnalysisLastOkAt.value = Date.now()
  trafficAbort = null
  deviceAnalysisLoading.value = false
}

async function fetchSelectedDetail(id: string) {
  const deviceID = String(id || '').trim()
  if (!deviceID) {
    clearLiveRadioFallbackTimer()
    selectedDetail.value = null
    updateTrafficSpeedFromSelected()
    return
  }

  if (detailAbort) detailAbort.abort()
  detailAbort = new AbortController()
  const result = await devicesStore.fetchDetail(deviceID, detailAbort.signal)
  if (result.ok) {
    selectedDetail.value = resolveDetailForDisplay((storeDetail.value || null) as DeviceOverviewItem | null)
  }
  updateTrafficSpeedFromSelected()
}

async function syncEditConfigFromSelected(force = false) {
  if (!force && editDirty.value) return
  const id = String(selectedId.value || '').trim()
  if (!id) {
    editConfig.value = null
    editBaseline.value = ''
    editDirty.value = false
    return
  }
  try {
    const result = await devicesStore.fetchConfig(id)
    if (!result.ok) throw new Error(result.error.message)
    editConfig.value = JSON.parse(JSON.stringify(storeConfig.value || {})) as DeviceConfigDTO

    const activeControlDevice = selectedDetail.value?.control_device || editConfig.value.control_device
    if (isWwanQmiControlPath(activeControlDevice)) {
      editConfig.value.device_backend = 'qmi'
    } else if (!editConfig.value.device_backend) {
      editConfig.value.device_backend = 'at'
    }
    editBaseline.value = JSON.stringify(editConfig.value)
    editDirty.value = false
  } catch {
    editConfig.value = null
    editBaseline.value = ''
    editDirty.value = false
  }
}

watch(
  editConfig,
  (v) => {
    if (!v) {
      editDirty.value = false
      editBaseline.value = ''
      return
    }
    const cur = JSON.stringify(v)
    if (!editBaseline.value) {
      editBaseline.value = cur
      editDirty.value = false
      return
    }
    editDirty.value = cur !== editBaseline.value
  },
  { deep: true }
)

function samePolicyICCID(a: string | undefined, b: string | undefined) {
  const normalize = (v: string | undefined) => String(v || '').trim().replace(/^"|"$/g, '').replace(/[Ff]+$/g, '')
  return normalize(a) !== '' && normalize(a) === normalize(b)
}

async function fetchCardPolicy(iccid: string | undefined) {
  const requestSeq = ++cardPolicyRequestSeq.value
  if (!iccid) {
    cardPolicy.value = null
    return
  }
  cardPolicy.value = null
  const result = await cardsService.getPolicy(iccid)
  if (requestSeq !== cardPolicyRequestSeq.value) return
  if (result.ok && samePolicyICCID(result.data.iccid, iccid)) {
    cardPolicy.value = result.data
  }
}

// 卡策略热切换后：刷新卡策略 + 概览详情（让概览即时反映网络/VoWiFi/飞行模式面板切换）
async function onCardPolicyChanged() {
  await Promise.all([
    fetchCardPolicy(selectedDetail.value?.modem?.iccid),
    refreshSelectedDetailOnly()
  ])
}

watch(
  () => selectedDetail.value?.modem?.iccid,
  (iccid) => { void fetchCardPolicy(iccid) },
  { immediate: true }
)

async function copyText(text: unknown) {
  const val = String(text ?? '').trim()
  if (!val || val === '--' || val === '---') return
  await copyToClipboard(val, '已复制')
}

async function fetchAll() {
  loading.value = true
  loadError.value = null
  try {
    const prevSelected = selectedId.value
    if (listAbort) listAbort.abort()
    listAbort = new AbortController()
    const listResult = await devicesStore.fetchList(listAbort.signal)
    if (!listResult.ok) throw new Error(listResult.error.message)
    devices.value = (storeList.value || []) as DeviceMgmtListItem[]
    loadLastOkAt.value = Date.now()
    applyRouteSelection()

    if (!hasAutoSelected.value && !selectedId.value && devices.value.length) {
      selectedId.value = devices.value[0].id
      hasAutoSelected.value = true
    }
    // 移除强行重置 selectedId 的逻辑，因为这会在轮询期间当设备短暂拿不到时，把界面强制拉回到第一项。
    const selectedStillExists = selectedId.value
      ? devices.value.some(d => d.id === selectedId.value)
      : false
    if (selectedId.value && !selectedStillExists && devices.value.length === 0) {
      selectedId.value = ''
    }
    
    // 如果之前没有选中的id或者这次选中改变了，则加载详情
    const selectionChanged = prevSelected !== selectedId.value || (selectedDetail.value?.id || '') !== selectedId.value
    if (selectionChanged) {
      await fetchSelectedDetail(selectedId.value)
    }
    if (selectionChanged || !editConfig.value) await syncEditConfigFromSelected()
  } catch (e: unknown) {
    const err = toAppError(e)
    if (err.code === 'ERR_CANCELED') {
      loading.value = false
      return
    }
    loadError.value = {
      message: err.message || '加载设备信息失败',
      status: err.status,
      method: err.method,
      url: err.url
    }
  } finally {
    loading.value = false
  }
}

async function refreshListOnly() {
  try {
    const prevSelected = selectedId.value
    if (listAbort) listAbort.abort()
    listAbort = new AbortController()
    const listResult = await devicesStore.fetchList(listAbort.signal)
    if (!listResult.ok) throw new Error(listResult.error.message)
    devices.value = (storeList.value || []) as DeviceMgmtListItem[]
    // 自动刷新时如果当前选中的设备突然不在列表中，不要强行将其重置。
    // 这可以避免正在配置某设备时，因为网络或拔插一秒钟的掉线导致系统强制关闭当前配置并把页面顶上去拉回到第一项。
    if (!hasAutoSelected.value && !selectedId.value && devices.value.length) {
      selectedId.value = devices.value[0]?.id || ''
      hasAutoSelected.value = !!selectedId.value
    }
    const selectedStillExists = selectedId.value
      ? devices.value.some(d => d.id === selectedId.value)
      : false
    if (selectedId.value && !selectedStillExists && devices.value.length === 0) {
      selectedId.value = ''
    }
    const selectionChanged = prevSelected !== selectedId.value || (selectedDetail.value?.id || '') !== selectedId.value
    if (selectionChanged) {
      await fetchSelectedDetail(selectedId.value)
      await syncEditConfigFromSelected()
    }
    listPollFailCount.value = 0
    listPollWarned.value = false
  } catch (e: unknown) {
    const err = toAppError(e)
    if (err.code === 'ERR_CANCELED') return
    listPollFailCount.value += 1
    debugCollector.recordApiError(e)
    if (listPollFailCount.value >= 3 && !listPollWarned.value) {
      listPollWarned.value = true
      ElMessage.warning('设备列表刷新异常，已自动降低刷新频率')
    }
    throw e
  }
}

async function refreshSelectedDetailOnly() {
  if (!selectedId.value) return
  try {
    await fetchSelectedDetail(selectedId.value)
    detailPollFailCount.value = 0
    detailPollWarned.value = false
  } catch (e: unknown) {
    const err = toAppError(e)
    if (err.code === 'ERR_CANCELED') return
    detailPollFailCount.value += 1
    debugCollector.recordApiError(e)
    if (detailPollFailCount.value >= 3 && !detailPollWarned.value) {
      detailPollWarned.value = true
      ElMessage.warning('设备详情刷新异常，已自动降低刷新频率')
    }
    throw e
  }
}

async function refreshDeviceViews() {
  await Promise.all([refreshSelectedDetailOnly(), refreshListOnly()])
}

function scheduleRefreshDeviceViews(delayMs: number) {
  window.setTimeout(() => {
    void refreshDeviceViews().catch(() => {})
  }, delayMs)
}

async function selectDevice(id: string) {
  const next = String(id || '').trim()
  if (!next) return
  if (selectedId.value === next && selectedDetail.value) return
  selectedId.value = next
  hasAutoSelected.value = true
  void router.replace({
    name: 'Devices',
    query: {
      ...route.query,
      device: next,
      tab: activeTab.value
    }
  })
  await Promise.all([fetchSelectedDetail(next), syncEditConfigFromSelected()])
}

watch(
  () => [route.query.device, route.query.tab],
  () => {
    const selectionChanged = applyRouteSelection()
    if (!selectionChanged) return
    void Promise.all([
      fetchSelectedDetail(selectedId.value),
      syncEditConfigFromSelected()
    ])
  }
)

async function rotateIP() {
  const id = String(selectedId.value || '').trim()
  if (!id) return
  if (!selectedListItem.value?.network_connected) {
    ElMessage.warning('设备网络未连接，请先启动网络')
    return
  }
  const confirmed = await ElMessageBox.confirm(
    `确定对设备 ${id} 执行 IP 轮换？`,
    '确认操作',
    { confirmButtonText: '立即轮换', cancelButtonText: '取消', type: 'warning' }
  ).then(() => true).catch(() => false)
  if (!confirmed) return

  rotating.value = true
  try {
    const result = await devicesService.rotateIP(id)
    if (!result.ok) throw new Error(result.error.message || '轮换失败')
    ElMessage.success('轮换请求已发送')
    await refreshDeviceViews()
    scheduleRefreshDeviceViews(1500)
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '轮换失败')
  } finally {
    rotating.value = false
  }
}

async function reconnectVoWiFi() {
  const id = String(selectedId.value || '').trim()
  if (!id) return
  const confirmed = await ElMessageBox.confirm(
    `确定对设备 ${id} 发起 VoWiFi 环境的重新连接拨号？这将在后台重新注册 IMS 链路。`,
    '重连 VoWiFi',
    { confirmButtonText: '确定重连', cancelButtonText: '取消', type: 'info' }
  ).then(() => true).catch(() => false)
  if (!confirmed) return

  reconnectingVoWiFi.value = true
  try {
    const result = await devicesService.reconnectVoWiFi(id)
    if (!result.ok) throw new Error(result.error.message || '重连请求失败')
    ElMessage.success('已触发重连指令，VoWiFi 服务正在重启...')
    void refreshDeviceViews().catch(() => {})
    scheduleRefreshDeviceViews(4000)
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '重连命令下发失败')
  } finally {
    reconnectingVoWiFi.value = false
  }
}

async function openE911Websheet() {
  const id = String(selectedId.value || '').trim()
  if (!id || e911Starting.value) return

  e911Starting.value = true
  try {
    const result = await devicesService.startE911Websheet(id)
    if (!result.ok) throw new Error(result.error.message || 'E911地址设置页面打开失败')
    e911Websheet.value = result.data
    e911WebsheetOpen.value = true
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || 'E911地址设置页面打开失败')
  } finally {
    e911Starting.value = false
  }
}

async function finishE911Websheet() {
  e911WebsheetOpen.value = false
  e911Websheet.value = null
  await refreshDeviceViews()
}

const rebooting = ref(false)
async function rebootModem() {
  const id = String(selectedId.value || '').trim()
  if (!id) return
  const confirmed = await ElMessageBox.confirm(
    `确定对设备 ${id} 发送重启模组指令？设备将在此期间脱网和失联数秒。`,
    '确认重启',
    { confirmButtonText: '立即重启', cancelButtonText: '取消', type: 'warning' }
  ).then(() => true).catch(() => false)
  if (!confirmed) return

  rebooting.value = true
  try {
    const result = await devicesService.rebootModem(id)
    if (!result.ok) throw new Error(result.error.message || '指令下发失败')
    ElMessage.success('重启指令已送达，设备正在重新启动')
    void refreshDeviceViews().catch(() => {})
    // 稍微延迟查询，因为网络可能正在断开
    scheduleRefreshDeviceViews(5000)
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '指令下发失败')
  } finally {
    rebooting.value = false
  }
}

// 手动触发设备重新扫描
async function rescanDevices() {
  rescanning.value = true
  try {
    const result = await devicesService.rescanAll()
    if (!result.ok) throw new Error(result.error.message || '重新扫描失败')
    ElMessage.success('设备重新扫描完成')
    await fetchAll()
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '重新扫描失败')
  } finally {
    rescanning.value = false
  }
}

function openSms() {
  if (!selectedId.value) return
  router.push(`/sms?device=${selectedId.value}`)
}

async function saveConfig() {
  const id = String(selectedId.value || '').trim()
  if (!id || !editConfig.value) return
  saving.value = true
  try {
    const result = await devicesService.updateConfig(id, editConfig.value)
    if (!result.ok) throw new Error(result.error.message || '保存失败')
    if (result.data.warning) {
      ElMessage.warning(result.data.warning)
    } else if (result.data.requiresRestart) {
      ElMessage.warning('配置已保存，但部分变更需要重启服务后生效')
    } else {
      ElMessage.success('配置已保存')
    }
    editDirty.value = false
    editBaseline.value = JSON.stringify(editConfig.value)
    await Promise.all([refreshListOnly(), refreshSelectedDetailOnly()])
    await syncEditConfigFromSelected(true)
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function deleteDevice() {
  const id = String(selectedId.value || '').trim()
  if (!id) return
  const confirmed = await ElMessageBox.confirm(
    `确定删除设备 ${id} 的配置？删除后该设备将停止接管（代理/网络/AT）。`,
    '确认删除',
    { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' }
  ).then(() => true).catch(() => false)
  if (!confirmed) return

  deleting.value = true
  try {
    const result = await devicesService.deleteManaged(id)
    if (!result.ok) throw new Error(result.error.message || '删除失败')
    ElMessage.success('设备已删除')
    clearLiveRadioFallbackTimer()
    selectedDetail.value = null
    updateTrafficSpeedFromSelected()
    resetDeviceTrafficAnalysis()
    editConfig.value = null
    editBaseline.value = ''
    editDirty.value = false
    selectedId.value = ''
    hasAutoSelected.value = false
    await fetchAll()
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '删除失败')
  } finally {
    deleting.value = false
  }
}

function openAddDialog() {
  addDialogOpen.value = true
  addSelected.value = null
  addConfig.value = {
    id: '',
    name: '',
    interface: '',
    modem_imei: '',
    usb_path: '',
    esim_transport: 'at',
    at_port: '',
    control_device: '',
    device_backend: 'at'
  }
  refreshDiscoveredForAdd()
}

async function refreshDiscoveredForAdd() {
  discovering.value = true
  try {
    const result = await devicesStore.fetchDiscovered()
    if (result.ok) {
      discovered.value = Array.isArray(storeDiscovered.value) ? storeDiscovered.value : []
    } else {
      discovered.value = []
      ElMessage.warning(result.error.message || '设备探测失败')
    }
  } catch (e: unknown) {
    discovered.value = []
    const err = toAppError(e)
    ElMessage.warning(err.message || '设备探测失败')
  } finally {
    discovering.value = false
  }
}

function applyDiscoveredToAddConfig(d: DiscoveredDevice | null, options: { preserveBackend?: boolean } = {}) {
  if (!d) return
  const selectedBackend = addConfig.value.device_backend
  addConfig.value.interface = d.net_interface || ''
  addConfig.value.at_port = d.at_port || ''
  addConfig.value.control_device = d.control_path || ''
  addConfig.value.modem_imei = d.imei || ''
  addConfig.value.usb_path = d.usb_path || ''

  if (options.preserveBackend && selectedBackend) {
    addConfig.value.device_backend = selectedBackend
    return
  }

  addConfig.value.device_backend = defaultBackendForDiscoveredDevice(d)
}

function selectDiscoveredForAdd(d: DiscoveredDevice) {
  if (d.degraded) {
    ElMessage.warning('无法读取该设备 IMEI（可能控制口挂死），请执行 AT!RESET 或切换组态后重试')
    return
  }
  addSelected.value = d
  applyDiscoveredToAddConfig(d)
}

async function addDevice() {
  addSaving.value = true
  try {
    if (!addSelected.value) {
      ElMessage.warning('请选择一个未配置设备')
      return
    }
    applyDiscoveredToAddConfig(addSelected.value, { preserveBackend: true })
    const result = await devicesService.addManaged(addConfig.value)
    if (!result.ok) throw new Error(result.error.message || '添加失败')
    const warning = result.data.warning
    const started = result.data.started
    if (warning) {
      ElMessage.warning(warning)
    } else if (started === true) {
      ElMessage.success('设备已添加并开始接管')
    } else {
      ElMessage.success('设备配置已添加')
    }
    addDialogOpen.value = false
    await fetchAll()
  } catch (e: unknown) {
    const err = toAppError(e)
    ElMessage.error(err.message || '添加失败')
  } finally {
    addSaving.value = false
  }
}

function refreshCurrentDeviceTrafficAnalysis() {
  const detail = selectedDetail.value
  if (!detail?.id) {
    resetDeviceTrafficAnalysis()
    return
  }
  void fetchDeviceTrafficAnalysis(detail.id)
}

function handleDeviceTrafficRangeChange(range: TrafficRange) {
  if (deviceAnalysisRange.value === range) return
  deviceAnalysisRange.value = range
  refreshCurrentDeviceTrafficAnalysis()
}

watch(
  selectedId,
  (nextID, prevID) => {
    if (nextID !== prevID) {
      clearLiveRadioFallbackTimer()
      resetDeviceTrafficAnalysis()
    }
  }
)

watch(
  () => selectedDetail.value?.id || '',
  (nextID) => {
    if (nextID) {
      void fetchDeviceTrafficAnalysis(nextID)
    }
  }
)

watch(
  () => selectedDetail.value?.network_connected,
  (connected, prevConnected) => {
    const detail = selectedDetail.value
    if (!detail?.id) return
    if (!connected) {
      resetDeviceTrafficAnalysis()
      return
    }
    if (prevConnected === false && connected === true) {
      void fetchDeviceTrafficAnalysis(detail.id)
    }
  }
)

watch(activeTab, (tab, prevTab) => {
  if (tab === 'overview' && prevTab !== 'overview') {
    refreshCurrentDeviceTrafficAnalysis()
  }
})

onMounted(() => {
  fetchAll()
  getMccMncIndex().then(index => {
    mccMncIndex.value = index
  }).catch(() => {})
})

onBeforeUnmount(() => {
  if (listAbort) listAbort.abort()
  if (detailAbort) detailAbort.abort()
  if (trafficAbort) trafficAbort.abort()
  clearLiveRadioFallbackTimer()
})

// 由于 SSE 只订阅当前选中的单设备详情，恢复列表的低频拉取（无 IPC 开销）以同步左右设备增减和状态跳变。
const listEnabled = computed(() => !loading.value)
usePollingScheduler(refreshListOnly, 15000, {
  enabled: listEnabled,
  maxIntervalMs: 120000,
  backgroundIntervalMs: 45000
})

type OverviewSSEPayload = { devices?: DeviceOverviewItem[] }

function handleOverviewEvent(data: OverviewSSEPayload) {
  if (!data?.devices || !data.devices.length) return

  const found = data.devices[0]
  const idx = devices.value.findIndex(d => d.id === found.id)
  if (idx !== -1) {
    Object.assign(devices.value[idx], found)
    devices.value = [...devices.value]
  }

  if (selectedId.value === found.id) {
    selectedDetail.value = resolveDetailForDisplay(found as any)
    updateTrafficSpeedFromSelected()
  }

  loading.value = false
}

let overviewStream: ReturnType<typeof useEventStream<OverviewSSEPayload>> | null = null

function setupSSE() {
  if (overviewStream) {
    overviewStream.disconnect()
    overviewStream = null
  }
  realtimeTrafficActiveUntil.value = 0
  trafficSpeedRx.value = ''
  trafficSpeedTx.value = ''
  resetRollingTrafficWindow()

  const id = selectedId.value
  if (!id) {
    return
  }

  overviewStream = useEventStream<OverviewSSEPayload>({
    path: `/devices/${id}/overview/stream`,
    eventName: 'overview',
    reconnectDelayMs: 3000,
    parse: (payload: string) => JSON.parse(payload) as OverviewSSEPayload,
    onEvent: handleOverviewEvent,
    onRawEvent: (eventName: string, payload: string) => {
      if (eventName !== 'traffic') return
      try {
        handleRealtimeTrafficEvent(JSON.parse(payload) as RealtimeTrafficSnapshot)
      } catch {
        // Ignore a malformed realtime frame without tearing down the overview stream.
      }
    }
  })

  void overviewStream.connect()
}

watch(selectedId, () => {
  setupSSE()
})

onMounted(() => {
  setupSSE()
})

onBeforeUnmount(() => {
  overviewStream?.disconnect()
})

usePollingScheduler(async () => {
  if (activeTab.value !== 'overview') return
  if (!selectedDetail.value?.id || !selectedDetail.value.network_connected) return
  await fetchDeviceTrafficAnalysis(selectedDetail.value.id)
}, 60000, {
  immediate: false,
  maxIntervalMs: 300000,
  backgroundIntervalMs: 120000
})
</script>

<template>
  <div class="devices-page max-w-7xl mx-auto">
    <PageHeader title="设备管理" subtitle="查看设备信息、编辑配置、执行 AT 指令">
      <template #actions>
        <div class="flex items-center gap-2">
          <RefreshButton :loading="loading" @click="fetchAll" />
          <el-button @click="rescanDevices" :loading="rescanning" class="ui-glass-border !border-0">
            <el-icon><ArrowSync24Regular /></el-icon>
            重新扫描
          </el-button>
          <el-button type="primary" @click="openAddDialog" class="!border-0">
            <el-icon><Add24Regular /></el-icon>
            添加设备
          </el-button>
        </div>
      </template>
    </PageHeader>

    <ErrorState
      v-if="loadError"
      class="mb-6"
      title="设备数据加载失败"
      :message="loadError.message"
      :status-code="loadError.status"
      :request-method="loadError.method"
      :request-url="loadError.url"
      :last-success-at="loadLastOkAt"
      retry-text="重试"
      @retry="fetchAll"
    />

    <div class="devices-layout">
      <DeviceListPanel
        :loading="loading"
        :query="query"
        :status-filter="statusFilter"
        :sort-key="sortKey"
        :sort-dir="sortDir"
        :selected-id="selectedId"
        :filtered-devices="filteredDevices"
        :device-count="devices.length"
        :device-limit="deviceLimit"
        @update:query="query = $event"
        @update:status-filter="statusFilter = $event"
        @update:sort-key="sortKey = $event"
        @update:sort-dir="sortDir = $event"
        @select-device="selectDevice"
      />

      <div v-if="selectedDevice" class="space-y-6">
        <DeviceDetailHeader
          :device="selectedDevice"
          :rotating="rotating"
          :rebooting="rebooting"
          :reconnectingVoWiFi="reconnectingVoWiFi"
          @copy-text="copyText"
          @rotate-ip="rotateIP"
          @reconnect-vowifi="reconnectVoWiFi"
          @reboot-modem="rebootModem"
          @open-sms="openSms"
        />

        <div class="ui-card p-6">
          <el-tabs v-model="activeTab" class="device-detail-tabs">
            <el-tab-pane label="概览" name="overview">
              <div class="space-y-6">
                <DeviceOverviewTab
                  :device="selectedDevice"
                  :sim-operator-display="selectedSimOperatorDisplay"
                  :traffic-speed-rx="trafficSpeedRx"
                  :traffic-speed-tx="trafficSpeedTx"
                  :traffic-minute-rx="rollingMinuteRx"
                  :traffic-minute-tx="rollingMinuteTx"
                  :e911-starting="e911Starting"
                  @setup-e911="openE911Websheet"
                />
                <TrafficAnalysisPanel
                  :analysis="deviceAnalysis"
                  :loading="deviceAnalysisLoading"
                  :error="deviceAnalysisError"
                  :last-ok-at="deviceAnalysisLastOkAt"
                  :range="deviceAnalysisRange"
                  mode="device"
                  title="当前设备流量分析"
                  subtitle="数据每分钟采样一次，按日/周/月聚合"
                  :disabled="!selectedDevice?.network_connected"
                  :device-label="selectedDevice?.name || selectedDevice?.id"
                  @update:range="handleDeviceTrafficRangeChange"
                  @refresh="refreshCurrentDeviceTrafficAnalysis"
                />
              </div>
            </el-tab-pane>
            <el-tab-pane label="eSIM" name="esim" lazy>
              <DeviceEsimTab :device-id="selectedDevice.id" :device-imei="selectedDevice.modem?.imei || ''" :is-active="activeTab === 'esim'" :device-online="selectedDevice.running === true" />
            </el-tab-pane>
            <el-tab-pane label="AT 终端" name="at" lazy>
              <DeviceAtTab
                :device-id="selectedDevice.id"
                :backend-mode="selectedDevice.backend_mode"
                :at-port="selectedDevice.at_port"
                :running="selectedDevice.running"
              />
            </el-tab-pane>
            <el-tab-pane label="USSD 终端" name="ussd" lazy>
              <DeviceUssdTab :device-id="selectedDevice.id" />
            </el-tab-pane>
            <el-tab-pane label="卡策略" name="card" lazy>
              <CardPolicyPanel
                :device-id="selectedDevice.id"
                :iccid="selectedDetail?.modem?.iccid"
                :policy="cardPolicy"
                :device-online="selectedDevice.running === true"
                @policy-changed="onCardPolicyChanged"
              />
            </el-tab-pane>
            <el-tab-pane label="配置" name="config" lazy>
              <DeviceConfigTab
                :edit-config="editConfig"
                :device-status="selectedDetail"
                :saving="saving"
                :deleting="deleting"
                @save="saveConfig"
                @delete="deleteDevice"
              />
            </el-tab-pane>
          </el-tabs>
        </div>
      </div>

      <div v-else>
        <DeviceDetailLoading v-if="loading" />
        <div v-else class="ui-card p-8 text-gray-500 dark:text-gray-400">
          暂无设备
        </div>
      </div>
    </div>
  </div>

  <DeviceAddDialog
    v-model="addDialogOpen"
    :discovering="discovering"
    :unconfigured-discovered="addableDiscovered"
    :add-selected="addSelected"
    :add-config="addConfig"
    :add-saving="addSaving"
    @select-device="selectDiscoveredForAdd"
    @save="addDevice"
  />
  <CarrierWebsheetDialog
    v-model="e911WebsheetOpen"
    :websheet="e911Websheet"
    @done="finishE911Websheet"
  />
</template>

<style scoped>
.devices-page {
  container-type: inline-size;
}

.devices-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 1.5rem;
}

@container (min-width: 980px) {
  .devices-layout {
    grid-template-columns: 270px minmax(0, 1fr);
  }
}

.device-detail-tabs :deep(.el-tabs__content) {
  overflow: visible;
}

.device-detail-tabs :deep(.el-tab-pane) {
  padding-bottom: 0.25rem;
}
</style>
