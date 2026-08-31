import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 版本号构建期从 package.json 注入（侧边栏底部展示，见 env.d.ts 声明）
const pkg = JSON.parse(
  readFileSync(new URL('./package.json', import.meta.url), 'utf-8'),
) as { version: string }

// 后端端口：start.sh 从 .env 读 SERVER_PORT 后 export（本地端口被占用时 .env 改一处即可）
const apiTarget = `http://localhost:${process.env.SERVER_PORT ?? '8080'}`

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // /api → 后端 Go (Gin)；端口与 .env SERVER_PORT 同源（默认 8080）
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
      // /health 在 /api/v1 之外（探活、不走鉴权），dev 下单独转发
      '/health': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
})
