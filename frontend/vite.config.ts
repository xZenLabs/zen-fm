import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { mockApiPlugin } from './dev/mockApi'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const mock = mode === 'mock'
  const proxyTarget = env.ZENFM_API_PROXY || 'http://127.0.0.1:8080'
  const proxyOrigin = mock ? '' : new URL(proxyTarget).origin
  return {
    plugins: [react(), ...(mock ? [mockApiPlugin()] : [])],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      sourcemap: false,
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (!id.includes('/node_modules/')) return
            if (/\/node_modules\/@codemirror\/lang-/.test(id)) return 'editor-languages'
            if (/\/node_modules\/(?:@codemirror\/(?:state|view)\/|@lezer\/)/.test(id)) return 'editor-foundation'
            if (/\/node_modules\/(?:@uiw\/react-codemirror|@codemirror\/|codemirror\/)/.test(id)) return 'editor-core'
            if (/\/node_modules\/(?:@mui\/|@emotion\/|react-transition-group\/|@popperjs\/)/.test(id)) return 'mui'
            if (/\/node_modules\/(?:react\/|react-dom\/|react-router|scheduler\/|@tanstack\/react-query)/.test(id)) return 'react'
            if (/\/node_modules\/(?:i18next\/|react-i18next\/)/.test(id)) return 'i18n'
          },
        },
      },
    },
    server: {
      proxy: mock ? undefined : {
        '/api': {
          target: proxyTarget,
          changeOrigin: true,
          secure: false,
          configure(proxy) {
            proxy.on('proxyReq', (proxyRequest) => proxyRequest.setHeader('Origin', proxyOrigin))
          },
        },
      },
    },
    test: {
      include: ['src/**/*.test.{ts,tsx}'],
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      css: true,
      restoreMocks: true,
    },
  }
})
