import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// The studio is a static SPA embedded in the keel Go binary and served on
// loopback. Build output goes to dist/ with JS/CSS under /app so it never
// collides with the Go-embedded brand images served at /assets/*.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  base: '/',
  build: {
    outDir: 'dist',
    assetsDir: 'app',
    emptyOutDir: true,
  },
})
