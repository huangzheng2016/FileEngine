import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      // the generated Wails bindings live next to the desktop code and import the runtime from there
      '@bindings': fileURLToPath(new URL('../cmd/fileengine-desktop/bindings', import.meta.url)),
      '@wailsio/runtime': fileURLToPath(new URL('./node_modules/@wailsio/runtime', import.meta.url)),
    },
  },
  // the Markdown renderer is one lazy ~700 kB chunk, loaded on first use
  build: { sourcemap: false, chunkSizeWarningLimit: 750 },
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
