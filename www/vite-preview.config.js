import { defineConfig } from 'vite'
export default defineConfig({
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://192.168.1.99:8083', changeOrigin: true },
    }
  },
  preview: {
    port: 4173,
    proxy: {
      '/api': { target: 'http://192.168.1.99:8083', changeOrigin: true },
    }
  }
})
