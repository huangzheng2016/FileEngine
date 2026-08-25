import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import yaml from '@modyfi/vite-plugin-yaml'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), yaml()],
  server: {
    proxy: {
      // Forwards /api/* to the Go backend. Plain HTTP proxying also covers the
      // SSE run-event stream (GET /api/v1/sessions/:id/events/stream): EventSource
      // is a chunked HTTP response, not a WebSocket, so no `ws: true` is needed.
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
