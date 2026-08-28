<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { Expand, Fold } from '@element-plus/icons-vue'
import LoadingScreen from '../components/LoadingScreen.vue'
import ErrorBoundary from '../components/ErrorBoundary.vue'
import SwitchDark from '../components/SwitchDark.vue'
import { systemService } from '../services/system'
import { debugCollector } from '../debug/collector'
import { normalizeDisplayVersion } from '../utils/version'
import {
  Mail24Regular,
  Settings24Regular,
  SignOut24Regular,
  Board24Regular,
  Phone24Regular,
  Globe24Regular,
  DocumentText24Regular
} from '@vicons/fluent'

defineProps({
  isDark: {
    type: Boolean,
    required: true
  }
})

const emit = defineEmits(['toggle-theme'])

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const collapsed = ref(false)
const isMobile = ref(false)
const drawerOpen = ref(false)
const debugOpen = ref(false)
const DebugPanel = defineAsyncComponent(() => import('../components/DebugPanel.vue'))
declare const __VOHIVE_APP_VERSION__: string
const appVersion = normalizeDisplayVersion(typeof __VOHIVE_APP_VERSION__ === 'string' ? __VOHIVE_APP_VERSION__ : '', 'dev')
const backendVersion = ref(appVersion)

const menuItems = [
  { index: '/', label: '仪表盘', icon: Board24Regular },
  { index: '/devices', label: '设备管理', icon: Phone24Regular },
  { index: '/proxy', label: '代理管理', icon: Globe24Regular },
  { index: '/sms', label: '短信中心', icon: Mail24Regular },
  { index: '/logs', label: '实时日志', icon: DocumentText24Regular },
  { index: '/settings', label: '系统设置', icon: Settings24Regular }
]

async function handleLogout() {
  const { ElMessageBox } = await import('element-plus')
  const confirmed = await ElMessageBox.confirm('确认退出登录？', '提示', {
    confirmButtonText: '退出',
    cancelButtonText: '取消',
    type: 'warning'
  })
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  auth.logout()
  router.push('/login')
}

function syncIsMobile() {
  if (typeof window === 'undefined') return
  isMobile.value = window.matchMedia('(max-width: 767px)').matches
  if (!isMobile.value) {
    drawerOpen.value = false
  }
}

function handleNavToggle() {
  if (isMobile.value) {
    drawerOpen.value = true
  } else {
    collapsed.value = !collapsed.value
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.ctrlKey && e.shiftKey && String(e.key || '').toLowerCase() === 'd') {
    e.preventDefault()
    debugOpen.value = !debugOpen.value
    localStorage.setItem('debug_panel_open', debugOpen.value ? '1' : '0')
  }
}

async function loadBackendVersion() {
  try {
    const info = await systemService.getInfo()
    if (info.ok) {
      backendVersion.value = normalizeDisplayVersion(info.data.version, appVersion)
    } else {
      backendVersion.value = appVersion
    }
  } catch {
    backendVersion.value = appVersion
  }
}

onMounted(() => {
  syncIsMobile()
  void loadBackendVersion()
  window.addEventListener('resize', syncIsMobile, { passive: true })

  const saved = localStorage.getItem('debug_panel_open')
  debugOpen.value = saved === '1'

  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  window.removeEventListener('resize', syncIsMobile)
  window.removeEventListener('keydown', onKeydown)
})

watch(
  () => route.fullPath,
  () => {
    drawerOpen.value = false
  }
)

watch(
  () => debugOpen.value,
  (v) => {
    localStorage.setItem('debug_panel_open', v ? '1' : '0')
  }
)

watch(
  () => debugCollector.openPanelRequestAt.value,
  (ts) => {
    if (!ts) return
    debugOpen.value = true
  }
)

const activePath = computed(() => route.path)
</script>

<template>
  <el-container v-if="auth.isAuthenticated && route.name !== 'Login'" class="h-full">
    <el-aside
      v-if="!isMobile"
      :width="collapsed ? '52px' : '232px'"
      class="h-full ui-glass transition-[width] duration-200 relative sidebar-shell"
    >
      <div class="h-14 px-4 flex items-center" :class="collapsed ? 'justify-center px-0' : ''">
        <div class="sidebar-brand-icon">V</div>
        <div v-if="!collapsed" class="ml-3">
          <div class="sidebar-brand-title">VoHive v{{ backendVersion }}</div>
        </div>
      </div>

      <el-menu
        :collapse="collapsed"
        :collapse-transition="false"
        :default-active="activePath"
        class="sidebar-menu !border-0 !border-r-0 !bg-transparent mt-2"
        router
      >
        <el-menu-item v-for="item in menuItems" :key="item.index" :index="item.index">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title><span class="sidebar-menu-label">{{ item.label }}</span></template>
        </el-menu-item>
      </el-menu>

      <div class="absolute bottom-4 w-full px-3" v-if="!collapsed">
        <div class="ui-panel-muted p-3 flex items-center gap-3">
          <div class="w-9 h-9 rounded-xl bg-indigo-50 dark:bg-indigo-500/10 flex items-center justify-center text-indigo-600 dark:text-indigo-300">
            <el-icon><Settings24Regular /></el-icon>
          </div>
          <div class="flex-1 min-w-0">
            <div class="text-sm font-bold truncate">Admin</div>
            <div class="text-xs text-gray-400 truncate">Administrator</div>
          </div>
          <el-button text type="danger" @click="handleLogout">
            <el-icon><SignOut24Regular /></el-icon>
          </el-button>
        </div>
      </div>
    </el-aside>

    <el-drawer v-model="drawerOpen" direction="ltr" size="256px" :with-header="false" class="mobile-drawer">
      <div class="h-full bg-white/95 dark:bg-[#141418]/95 backdrop-blur-md relative sidebar-shell">
        <div class="h-16 px-4 flex items-center">
          <div class="sidebar-brand-icon">V</div>
          <div class="ml-3">
            <div class="sidebar-brand-title">VoHive v{{ backendVersion }}</div>
          </div>
        </div>

        <el-menu
          :collapse="false"
          :collapse-transition="false"
          :default-active="activePath"
          class="sidebar-menu !border-0 !border-r-0 !bg-transparent mt-2"
          router
        >
          <el-menu-item v-for="item in menuItems" :key="item.index" :index="item.index">
            <el-icon><component :is="item.icon" /></el-icon>
            <template #title><span class="sidebar-menu-label">{{ item.label }}</span></template>
          </el-menu-item>
        </el-menu>

        <div class="absolute bottom-4 w-full px-3">
          <div class="ui-panel-muted p-3 flex items-center gap-3">
            <div class="w-9 h-9 rounded-xl bg-indigo-50 dark:bg-indigo-500/10 flex items-center justify-center text-indigo-600 dark:text-indigo-300">
              <el-icon><Settings24Regular /></el-icon>
            </div>
            <div class="flex-1 min-w-0">
              <div class="text-sm font-bold truncate">Admin</div>
              <div class="text-xs text-gray-400 truncate">Administrator</div>
            </div>
            <el-button text type="danger" @click="handleLogout">
              <el-icon><SignOut24Regular /></el-icon>
            </el-button>
          </div>
        </div>
      </div>
    </el-drawer>

    <el-container class="h-full">
      <el-header class="h-14 px-4 sm:px-5 flex items-center justify-between ui-glass border-b border-gray-100 dark:border-white/5 sticky top-0 z-10">
        <div class="flex items-center gap-2">
          <el-button text @click="handleNavToggle" class="!px-2">
            <el-icon>
              <Fold v-if="!isMobile && !collapsed" />
              <Expand v-else />
            </el-icon>
          </el-button>
        </div>

        <div class="flex items-center gap-3">
          <SwitchDark :is-dark="isDark" @toggle="(e) => emit('toggle-theme', e)" />

          <div class="hidden sm:flex items-center justify-center w-7 h-7 rounded-full bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-100 dark:border-emerald-500/20 shadow-sm">
            <span class="relative flex h-2 w-2">
              <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"></span>
              <span class="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"></span>
            </span>
          </div>
        </div>
      </el-header>

      <el-main class="p-4 sm:p-6 overflow-auto bg-gray-50/50 dark:bg-transparent">
        <div class="main-inner mx-auto w-full">
          <router-view v-slot="{ Component, route: r }">
            <ErrorBoundary v-if="Component" title="页面渲染失败">
              <component :is="Component" :key="r.fullPath" />
            </ErrorBoundary>
            <LoadingScreen v-else title="正在加载页面…" subtitle="正在准备页面组件与资源" />
          </router-view>
        </div>
      </el-main>
    </el-container>

    <DebugPanel v-model="debugOpen" />
  </el-container>
</template>

<style scoped>
.sidebar-shell {
  font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
  --sidebar-menu-text: #475569;
  --sidebar-menu-hover-bg: rgba(6, 182, 212, 0.08);
  --sidebar-menu-active-bg: linear-gradient(135deg, rgba(6, 182, 212, 0.14), rgba(20, 184, 166, 0.1));
  --sidebar-menu-active-color: #0f766e;
  --sidebar-menu-active-ring: rgba(6, 182, 212, 0.16);
}

.sidebar-brand-title {
  font-family: "Space Grotesk", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 1.62rem;
  font-weight: 600;
  letter-spacing: -0.03em;
  line-height: 1;
  display: flex;
  align-items: center;
  min-height: 1.75rem;
  background: linear-gradient(135deg, #06b6d4, #8b5cf6);
  background-clip: text;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  filter: drop-shadow(0 2px 8px rgba(6, 182, 212, 0.18));
  white-space: nowrap;
  padding-right: 4px;
}

.sidebar-brand-icon {
  width: 1.62rem;
  height: 1.62rem;
  border-radius: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: linear-gradient(135deg, #06b6d4, #14b8a6);
  color: #fff;
  font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-size: 0.84rem;
  font-weight: 700;
  box-shadow: 0 6px 14px rgba(6, 182, 212, 0.18);
}

.sidebar-menu-label {
  font-family: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-weight: 500;
  letter-spacing: -0.01em;
}

:global(html.dark) .sidebar-shell {
  --sidebar-menu-text: rgba(255, 255, 255, 0.72);
  --sidebar-menu-hover-bg: rgba(45, 212, 191, 0.1);
  --sidebar-menu-active-bg: linear-gradient(135deg, rgba(34, 211, 238, 0.18), rgba(45, 212, 191, 0.12));
  --sidebar-menu-active-color: #99f6e4;
  --sidebar-menu-active-ring: rgba(103, 232, 249, 0.18);
}

:deep(.sidebar-menu) {
  border-right: 0 !important;
  --el-menu-hover-bg-color: var(--sidebar-menu-hover-bg);
  --el-menu-active-color: var(--sidebar-menu-active-color);
  --el-menu-text-color: var(--sidebar-menu-text);
}

:deep(.sidebar-menu .el-menu-item) {
  height: 40px;
  min-height: 40px;
  line-height: 40px;
  margin: 2px 8px;
  border-radius: 10px;
  padding-left: 13px !important;
  padding-right: 13px !important;
  font-size: 0.94rem;
  font-weight: 400;
  letter-spacing: 0;
  color: var(--sidebar-menu-text);
  transition: background-color 160ms ease, color 160ms ease, box-shadow 160ms ease;
}

:deep(.sidebar-menu .el-menu-item .el-icon) {
  margin-right: 8px !important;
  font-size: 1.18rem;
}

:deep(.sidebar-menu .el-menu-item .el-icon svg) {
  width: 1.18rem;
  height: 1.18rem;
}

:deep(.sidebar-menu .el-menu-item:hover) {
  background: var(--sidebar-menu-hover-bg);
}

:deep(.sidebar-menu .el-menu-item.is-active) {
  background: var(--sidebar-menu-active-bg);
  color: var(--sidebar-menu-active-color);
  box-shadow: inset 0 0 0 1px var(--sidebar-menu-active-ring);
}

:deep(.sidebar-menu .el-menu-item.is-active .el-icon),
:deep(.sidebar-menu .el-menu-item.is-active .sidebar-menu-label) {
  color: inherit;
}

:deep(.sidebar-menu .el-menu-item::after) {
  display: none !important;
}

:deep(.sidebar-menu.el-menu--collapse) {
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item) {
  width: 36px;
  height: 36px;
  min-height: 36px;
  line-height: 36px;
  margin: 3px auto;
  border-radius: 10px;
  display: grid;
  place-items: center;
  padding: 0 !important;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item .el-icon) {
  width: 1.18rem;
  height: 1.18rem;
  margin: 0 !important;
  font-size: 1.18rem;
  line-height: 1;
  display: grid;
  place-items: center;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item .el-icon svg) {
  width: 1.18rem;
  height: 1.18rem;
  display: block;
}

:deep(.sidebar-menu.el-menu--collapse .el-menu-item .el-menu-tooltip__trigger) {
  position: static;
  inset: auto;
  width: 100%;
  height: 100%;
  padding: 0 !important;
  display: grid;
  place-items: center;
}

:deep(.sidebar-menu.el-menu--collapse > .el-menu-item [class^=el-icon]) {
  width: 1.18rem !important;
}

:deep(.sidebar-menu.el-menu--collapse .el-tooltip) {
  width: 36px;
  display: grid;
  place-items: center;
}

:deep(.sidebar-menu.el-menu--collapse .el-tooltip__trigger) {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
}

.main-inner {
  max-width: 100%;
}

@media (min-width: 768px) {
  .main-inner {
    max-width: clamp(0px, calc(100vw - 240px - 48px), 80rem);
  }
}

:deep(.mobile-drawer .el-drawer__body) {
  padding: 0 !important;
}
</style>
