<script setup lang="ts">
import { ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Edit20Regular, Eye24Regular, EyeOff24Regular, Settings24Regular } from '@vicons/fluent'
import type { DeviceOverviewItem } from '../types/api'
import { useSensitiveVisibility } from '../composables/useSensitiveVisibility'
import { devicesService } from '../services/devices'
import { errorMessage } from '../services/http'
import { copyToClipboard } from '../utils/clipboard'
import { activeEsimProfileDisplayName } from './deviceOverviewActiveEsim'
import { isControlOnline, isRadioRegistered, isRecoveryPhase, lifecycleStatusLabel } from '../utils/deviceLifecycle'
import StatusLight from './StatusLight.vue'
import OperatorSelectionDialog from './OperatorSelectionDialog.vue'
import type { StatusLightTone } from './statusLight'

const props = defineProps<{
  device: DeviceOverviewItem | null
  simOperatorDisplay: string
  trafficSpeedRx: string
  trafficSpeedTx: string
  trafficMinuteRx: string
  trafficMinuteTx: string
  e911Starting: boolean
}>()

const emit = defineEmits<{
  'setup-e911': []
  'refresh': []
}>()

const showSensitive = useSensitiveVisibility()
const showOperatorSelection = ref(false)
const localPhoneSaving = ref(false)

const trafficStateLabel = computed(() => {
  const status = props.device?.traffic_meta?.status
  if (status === 'waiting_sample') return '等待采样'
  if (status === 'stale') return '采样中断'
  return ''
})

function trafficDisplay(value: string | undefined) {
  return trafficStateLabel.value || value
}

const trafficRxDisplay = computed(() => props.trafficMinuteRx || trafficDisplay(props.device?.traffic?.rx))
const trafficTxDisplay = computed(() => props.trafficMinuteTx || trafficDisplay(props.device?.traffic?.tx))
const trafficDownloadRateDisplay = computed(() => props.trafficSpeedRx || trafficDisplay(props.device?.traffic?.rate) || '--')
const trafficUploadRateDisplay = computed(() => props.trafficSpeedTx || trafficStateLabel.value || '--')

// 次要字段折叠状态（VoWiFi 模式）
const showVowifiDetail = ref(false)

// ---- VoWiFi 模式计算属性 ----

const readinessItems = computed(() => {
  const rt = props.device?.vowifi_runtime
  return [
    { key: 'SIM',    ready: rt?.sim_ready },
    { key: 'Access', ready: rt?.access_ready },
    { key: 'Tunnel', ready: rt?.tunnel_ready },
    { key: 'IMS',    ready: rt?.ims_ready },
    { key: 'SMS',    ready: rt?.sms_ready },
  ]
})

// 'ok' | 'partial' | 'off'
const vowifiStatus = computed<'ok' | 'partial' | 'off'>(() => {
  const rt = props.device?.vowifi_runtime
  if (!rt) return 'off'
  const all = [rt.sim_ready, rt.access_ready, rt.tunnel_ready, rt.ims_ready, rt.sms_ready]
  if (all.every(Boolean)) return 'ok'
  if (all.some(Boolean)) return 'partial'
  return 'off'
})

// 未就绪的环节名称列表
const notReadyNames = computed(() =>
  readinessItems.value.filter(i => !i.ready).map(i => i.key)
)

// 有错误时自动展开
const hasError = computed(() =>
  !!(props.device?.vowifi_runtime?.last_error_class || props.device?.vowifi_runtime?.last_reason)
)

// ---- 窝蜂模式计算属性 ----

function hasValidSignalDbm(dbm: number | null | undefined): dbm is number {
  return typeof dbm === 'number' && Number.isFinite(dbm) && dbm !== 0 && dbm !== -999
}

// 0-5 格
const signalLevel = computed<number>(() => {
  const dbm = props.device?.modem?.signal_dbm
  if (!hasValidSignalDbm(dbm)) return 0
  if (dbm >= -75)  return 5
  if (dbm >= -85)  return 4
  if (dbm >= -95)  return 3
  if (dbm >= -105) return 2
  return 1
})

const signalColor = computed<'green' | 'amber' | 'red' | 'gray'>(() => {
  const dbm = props.device?.modem?.signal_dbm
  if (!hasValidSignalDbm(dbm)) return 'gray'
  if (dbm >= -85)  return 'green'
  if (dbm >= -100) return 'amber'
  return 'red'
})

const signalColorClass = computed(() => ({
  green: 'text-emerald-500 dark:text-emerald-400',
  amber: 'text-amber-500 dark:text-amber-400',
  red:   'text-red-500 dark:text-red-400',
  gray:  'text-gray-400 dark:text-gray-500',
}[signalColor.value]))

const signalBarColor = computed(() => ({
  green: 'bg-emerald-500',
  amber: 'bg-amber-500',
  red:   'bg-red-500',
  gray:  'bg-gray-300 dark:bg-gray-600',
}[signalColor.value]))

const flightModeEnabled = computed(() => {
  if (props.device?.vowifi_active) return true
  const mode = props.device?.modem?.operating_mode
  return mode === 0 || mode === 4
})

const vowifiStatusTone = computed<StatusLightTone>(() => {
  if (vowifiStatus.value === 'partial') return 'warning'
  return vowifiStatus.value === 'ok' ? 'success' : 'danger'
})

const flightModeStatusText = computed(() => {
  return flightModeEnabled.value ? '是' : '否'
})

const activeEsimProfileName = computed(() => activeEsimProfileDisplayName(props.device))

const localPhoneDisplay = computed(() => {
  const phone = props.device?.local_phone?.trim()
  return phone || '--'
})

const canCopyLocalPhone = computed(() => {
  return localPhoneDisplay.value !== '--' && localPhoneDisplay.value !== '---'
})

const localPhoneTitle = computed(() => {
  if (!showSensitive.value || !canCopyLocalPhone.value) return ''
  return localPhoneDisplay.value
})

async function copyLocalPhone() {
  if (!canCopyLocalPhone.value) return
  await copyToClipboard(localPhoneDisplay.value, '已复制')
}

async function editLocalPhone() {
  const deviceId = props.device?.id
  if (!deviceId || localPhoneSaving.value) return

  const current = props.device?.local_phone?.trim() || ''
  const promptResult = await ElMessageBox.prompt(
    '请输入本机号码，留空可清除手动号码。',
    '编辑本机号码',
    {
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputValue: current,
      inputPlaceholder: '例如 +8613800138000',
      inputPattern: /^\+?[0-9\s().-]{0,32}$/,
      inputErrorMessage: '请输入有效号码，最多 32 个字符'
    }
  ).catch(() => null)
  if (!promptResult) return

  const phoneNumber = String(promptResult.value ?? '').trim()
  if (phoneNumber === current) return

  localPhoneSaving.value = true
  try {
    const result = await devicesService.setLocalPhone(deviceId, phoneNumber)
    if (!result.ok) throw result.error
    ElMessage.success(phoneNumber ? '本机号码已保存' : '本机号码已清除')
    emit('refresh')
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '保存本机号码失败'))
  } finally {
    localPhoneSaving.value = false
  }
}

const controlOnline = computed(() => isControlOnline(props.device))

const isRegistered = computed(() => isRadioRegistered(props.device))

const registrationModeText = computed(() => {
  if (!isRegistered.value) return ''
  return props.device?.registration_state_label === 'roaming' ? '漫游驻网' : '归属地驻网'
})

const cellularStatusTone = computed<StatusLightTone>(() => {
  if (isRecoveryPhase(props.device?.lifecycle_phase)) return 'warning'
  if (!controlOnline.value) return 'danger'
  return isRegistered.value ? 'success' : 'warning'
})

const cellularStatusText = computed(() => {
  const phaseText = lifecycleStatusLabel(props.device?.lifecycle_phase)
  if (phaseText && props.device?.lifecycle_phase !== 'online' && props.device?.lifecycle_phase !== 'offline') return phaseText
  if (!controlOnline.value) return props.device?.running ? '控制面恢复中' : '离线'
  if (isRegistered.value) return ''
  if (props.device?.registration_state_label === 'searching') return '搜索网络中'
  if (props.device?.registration_state_label === 'denied') return '驻网被拒'
  return '未驻网'
})

const networkPanelMessage = computed(() => {
  if (!props.device?.network_enabled) return '数据未开启'
  if (!props.device?.network_connected) return '数据网络未连接'
  return ''
})

</script>

<template>
  <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">

    <!-- ===== 运行状态面板 ===== -->
    <div class="ui-panel-muted p-4">
      <div class="text-xs font-bold text-gray-500 uppercase tracking-wider mb-3">运行状态</div>

      <!-- ── VoWiFi 模式 ── -->
      <template v-if="device?.vowifi_enabled">

        <!-- Hero pill -->
        <div
          class="flex items-center gap-2.5 rounded-xl px-3.5 py-2.5 mb-3 border"
          :class="{
            'bg-emerald-50 border-emerald-200 dark:bg-emerald-500/10 dark:border-emerald-500/25': vowifiStatus === 'ok',
            'bg-amber-50 border-amber-200 dark:bg-amber-500/10 dark:border-amber-500/25':         vowifiStatus === 'partial',
            'bg-red-50 border-red-200 dark:bg-red-500/10 dark:border-red-500/25':                 vowifiStatus === 'off',
          }"
        >
          <!-- 状态点 -->
          <StatusLight :tone="vowifiStatusTone" size="sm" :animated="vowifiStatus !== 'off'" />
          <div class="min-w-0">
            <div class="text-sm font-bold leading-tight" :class="{
              'text-emerald-700 dark:text-emerald-300': vowifiStatus === 'ok',
              'text-amber-700 dark:text-amber-300':     vowifiStatus === 'partial',
              'text-red-700 dark:text-red-300':         vowifiStatus === 'off',
            }">
              <template v-if="vowifiStatus === 'ok'">WiFi-Calling · 全部就绪</template>
              <template v-else-if="vowifiStatus === 'partial'">{{ notReadyNames.join(' · ') }} 未就绪</template>
              <template v-else>VoWiFi 未连接</template>
            </div>
            <div v-if="vowifiStatus === 'partial' && device?.vowifi_runtime?.last_reason"
              class="text-xs text-amber-600 dark:text-amber-400 mt-0.5 truncate">
              {{ device.vowifi_runtime.last_reason }}
            </div>
          </div>
        </div>

        <!-- Readiness 进度条 -->
        <div class="mb-3">
          <div class="flex gap-1 mb-1">
            <div
              v-for="item in readinessItems" :key="item.key"
              class="flex-1 h-1.5 rounded-full"
              :class="item.ready === true  ? 'bg-emerald-500 dark:bg-emerald-400'
                    : item.ready === false ? 'bg-red-500 dark:bg-red-400'
                    : 'bg-gray-200 dark:bg-white/10'"
            />
          </div>
          <div class="flex justify-between">
            <span
              v-for="item in readinessItems" :key="item.key"
              class="flex-1 text-center text-[10px]"
              :class="item.ready === false ? 'text-red-500 dark:text-red-400 font-bold' : 'text-gray-400 dark:text-gray-500'"
            >{{ item.key }}</span>
          </div>
        </div>

        <!-- 次要字段（有错误自动展开，否则可折叠） -->
        <div class="border border-gray-200 dark:border-white/10 rounded-lg overflow-hidden">
          <button
            class="w-full flex items-center justify-between px-3 py-2 text-xs text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-white/5 transition-colors"
            @click="showVowifiDetail = !showVowifiDetail"
          >
            <span class="font-bold uppercase tracking-wider">详情</span>
            <span>{{ showVowifiDetail || hasError ? '▴' : '▾' }}</span>
          </button>
          <div v-if="showVowifiDetail || hasError" class="px-3 pb-2 space-y-1.5 text-sm text-gray-700 dark:text-gray-200 border-t border-gray-100 dark:border-white/5 pt-2">
            <FieldRow label="数据平面" :value="device?.vowifi_runtime?.dataplane_mode || '--'" monospace />
            <FieldRow label="最后原因" :value="device?.vowifi_runtime?.last_reason || '--'" />
            <FieldRow label="错误分类" :value="device?.vowifi_runtime?.last_error_class || '--'" monospace copyable />
          </div>
        </div>
      </template>

      <!-- ── 窝蜂模式 ── -->
      <template v-else>

        <!-- 运营商 hero（与 VoWiFi pill 统一样式） -->
        <div class="flex items-center gap-2.5 rounded-xl px-3.5 py-2.5 mb-3 border"
          :class="isRegistered
            ? 'bg-emerald-50 border-emerald-200 dark:bg-emerald-500/10 dark:border-emerald-500/25'
            : controlOnline
              ? 'bg-amber-50 border-amber-200 dark:bg-amber-500/10 dark:border-amber-500/25'
              : 'bg-gray-100 border-gray-200 dark:bg-white/5 dark:border-white/10'"
        >
          <StatusLight :tone="cellularStatusTone" size="sm" :animated="isRegistered" />
          <div class="flex-1 min-w-0">
            <div class="text-sm font-bold leading-tight"
              :class="isRegistered
                ? 'text-emerald-700 dark:text-emerald-300'
                : controlOnline
                  ? 'text-amber-700 dark:text-amber-300'
                  : 'text-gray-500 dark:text-gray-400'"
            >
              <template v-if="isRegistered">
                {{ device?.modem?.operator || '--' }}
                <span v-if="device?.modem?.network_mode" class="opacity-70">· {{ [device?.modem?.network_duplex, device?.modem?.network_mode].filter(Boolean).join(' ') }}</span>
                <span v-if="registrationModeText" class="opacity-70">· {{ registrationModeText }}</span>
              </template>
              <template v-else>
                {{ cellularStatusText }}
              </template>
            </div>
          </div>
          <button @click="showOperatorSelection = true" class="p-1 rounded hover:bg-black/5 dark:hover:bg-white/10 transition-colors" title="网络选择设置">
            <Settings24Regular class="w-5 h-5 text-gray-500 dark:text-gray-400" />
          </button>
        </div>

        <!-- 信号大字 -->
        <div class="rounded-xl border border-gray-200 dark:border-white/10 px-3.5 py-3 mb-3">
          <div class="text-[10px] font-bold text-gray-400 uppercase tracking-wider mb-1.5">信号强度</div>
          <div class="flex items-center gap-3">
            <div>
              <div class="flex items-baseline gap-1">
                <span class="text-2xl font-extrabold tabular-nums leading-none" :class="signalColorClass">
                  {{ device?.modem?.signal_dbm ?? '--' }}
                </span>
                <span class="text-xs text-gray-400">dBm</span>
              </div>
              <div class="text-[10px] text-gray-400 mt-1">
                RSRP {{ device?.modem?.signal_rsrp ?? '--' }}
                &nbsp;·&nbsp;
                RSRQ {{ device?.modem?.signal_rsrq ?? '--' }}
                &nbsp;·&nbsp;
                SINR {{ device?.modem?.signal_sinr ?? '--' }}
                <template v-if="device?.modem?.nr5g_signal_sinr !== undefined">
                  &nbsp;·&nbsp;NR5G SINR {{ device?.modem?.nr5g_signal_sinr }}
                </template>
              </div>
            </div>
            <!-- 信号格 -->
            <div class="flex items-end gap-0.5 ml-auto" style="height: 28px">
              <div v-for="i in 5" :key="i"
                class="w-1.5 rounded-sm"
                :style="{ height: (i * 18 + 10) + '%' }"
                :class="i <= signalLevel ? signalBarColor : 'bg-gray-200 dark:bg-white/10'"
              />
            </div>
          </div>
        </div>

        <!-- 次要字段 -->
        <div class="space-y-1.5 text-sm text-gray-700 dark:text-gray-200">
          <FieldRow label="网络模式"  :value="[device?.modem?.network_duplex, device?.modem?.network_mode].filter(Boolean).join(' ') || '--'" monospace />
          <FieldRow label="频段"  :value="device?.modem?.radio_band || '--'" monospace />
          <FieldRow label="信道"  :value="device?.modem?.radio_channel ? String(device.modem.radio_channel) : '--'" monospace />
          <FieldRow label="注册状态"  :value="device?.modem?.reg_status_text || '--'" monospace />
        </div>
      </template>
    </div>

    <!-- ===== SIM / 设备面板（不变）===== -->
    <div class="ui-panel-muted p-4 relative min-w-0 overflow-hidden">
      <div class="flex items-center justify-between mb-2">
        <div class="text-xs font-bold text-gray-500 uppercase tracking-wider">SIM / 设备</div>
        <div class="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 cursor-pointer -mt-1 -mr-1" @click="showSensitive = !showSensitive">
          <el-icon size="18">
            <Eye24Regular v-if="showSensitive" />
            <EyeOff24Regular v-else />
          </el-icon>
        </div>
      </div>
      <div class="text-sm space-y-1.5 text-gray-700 dark:text-gray-200">
        <FieldRow label="IMEI"      :value="device?.modem?.imei"   :sensitive="!showSensitive" monospace copyable />
        <FieldRow label="ICCID"     :value="device?.modem?.iccid"  :sensitive="!showSensitive" monospace copyable />
        <FieldRow label="IMSI"      :value="device?.modem?.imsi"   :sensitive="!showSensitive" monospace copyable />
        <div class="flex w-full min-w-0 items-center justify-between gap-3 overflow-hidden">
          <span class="text-gray-500 shrink-0 whitespace-nowrap">本机号码</span>
          <div class="flex min-w-0 flex-1 items-center justify-end gap-1.5">
            <span
              class="block min-w-0 truncate text-right font-mono"
              :class="[
                canCopyLocalPhone ? 'cursor-pointer hover:underline' : '',
                !showSensitive ? 'blur-sm select-none transition-all' : ''
              ]"
              :title="localPhoneTitle"
              @click="copyLocalPhone"
            >
              {{ localPhoneDisplay }}
            </span>
            <el-tooltip content="编辑本机号码" placement="top">
              <el-button
                size="small"
                text
                circle
                :loading="localPhoneSaving"
                :disabled="!device?.id"
                class="!h-6 !w-6 !p-0 shrink-0"
                @click.stop="editLocalPhone"
              >
                <el-icon size="15">
                  <Edit20Regular />
                </el-icon>
              </el-button>
            </el-tooltip>
          </div>
        </div>
        <div v-if="device?.e911_setup_available" class="flex justify-between gap-3">
          <span class="text-gray-500">E911地址</span>
          <el-button
            size="small"
            type="primary"
            plain
            :loading="e911Starting"
            class="!border-0"
            @click="emit('setup-e911')"
          >
            设置
          </el-button>
        </div>
        <FieldRow v-if="activeEsimProfileName" label="当前eSIM" :value="activeEsimProfileName" monospace copyable />
        <FieldRow label="原运营商" :value="simOperatorDisplay" copyable />
        <FieldRow label="固件版本"      :value="device?.modem?.firmware" monospace copyable />
        <div class="flex justify-between gap-3">
          <span class="text-gray-500">飞行模式</span>
          <span>{{ flightModeStatusText }}</span>
        </div>
        <FieldRow label="运行模式"  :value="device?.backend_mode === 'qmi' ? 'QMI' : device?.backend_mode === 'mbim' ? 'MBIM' : device?.backend_mode === 'at' ? 'AT' : 'Auto'" monospace />
      </div>
    </div>

    <!-- ===== 流量面板（不变）===== -->
    <div class="ui-panel-muted p-4">
      <div class="text-xs font-bold text-gray-500 uppercase tracking-wider mb-2">网络</div>
      <div v-if="networkPanelMessage" class="flex items-center justify-center p-6 text-sm text-gray-400">
        {{ networkPanelMessage }}
      </div>
      <div v-else class="text-sm space-y-1.5 text-gray-700 dark:text-gray-200">
        <FieldRow label="内网 IPv4"     :value="device?.private_ip"           monospace copyable />
        <FieldRow label="内网 IPv6"   :value="device?.private_ipv6"         monospace copyable />
        <FieldRow label="外网 IPv4"     :value="device?.public_ip"            monospace copyable />
        <FieldRow label="外网 IPv6"   :value="device?.public_ipv6"          monospace copyable />
        <FieldRow label="近1分钟上传" :value="trafficTxDisplay"             monospace />
        <FieldRow label="近1分钟下载" :value="trafficRxDisplay"             monospace />
        <FieldRow label="实时下载速率"    :value="trafficDownloadRateDisplay"   monospace />
        <FieldRow label="实时上传速率"    :value="trafficUploadRateDisplay"     monospace />
      </div>
    </div>

    <!-- 运营商选择弹窗 -->
    <OperatorSelectionDialog
      v-if="device?.id"
      v-model="showOperatorSelection"
      :device-id="device.id"
      @updated="emit('refresh')"
    />
  </div>
</template>
