import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'node:path'
import { readFileSync } from 'node:fs'

// one version for the whole product: the repository's VERSION file
const version = readFileSync(resolve(__dirname, '../VERSION'), 'utf8').trim()

export default defineConfig({
  define: { __APP_VERSION__: JSON.stringify(version) },
  plugins: [
    react(),
    tailwindcss(),
    {
      name: 'admin-spa-fallback',
      configureServer(server) {
        server.middlewares.use((req, _res, next) => {
          if (req.url && (req.url === '/admin' || req.url.startsWith('/admin/')) && !req.url.includes('.')) req.url = '/admin.html'
          next()
        })
      },
    },
  ],
  resolve: { alias: { '@': resolve(__dirname, 'src') } },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:18080',
      '/files': 'http://localhost:18080',
      '/feeds': 'http://localhost:18080',
      '/openapi': 'http://localhost:18080',
    },
  },
  build: {
    rollupOptions: { input: { main: resolve(__dirname, 'index.html'), admin: resolve(__dirname, 'admin.html') } },
    chunkSizeWarningLimit: 1200,
  },
})
