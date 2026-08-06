<script setup lang="ts">
import { computed } from 'vue'
import EmptyState from './EmptyState.vue'
import ListSkeleton from './ListSkeleton.vue'
import StatusLight from './StatusLight.vue'
import type { DeviceMgmtListItem } from '../types/api'
import { isControlOnline, isRadioRegistered, lifecycleStatusLabel, primaryLifecycleStatus } from '../utils/deviceLifecycle'

const props = defineProps<{
  loading: boolean
  query: string
  statusFilter: 'all' | 'online' | 'offline'
  sortKey: 'name' | 'signal'
  sortDir: 'asc' | 'desc'
  selectedId: string
  filteredDevices: DeviceMgmtListItem[]
  deviceCount: number
  deviceLimit: number
}>()

const emit = defineEmits<{
  'update:query': [value: string]
  'update:statusFilter': [value: 'all' | 'online' | 'offline']
  'update:sortKey': [value: 'name' | 'signal']
  'update:sortDir': [value: 'asc' | 'desc']
  'select-device': [id: string]
}>()

const modelQuery = computed({
  get: () => props.query,
  set: (value: string) => emit('update:query', value)
})

const modelStatusFilter = computed({
  get: () => props.statusFilter,
  set: (value: 'all' | 'online' | 'offline') => emit('update:statusFilter', value)
})



const modelSortKey = computed({
  get: () => props.sortKey,
  set: (value: 'name' | 'signal') => emit('update:sortKey', value)
})

const modelSortDir = computed({
  get: () => props.sortDir,
  set: (value: 'asc' | 'desc') => emit('update:sortDir', value)
})

const primaryStatus = primaryLifecycleStatus

const registrationText = (d: DeviceMgmtListItem) => {
  const phaseText = lifecycleStatusLabel(d.lifecycle_phase)
  if (phaseText && d.lifecycle_phase !== 'online' && d.lifecycle_phase !== 'offline') return phaseText
  if (isRadioRegistered(d)) {
    const mode = [d?.modem?.network_duplex, d?.modem?.network_mode].filter(Boolean).join(' ') || '--'
    const roaming = d.registration_state_label === 'roaming' ? '漫游' : '归属地'
    return `${d?.modem?.operator || '--'} · ${mode} · ${roaming}`
  }
  if (!isControlOnline(d)) return '控制面恢复中'
  if (d.registration_state_label === 'searching') return '搜索网络中'
  if (d.registration_state_label === 'denied') return '驻网被拒'
  return '未驻网'
}

const dataNetworkText = (d: DeviceMgmtListItem) => {
  if (d?.vowifi_enabled) return ''
  if (!d?.network_enabled) return '数据未开启'
  if (!d?.network_connected) return '数据网络未连接'
  return ''
}

const secondaryStatus = (d: DeviceMgmtListItem) => {
  if (d?.vowifi_enabled) return 'WiFi-Calling'
  return [registrationText(d), dataNetworkText(d)].filter(Boolean).join(' · ')
}
</script>

<template>
  <div class="ui-card p-5">
    <div class="flex items-center gap-3 mb-4">
      <el-input v-model="modelQuery" placeholder="搜索设备 / ICCID / IMEI / 网卡" />
    </div>

    <div class="grid grid-cols-2 gap-2 mb-4">
      <el-select v-model="modelStatusFilter" size="small" placeholder="在线">
        <el-option label="全部状态" value="all" />
        <el-option label="仅在线" value="online" />
        <el-option label="仅离线" value="offline" />
      </el-select>

      <el-select v-model="modelSortKey" size="small" placeholder="排序">
        <el-option label="排序：名称" value="name" />
        <el-option label="排序：信号" value="signal" />
      </el-select>
      <el-select v-model="modelSortDir" size="small" placeholder="方向">
        <el-option label="升序" value="asc" />
        <el-option label="降序" value="desc" />
      </el-select>
      <div v-if="deviceLimit > 0" class="flex items-center">
        <el-tag
          size="small"
          :type="deviceCount >= deviceLimit ? 'warning' : 'info'"
          class="w-full justify-center"
        >
          配额 {{ deviceCount }} / {{ deviceLimit }}
        </el-tag>
      </div>
    </div>

    <ListSkeleton v-if="loading && filteredDevices.length === 0" :rows="8" />

    <EmptyState v-else-if="filteredDevices.length === 0" title="暂无设备" subtitle="点击右上角“添加设备”开始接管" />

    <div v-else class="device-list-scroll max-h-[65vh] overflow-y-auto pr-1">
      <div class="device-list-grid">
        <div v-for="d in filteredDevices" :key="d.id" class="device-list-item">
          <button
            type="button"
            class="w-full h-full text-left p-3 rounded-xl border transition-all"
            :class="selectedId === d.id
              ? 'border-indigo-200 dark:border-indigo-500/30 bg-indigo-50/70 dark:bg-indigo-500/10'
              : 'border-gray-100 dark:border-white/10 hover:bg-gray-50/60 dark:hover:bg-white/5'"
            @click="emit('select-device', d.id)"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="font-bold text-gray-800 dark:text-gray-100 truncate">{{ d.name || d.id }}</div>
                <div class="text-xs text-gray-500 mt-0.5 truncate">
                  {{ d.id }} · {{ d?.interface || '--' }}
                </div>
                <div class="text-xs text-gray-400 mt-1 truncate">
                  {{ secondaryStatus(d) }}
                </div>
              </div>
              <div class="flex items-center gap-2">
                <StatusLight :tone="primaryStatus(d).tone" size="sm" :animated="primaryStatus(d).animated" />
                <el-tag size="small" :type="primaryStatus(d).tag">{{ primaryStatus(d).label }}</el-tag>
              </div>
            </div>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.device-list-scroll {
  container-type: inline-size;
}

.device-list-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 0.5rem;
}

.device-list-item {
  min-width: 0;
}

@container (min-width: 700px) {
  .device-list-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
