import { ref, watch, type Ref } from 'vue'

// 卡策略三开关镜像（不含 ip/apn——那两项仅在独立"卡策略"页编辑）
export type PolicyMirror = {
  network_enabled: boolean
  vowifi_enabled: boolean
  airplane_enabled: boolean
  roaming_enabled: boolean
}

export type ToggleResult = { ok: boolean }

// 执行器接收开关目标值 enabled 与互斥后的完整目标镜像 next。
// live 消费方只用 enabled（调设备动作端点）；stored 消费方 PUT 整个 next 三元组。
export type CardPolicyExecutors = {
  applyNetwork: (enabled: boolean, next: PolicyMirror) => Promise<ToggleResult>
  applyVoWiFi: (enabled: boolean, next: PolicyMirror) => Promise<ToggleResult>
  applyAirplane: (enabled: boolean, next: PolicyMirror) => Promise<ToggleResult>
  applyRoaming: (enabled: boolean, next: PolicyMirror) => Promise<ToggleResult>
  onChanged?: () => void
}

// 互斥规则（照搬现有面板语义）：
// 开网络 ⇒ 关 VoWiFi、关飞行
// 开 VoWiFi ⇒ 关网络（不动飞行意图，飞行意图独立存储）
// 开飞行 ⇒ 关网络、关 VoWiFi
// 关任一项 ⇒ 不动其它项
function nextMirror(
  cur: PolicyMirror,
  field: keyof PolicyMirror,
  val: boolean
): PolicyMirror {
  if (field === 'network_enabled') {
    return val
      ? { ...cur, network_enabled: true, vowifi_enabled: false, airplane_enabled: false }
      : { ...cur, network_enabled: false }
  }
  if (field === 'vowifi_enabled') {
    return val
      ? { ...cur, network_enabled: false, vowifi_enabled: true }
      : { ...cur, vowifi_enabled: false }
  }
  if (field === 'airplane_enabled') {
    return val
      ? { ...cur, network_enabled: false, vowifi_enabled: false, airplane_enabled: true }
      : { ...cur, airplane_enabled: false }
  }
  // roaming_enabled
  return val
    ? { ...cur, roaming_enabled: true }
    : { ...cur, roaming_enabled: false }
}

export function useCardPolicyToggles(
  source: Ref<PolicyMirror | null>,
  executors: CardPolicyExecutors
) {
  const local = ref<PolicyMirror>({
    network_enabled: false,
    vowifi_enabled: false,
    airplane_enabled: false,
    roaming_enabled: true
  })

  const networkPending = ref(false)
  const networkFailed = ref(false)
  const vowifiPending = ref(false)
  const vowifiFailed = ref(false)
  const airplanePending = ref(false)
  const airplaneFailed = ref(false)
  const roamingPending = ref(false)
  const roamingFailed = ref(false)

  // 上游变化原地同步各字段（不整体替换对象，避免 el-switch 在 element-plus 2.13 崩溃）
  watch(
    source,
    (p) => {
      if (!p) return
      local.value.network_enabled = p.network_enabled
      local.value.vowifi_enabled = p.vowifi_enabled
      local.value.airplane_enabled = p.airplane_enabled
      local.value.roaming_enabled = p.roaming_enabled
      networkFailed.value = false
      vowifiFailed.value = false
      airplaneFailed.value = false
      roamingFailed.value = false
    },
    { immediate: true }
  )

  async function onNetworkToggle(rawVal: string | number | boolean) {
    const val = rawVal as boolean
    networkPending.value = true
    networkFailed.value = false
    const next = nextMirror(local.value, 'network_enabled', val)
    const result = await executors.applyNetwork(val, next)
    networkPending.value = false
    if (!result.ok) {
      local.value.network_enabled = !val
      networkFailed.value = true
      return
    }
    local.value.network_enabled = next.network_enabled
    local.value.vowifi_enabled = next.vowifi_enabled
    local.value.airplane_enabled = next.airplane_enabled
    local.value.roaming_enabled = next.roaming_enabled
    executors.onChanged?.()
  }

  async function onVoWiFiToggle(rawVal: string | number | boolean) {
    const val = rawVal as boolean
    vowifiPending.value = true
    vowifiFailed.value = false
    const next = nextMirror(local.value, 'vowifi_enabled', val)
    const result = await executors.applyVoWiFi(val, next)
    vowifiPending.value = false
    if (!result.ok) {
      local.value.vowifi_enabled = !val
      vowifiFailed.value = true
      return
    }
    local.value.network_enabled = next.network_enabled
    local.value.vowifi_enabled = next.vowifi_enabled
    local.value.airplane_enabled = next.airplane_enabled
    local.value.roaming_enabled = next.roaming_enabled
    executors.onChanged?.()
  }

  async function onAirplaneToggle(rawVal: string | number | boolean) {
    const val = rawVal as boolean
    airplanePending.value = true
    airplaneFailed.value = false
    const next = nextMirror(local.value, 'airplane_enabled', val)
    const result = await executors.applyAirplane(val, next)
    airplanePending.value = false
    if (!result.ok) {
      local.value.airplane_enabled = !val
      airplaneFailed.value = true
      return
    }
    local.value.network_enabled = next.network_enabled
    local.value.vowifi_enabled = next.vowifi_enabled
    local.value.airplane_enabled = next.airplane_enabled
    local.value.roaming_enabled = next.roaming_enabled
    executors.onChanged?.()
  }

  async function onRoamingToggle(rawVal: string | number | boolean) {
    const val = rawVal as boolean
    roamingPending.value = true
    roamingFailed.value = false
    const next = nextMirror(local.value, 'roaming_enabled', val)
    const result = await executors.applyRoaming(val, next)
    roamingPending.value = false
    if (!result.ok) {
      local.value.roaming_enabled = !val
      roamingFailed.value = true
      return
    }
    local.value.network_enabled = next.network_enabled
    local.value.vowifi_enabled = next.vowifi_enabled
    local.value.airplane_enabled = next.airplane_enabled
    local.value.roaming_enabled = next.roaming_enabled
    executors.onChanged?.()
  }

  return {
    local,
    networkPending,
    networkFailed,
    vowifiPending,
    vowifiFailed,
    airplanePending,
    airplaneFailed,
    roamingPending,
    roamingFailed,
    onNetworkToggle,
    onVoWiFiToggle,
    onAirplaneToggle,
    onRoamingToggle
  }
}
