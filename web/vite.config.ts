import { readFileSync } from 'node:fs'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const apiProxyTarget = env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:7575'
  const appVersion = JSON.parse(readFileSync(new URL('../desktop/package.json', import.meta.url), 'utf8')).version

  return {
    define: {
      __VOHIVE_APP_VERSION__: JSON.stringify(appVersion)
    },
    plugins: [
      vue(),
      AutoImport({
        dts: 'src/auto-imports.d.ts',
        imports: ['vue', 'vue-router', 'pinia'],
        resolvers: [ElementPlusResolver({ importStyle: false })]
      }),
      Components({
        dts: 'src/components.d.ts',
        resolvers: [
          ElementPlusResolver({
            importStyle: false
          })
        ]
      })
    ],
    build: {
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.includes('/src/views/Proxy.vue') || id.includes('/src/stores/proxy') || id.includes('/src/services/proxy')) return 'route-proxy'
            if (id.includes('/src/views/Devices.vue') || id.includes('/src/stores/devices') || id.includes('/src/services/devices')) return 'route-devices'
            if (id.includes('/src/views/Sms.vue') || id.includes('/src/stores/sms') || id.includes('/src/services/sms')) return 'route-sms'
            if (id.includes('/src/views/Logs.vue') || id.includes('/src/stores/logs') || id.includes('/src/services/logs')) return 'route-logs'
            if (!id.includes('node_modules')) return
            if (id.includes('echarts') || id.includes('zrender') || id.includes('vue-echarts')) return 'echarts'
            if (id.includes('element-plus')) return 'element-plus'
            if (id.includes('@element-plus/icons-vue')) return 'ep-icons'
            if (id.includes('@vicons')) return 'vicons'
            if (id.includes('vue-router') || id.includes('pinia') || id.includes('/vue/')) return 'vue-core'
            if (id.includes('dayjs')) return 'dayjs'
            return 'vendor'
          }
        }
      }
    },
    optimizeDeps: {
      exclude: ['@vicons/fluent', '@vicons/ionicons5'],
      include: [
        '@element-plus/icons-vue',
        'element-plus',
        'element-plus/es',
        'echarts/core',
        'echarts/renderers',
        'echarts/charts',
        'echarts/components',
        'vue-echarts',
        'dayjs',
        'dayjs/plugin/localeData.js',
        'dayjs/plugin/customParseFormat.js',
        'dayjs/plugin/advancedFormat.js',
        'dayjs/plugin/weekOfYear.js',
        'dayjs/plugin/weekYear.js',
        'dayjs/plugin/dayOfYear.js',
        'dayjs/plugin/isSameOrAfter.js',
        'dayjs/plugin/isSameOrBefore.js'
      ]
    },
    server: {
      host: '0.0.0.0',
      port: 5173,
      strictPort: true,
      watch: {
        ignored: ['**/dist/**', '**/.git/**']
      },
      proxy: {
        '/api': {
          target: apiProxyTarget,
          changeOrigin: true,
          timeout: 120000,
          proxyTimeout: 120000
        }
      }
    }
  }
})
