import { ref, watch, type Ref } from 'vue'

// 卡策略四开关镜像（不含 ip/apn——那两项仅在独立"卡策略"页编辑）
export type PolicyMirror = {
  network_enabled: boolean
  vowifi_enabled: boolean
  airplane_enabled: boolean
  roaming_enabled: boolean
}

export type ToggleResult = { ok: boolean; message?: string }

// 执行器接收开关目标值 enabled 与互斥后的完整目标镜像 next。
// live 消费方只用 enabled（调设备动作端点）；stored 消费方 PUT 整个 next 镜像。
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
  const networkError = ref('')
  const vowifiError = ref('')
  const airplaneError = ref('')
  const roamingError = ref('')

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
      networkError.value = ''
      vowifiError.value = ''
      airplaneError.value = ''
      roamingError.value = ''
    },
    { immediate: true }
  )

  function applyLocal(next: PolicyMirror) {
    local.value.network_enabled = next.network_enabled
    local.value.vowifi_enabled = next.vowifi_enabled
    local.value.airplane_enabled = next.airplane_enabled
    local.value.roaming_enabled = next.roaming_enabled
  }

  function errorMessage(err: unknown) {
    if (err instanceof Error) return err.message
    if (err && typeof err === 'object' && typeof (err as { message?: unknown }).message === 'string') {
      return (err as { message: string }).message
    }
    return String(err || '未生效')
  }

  async function runToggle(
    rawVal: string | number | boolean,
    field: keyof PolicyMirror,
    pending: Ref<boolean>,
    failed: Ref<boolean>,
    error: Ref<string>,
    apply: (enabled: boolean, next: PolicyMirror) => Promise<ToggleResult>
  ) {
    const val = rawVal as boolean
    pending.value = true
    failed.value = false
    error.value = ''
    const next = nextMirror(local.value, field, val)
    try {
      const result = await apply(val, next)
      if (!result.ok) {
        local.value[field] = !val
        failed.value = true
        error.value = result.message || '未生效'
        return
      }
      applyLocal(next)
      executors.onChanged?.()
    } catch (err) {
      local.value[field] = !val
      failed.value = true
      error.value = errorMessage(err)
    } finally {
      pending.value = false
    }
  }

  async function onNetworkToggle(rawVal: string | number | boolean) {
    await runToggle(rawVal, 'network_enabled', networkPending, networkFailed, networkError, executors.applyNetwork)
  }

  async function onVoWiFiToggle(rawVal: string | number | boolean) {
    await runToggle(rawVal, 'vowifi_enabled', vowifiPending, vowifiFailed, vowifiError, executors.applyVoWiFi)
  }

  async function onAirplaneToggle(rawVal: string | number | boolean) {
    await runToggle(rawVal, 'airplane_enabled', airplanePending, airplaneFailed, airplaneError, executors.applyAirplane)
  }

  async function onRoamingToggle(rawVal: string | number | boolean) {
    await runToggle(rawVal, 'roaming_enabled', roamingPending, roamingFailed, roamingError, executors.applyRoaming)
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
    networkError,
    vowifiError,
    airplaneError,
    roamingError,
    onNetworkToggle,
    onVoWiFiToggle,
    onAirplaneToggle,
    onRoamingToggle
  }
}
