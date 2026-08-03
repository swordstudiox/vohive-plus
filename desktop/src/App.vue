<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { runtimeService } from './services/runtime'
import type { ActionResult, RuntimeStatus } from './types/runtime'

const status = ref<RuntimeStatus | null>(null)
const logs = ref<string[]>([])
const busy = ref('')
const error = ref('')
const notice = ref('')
const suggestedAdminCommand = ref('')

const targetDevice = computed(() => status.value?.devices.find((d) => d.is_target))
const healthText = computed(() => status.value?.health.ok ? '正常' : status.value?.health.message || '未知')

async function refresh(clearNotice = true) {
  if (clearNotice) {
    error.value = ''
    notice.value = ''
    suggestedAdminCommand.value = ''
  }
  status.value = await runtimeService.status()
  logs.value = await runtimeService.logs()
}

async function runAction(name: string, fn: () => Promise<ActionResult>) {
  busy.value = name
  error.value = ''
  notice.value = ''
  suggestedAdminCommand.value = ''
  try {
    const result = await fn()
    if (!result.ok) {
      error.value = result.message
      suggestedAdminCommand.value = result.suggested_admin_command || ''
    } else {
      notice.value = result.message
    }
    if (result.status) {
      status.value = result.status
    }
    await refresh(false)
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    busy.value = ''
  }
}

onMounted(() => refresh())
</script>

<template>
  <main class="shell">
    <section class="topbar">
      <div>
        <h1>VoHive Plus</h1>
        <p>Windows 桌面运行壳 · WSL2 路线</p>
      </div>
      <button :disabled="!!busy" @click="refresh()">刷新</button>
    </section>

    <section v-if="status" class="grid">
      <div class="panel">
        <h2>运行环境</h2>
        <div class="row"><span>WSL2</span><b>{{ status.wsl.available ? '可用' : '不可用' }}</b></div>
        <div class="hint">{{ status.wsl.path || status.wsl.message }}</div>
        <button :disabled="!!busy || !status.wsl.available" @click="runAction('启动 WSL', runtimeService.startWsl)">启动 WSL</button>
        <div class="row"><span>usbipd-win</span><b>{{ status.usbipd.available ? '可用' : '不可用' }}</b></div>
        <div class="hint">{{ status.usbipd.path || status.usbipd.message }}</div>
      </div>

      <div class="panel">
        <h2>USB 模组</h2>
        <template v-if="targetDevice">
          <div class="row"><span>{{ targetDevice.vid_pid }}</span><b>{{ targetDevice.state }}</b></div>
          <div class="hint">{{ targetDevice.busid }} · {{ targetDevice.device }}</div>
        </template>
        <p v-else class="hint">未发现 2ca3:4006 Baiwang</p>
        <button :disabled="!!busy" @click="runAction('USB', runtimeService.attachUsb)">连接 USB 到 WSL</button>
        <button :disabled="!!busy" @click="runAction('准备 USB', runtimeService.prepareUsb)">准备 WSL USB</button>
      </div>

      <div class="panel">
        <h2>后端</h2>
        <div class="row"><span>进程</span><b>{{ status.backend.running ? '运行中' : '未运行' }}</b></div>
        <div v-if="status.backend.message" class="hint">{{ status.backend.message }}</div>
        <div class="row"><span>健康检查</span><b>{{ healthText }}</b></div>
        <div class="actions">
          <button :disabled="!!busy" @click="runAction('启动', runtimeService.start)">启动</button>
          <button :disabled="!!busy" @click="runAction('停止', runtimeService.stop)">停止</button>
          <button :disabled="!!busy || !status.health.ok" @click="runAction('打开 Web', runtimeService.openWeb)">打开 Web</button>
        </div>
      </div>
    </section>

    <p v-if="busy" class="busy">正在执行：{{ busy }}</p>
    <p v-if="notice" class="notice">{{ notice }}</p>
    <p v-if="error" class="error">{{ error }}</p>
    <pre v-if="suggestedAdminCommand" class="command">{{ suggestedAdminCommand }}</pre>

    <section class="panel log-panel">
      <h2>诊断日志</h2>
      <pre>{{ logs.join('\n') }}</pre>
    </section>
  </main>
</template>
