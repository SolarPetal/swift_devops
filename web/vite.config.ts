import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 开发时前端 5173，后端 8088。/api 与 /ws 反代到后端。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8088',
      '/ws':  { target: 'ws://127.0.0.1:8088', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
})
