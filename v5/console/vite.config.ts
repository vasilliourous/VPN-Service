import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The console is served by Caddy from /admin/, so all asset URLs must be
// prefixed. Without `base`, the built index.html would reference /assets/...
// and 404 behind the /admin path.
export default defineConfig({
  plugins: [vue()],
  base: '/admin/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Keep a single small bundle — this is an internal tool on a 512MB box,
    // there is nothing to gain from aggressive code splitting.
    chunkSizeWarningLimit: 1200,
  },
})
