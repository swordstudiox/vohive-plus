<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import type { CardPolicy } from '../types/api'
import { cardsService } from '../services/cards'
import { devicesService } from '../services/devices'
import { useCardPolicyToggles, type PolicyMirror, type ToggleResult } from '../composables/useCardPolicyToggles'

const props = defineProps<{
  deviceId: string
  iccid: string
  isActiveCard: boolean
  deviceOnline: boolean
}>()

const emit = defineEmits<{
  policyChanged: []
}>()

const policy = ref<CardPolicy | null>(null)
const loadFailed = ref(false)
const loading = ref(false)

// 激活卡 + 设备在线 → live 热切换；否则 stored 存储（激活/上线后生效）
const mode = computed<'live' | 'stored'>(() =>
  props.isActiveCard && props.deviceOnline ? 'live' : 'stored'
)

const hint = computed(() => {
  if (mode.value === 'live') return ''
  if (!props.deviceOnline) return '设备离线，改动已保存，激活/上线后生效'
  return '改动将在此卡激活后生效'
})

const mirror = computed<PolicyMirror | null>(() =>
  policy.value
    ? {
        network_enabled: policy.value.network_enabled,
        vowifi_enabled: policy.value.vowifi_enabled,
        airplane_enabled: policy.value.airplane_enabled,
        roaming_enabled: policy.value.roaming_enabled
      }
    : null
)

async function loadPolicy() {
  loading.value = true
  loadFailed.value = false
  const r = await cardsService.getPolicy(props.iccid)
  loading.value = false
  if (r.ok) {
    policy.value = r.data
  } else {
    loadFailed.value = true
  }
}

onMounted(loadPolicy)

// stored 执行器：PUT 互斥后的完整开关镜像
async function putTriple(next: PolicyMirror): Promise<ToggleResult> {
  const r = await cardsService.putPolicy(props.iccid, {
    network_enabled: next.network_enabled,
    vowifi_enabled: next.vowifi_enabled,
    airplane_enabled: next.airplane_enabled,
    roaming_enabled: next.roaming_enabled
  })
  return { ok: r.ok, message: r.ok ? undefined : r.error.message }
}

const {
  local,
  networkPending,
  networkFailed,
  vowifiPending,
  vowifiFailed,
  airplanePending,
  airplaneFailed,
  roamingPending,
  roamingFailed,
  networkError,
  vowifiError,
  airplaneError,
  roamingError,
  onNetworkToggle,
  onVoWiFiToggle,
  onAirplaneToggle,
  onRoamingToggle
} = useCardPolicyToggles(mirror, {
  async applyNetwork(enabled, next) {
    if (mode.value === 'stored') return putTriple(next)
    const r = enabled
      ? await devicesService.startNetwork(props.deviceId, {
          ip_version: policy.value?.ip_version || 'v4',
          apn: policy.value?.apn || ''
        })
      : await devicesService.stopNetwork(props.deviceId)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyVoWiFi(enabled, next) {
    if (mode.value === 'stored') return putTriple(next)
    const r = enabled
      ? await devicesService.enableVoWiFi(props.deviceId)
      : await devicesService.disableVoWiFi(props.deviceId)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyAirplane(enabled, next) {
    if (mode.value === 'stored') return putTriple(next)
    const r = await devicesService.setFlightMode(props.deviceId, enabled)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyRoaming(enabled, next) {
    if (mode.value === 'stored') return putTriple(next)
    const r = await devicesService.setRoaming(props.deviceId, enabled)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  onChanged() {
    emit('policyChanged')
  }
})

const registrationAndSmsEnabled = computed({
  get: () => !local.value.airplane_enabled,
  set: (enabled: boolean) => {
    local.value.airplane_enabled = !enabled
  }
})

async function onRegistrationAndSmsToggle(rawVal: string | number | boolean) {
  await onAirplaneToggle(!(rawVal as boolean))
}
</script>

<template>
  <div class="px-4 py-3 bg-gray-50/60 dark:bg-white/5 rounded-lg space-y-3">
    <div v-if="loading" class="text-xs text-gray-400 flex items-center gap-1">
      <el-icon class="animate-spin"><Loading /></el-icon> 正在加载策略...
    </div>
    <div v-else-if="loadFailed" class="text-xs text-orange-500 flex items-center gap-2">
      策略加载失败
      <el-button size="small" text @click="loadPolicy">重试</el-button>
    </div>
    <template v-else>
      <div v-if="hint" class="text-[11px] text-amber-600 dark:text-amber-400">{{ hint }}</div>
      <div class="grid grid-cols-1 sm:grid-cols-4 gap-2">
        <!-- 蜂窝数据 -->
        <div class="flex items-center justify-between rounded-lg px-3 py-2 bg-white dark:bg-white/5">
          <span class="text-sm text-gray-700 dark:text-gray-200">蜂窝数据</span>
          <div class="flex items-center gap-2">
            <span v-if="networkFailed" class="text-xs text-orange-500">{{ networkError }}</span>
            <el-icon v-if="networkPending" class="animate-spin text-gray-400"><Loading /></el-icon>
            <el-switch
              v-model="local.network_enabled"
              :disabled="local.vowifi_enabled || local.airplane_enabled || networkPending"
              @change="onNetworkToggle"
            />
          </div>
        </div>
        <!-- VoWiFi -->
        <div class="flex items-center justify-between rounded-lg px-3 py-2 bg-white dark:bg-white/5">
          <span class="text-sm text-gray-700 dark:text-gray-200">VoWiFi</span>
          <div class="flex items-center gap-2">
            <span v-if="vowifiFailed" class="text-xs text-orange-500">{{ vowifiError }}</span>
            <el-icon v-if="vowifiPending" class="animate-spin text-gray-400"><Loading /></el-icon>
            <el-switch
              v-model="local.vowifi_enabled"
              :disabled="vowifiPending"
              @change="onVoWiFiToggle"
            />
          </div>
        </div>
        <!-- 驻网与短信 -->
        <div class="flex items-center justify-between rounded-lg px-3 py-2 bg-white dark:bg-white/5">
          <span class="text-sm text-gray-700 dark:text-gray-200">驻网与短信</span>
          <div class="flex items-center gap-2">
            <span v-if="airplaneFailed" class="text-xs text-orange-500">{{ airplaneError }}</span>
            <el-icon v-if="airplanePending" class="animate-spin text-gray-400"><Loading /></el-icon>
            <el-switch
              v-model="registrationAndSmsEnabled"
              :disabled="local.vowifi_enabled || airplanePending"
              @change="onRegistrationAndSmsToggle"
            />
          </div>
        </div>
        <!-- 数据漫游 -->
        <div class="flex items-center justify-between rounded-lg px-3 py-2 bg-white dark:bg-white/5">
          <span class="text-sm text-gray-700 dark:text-gray-200">数据漫游</span>
          <div class="flex items-center gap-2">
            <span v-if="roamingFailed" class="text-xs text-orange-500">{{ roamingError }}</span>
            <el-icon v-if="roamingPending" class="animate-spin text-gray-400"><Loading /></el-icon>
            <el-switch
              v-model="local.roaming_enabled"
              :disabled="roamingPending"
              @change="onRoamingToggle"
            />
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
