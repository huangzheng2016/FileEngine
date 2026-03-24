import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import yaml from '@modyfi/vite-plugin-yaml'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), yaml()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
