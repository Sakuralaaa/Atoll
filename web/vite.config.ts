import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const backend = process.env.ATOLL_DEV_BACKEND ?? 'http://localhost:7300'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
  server: {
    proxy: {
      // REST + the /events and /services/{id}/logs WebSockets
      '/api': { target: backend, changeOrigin: true, ws: true },
      '/agent': { target: backend, changeOrigin: true, ws: true },
    },
  },
})
