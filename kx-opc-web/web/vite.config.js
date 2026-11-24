import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 打包到 Go 程序内嵌目录；开发时 npm run dev，接口代理到本机运行的 kx-opc-web。
// 想直接用服务器上的后端时设 KX_BACKEND，如 KX_BACKEND=10.65.104.171:8700（本机不用起后端，避免告警记两遍）
const backend = process.env.KX_BACKEND || '127.0.0.1:8700'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': `http://${backend}`,
      '/ws': { target: `ws://${backend}`, ws: true },
    },
  },
})
