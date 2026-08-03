<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Sim24Regular } from '@vicons/fluent'
import { Loading } from '@element-plus/icons-vue'
import type { CardPolicy } from '../types/api'
import { devicesService } from '../services/devices'
import { useCardPolicyToggles, type PolicyMirror } from '../composables/useCardPolicyToggles'

const props = defineProps<{
  deviceId: string | undefined
  iccid: string | undefined
  policy: CardPolicy | null
  deviceOnline: boolean
}>()

const emit = defineEmits<{
  policyChanged: []
}>()

// ip/apn 仍由本组件独立持有（不进 composable）
const ipVersion = ref<'v4' | 'v6' | 'v4v6'>('v4')
const apn = ref('')

function samePolicyICCID(a: string | undefined, b: string | undefined) {
  const normalize = (v: string | undefined) => String(v || '').trim().replace(/^"|"$/g, '').replace(/[Ff]+$/g, '')
  return normalize(a) !== '' && normalize(a) === normalize(b)
}

const policyMatchesCurrentCard = computed(() => samePolicyICCID(props.policy?.iccid, props.iccid))
const canToggle = computed(() => props.deviceOnline && !!props.iccid && policyMatchesCurrentCard.value)

// 上游 policy → 四开关镜像（喂给 composable）
const mirror = computed<PolicyMirror | null>(() =>
  props.policy
    ? {
        network_enabled: props.policy.network_enabled,
        vowifi_enabled: props.policy.vowifi_enabled,
        airplane_enabled: props.policy.airplane_enabled,
        roaming_enabled: props.policy.roaming_enabled
      }
    : null
)

// 同步 ip/apn（这两项不参与 composable）
watch(
  () => props.policy,
  (p) => {
    if (!p) return
    ipVersion.value = p.ip_version || 'v4'
    apn.value = p.apn || ''
  },
  { immediate: true }
)

// live 执行器：调设备动作端点，即时生效。network 携带本组件的 ip/apn。
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
  async applyNetwork(enabled) {
    if (!props.deviceId) return { ok: false }
    const r = enabled
      ? await devicesService.startNetwork(props.deviceId, { ip_version: ipVersion.value, apn: apn.value })
      : await devicesService.stopNetwork(props.deviceId)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyVoWiFi(enabled) {
    if (!props.deviceId) return { ok: false }
    const r = enabled
      ? await devicesService.enableVoWiFi(props.deviceId)
      : await devicesService.disableVoWiFi(props.deviceId)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyAirplane(enabled) {
    if (!props.deviceId) return { ok: false }
    const r = await devicesService.setFlightMode(props.deviceId, enabled)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  async applyRoaming(enabled) {
    if (!props.deviceId) return { ok: false }
    const r = await devicesService.setRoaming(props.deviceId, enabled)
    return { ok: r.ok, message: r.ok ? undefined : r.error.message }
  },
  onChanged() {
    emit('policyChanged')
  }
})

const sourceLabel = computed(() => {
  if (!props.policy) return ''
  return props.policy.source === 'user' ? '手动设置' : '自动默认'
})
</script>

<template>
  <div>
    <!-- 标题行 -->
    <div class="flex items-center gap-3 mb-4">
      <div class="w-10 h-10 rounded-xl bg-violet-50 dark:bg-violet-500/10 flex items-center justify-center text-violet-600 dark:text-violet-400">
        <el-icon size="22"><Sim24Regular /></el-icon>
      </div>
      <div>
        <div class="text-lg font-bold text-gray-900 dark:text-white">卡策略</div>
        <div class="text-xs text-gray-500 dark:text-gray-400">网络/VoWiFi 开关跟着 SIM 卡走，切换即时生效</div>
      </div>
    </div>

    <!-- 无 ICCID 提示 -->
    <div v-show="!iccid" class="ui-panel-muted p-4 text-center text-sm text-gray-500 dark:text-gray-400">
      设备尚未识别到 SIM 卡 ICCID，策略不可操作
    </div>

    <!-- 离线提示（有 ICCID 但设备离线） -->
    <div v-show="iccid && !deviceOnline" class="mb-3 px-3 py-2 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 text-xs text-yellow-700 dark:text-yellow-300">
      设备离线，策略仅展示，切换操作已禁用
    </div>

    <!-- 用 v-show 让 el-switch 始终挂载，避免 element-plus 2.13 在挂载前访问未就绪 input 而崩溃 -->
    <div v-show="iccid" class="space-y-3">
      <!-- ICCID + 来源 -->
      <div class="ui-panel-muted p-3 flex items-center justify-between">
        <div>
          <div class="text-xs font-bold text-gray-500 uppercase tracking-wider mb-0.5">当前卡 ICCID</div>
          <div class="text-sm font-mono text-gray-800 dark:text-gray-100">{{ iccid }}</div>
        </div>
        <el-tag v-if="sourceLabel" :type="policy?.source === 'user' ? 'primary' : 'info'" size="small">{{ sourceLabel }}</el-tag>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
                <!-- IP 版本 -->
        <div class="space-y-1">
          <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">IP 版本</label>
          <el-select v-model="ipVersion" class="w-full" :disabled="!canToggle">
            <el-option label="IPv4" value="v4" />
            <el-option label="IPv6" value="v6" />
            <el-option label="IPv4 + IPv6（双栈）" value="v4v6" />
          </el-select>
          <div class="text-xs text-gray-400">下次开启网络时生效</div>
        </div>

        <!-- APN -->
        <div class="space-y-1">
          <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">APN（可选）</label>
          <el-input v-model="apn" placeholder="留空自动识别" :disabled="!canToggle" />
          <div class="text-xs text-gray-400">下次开启网络时生效</div>
        </div>
        <!-- 开启网络 -->
        <div
          class="ui-panel-muted p-3 space-y-1"
          :class="local.network_enabled ? 'border border-emerald-300 bg-emerald-50/50 dark:bg-emerald-900/20' : ''"
        >
          <div class="flex items-center justify-between">
            <div>
              <div class="text-sm font-bold text-gray-800 dark:text-gray-100">开启网络</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">VoWiFi/飞行开启时不可用</div>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="networkFailed" class="text-xs text-orange-500 dark:text-orange-400">{{ networkError }}</span>
              <el-icon v-if="networkPending" class="animate-spin text-gray-400"><Loading /></el-icon>
              <el-switch
                v-model="local.network_enabled"
                :disabled="!canToggle || local.vowifi_enabled || local.airplane_enabled || networkPending"
                @change="onNetworkToggle"
              />
            </div>
          </div>
        </div>

        <!-- VoWiFi -->
        <div
          class="ui-panel-muted p-3 space-y-1"
          :class="local.vowifi_enabled ? 'border border-orange-300 bg-orange-50/50 dark:bg-orange-900/20' : ''"
        >
          <div class="flex items-center justify-between">
            <div>
              <div class="text-sm font-bold text-gray-800 dark:text-gray-100">VoWiFi</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">启用后进飞行模式，不支持国内运营商</div>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="vowifiFailed" class="text-xs text-orange-500 dark:text-orange-400">{{ vowifiError }}</span>
              <el-icon v-if="vowifiPending" class="animate-spin text-gray-400"><Loading /></el-icon>
              <el-switch
                v-model="local.vowifi_enabled"
                :disabled="!canToggle || vowifiPending"
                @change="onVoWiFiToggle"
              />
            </div>
          </div>
        </div>

        <!-- 飞行模式 -->
        <div
          class="ui-panel-muted p-3 space-y-1"
          :class="local.airplane_enabled ? 'border border-sky-300 bg-sky-50/50 dark:bg-sky-900/20' : ''"
        >
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <div>
                <div class="text-sm font-bold text-gray-800 dark:text-gray-100">飞行模式</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">射频关闭，断网；VoWiFi 开启时由其接管</div>
              </div>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="airplaneFailed" class="text-xs text-orange-500 dark:text-orange-400">{{ airplaneError }}</span>
              <el-icon v-if="airplanePending" class="animate-spin text-gray-400"><Loading /></el-icon>
              <el-switch
                v-model="local.airplane_enabled"
                :disabled="!canToggle || local.vowifi_enabled || airplanePending"
                @change="onAirplaneToggle"
              />
            </div>
          </div>
        </div>


        <!-- 允许漫游 -->
        <div
          class="ui-panel-muted p-3 space-y-1"
          :class="local.roaming_enabled ? 'border border-lime-300 bg-lime-50/50 dark:bg-lime-900/20' : ''"
        >
          <div class="flex items-center justify-between">
            <div>
              <div class="text-sm font-bold text-gray-800 dark:text-gray-100">允许漫游</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">关闭后模块不会注册漫游网络</div>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="roamingFailed" class="text-xs text-orange-500 dark:text-orange-400">{{ roamingError }}</span>
              <el-icon v-if="roamingPending" class="animate-spin text-gray-400"><Loading /></el-icon>
              <el-switch
                v-model="local.roaming_enabled"
                :disabled="!canToggle || roamingPending"
                @change="onRoamingToggle"
              />
            </div>
          </div>
        </div>

      </div>
    </div>
  </div>
</template>
