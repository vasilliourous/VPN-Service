import path from 'node:path'

import legacy from '@vitejs/plugin-legacy'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import svgr from 'vite-plugin-svgr'

export default defineConfig({
  // MUST be relative. In production Tauri serves `dist/` over the
  // `tauri://localhost` custom protocol, where a root-absolute `/assets/...`
  // resolves against the protocol root and 404s — every script and stylesheet
  // fails to load and the window renders empty. `./` keeps the URLs relative to
  // the document, which is the only form the custom protocol can resolve.
  //
  // This is load-bearing, not cosmetic: without it the app is a blank window.
  // Vite's default is `/`, so this line cannot be removed "for tidiness".
  base: './',
  root: 'src',
  server: { port: 3000 },
  plugins: [
    svgr(),
    react(),
    legacy({
      modernTargets: ['edge>=109', 'safari>=14'],
      renderLegacyChunks: false,
      modernPolyfills: ['es.object.has-own', 'web.structured-clone'],
      additionalModernPolyfills: [
        path.resolve('./src/polyfills/matchMedia.js'),
        path.resolve('./src/polyfills/WeakRef.js'),
        path.resolve('./src/polyfills/RegExp.js'),
      ],
    }),
  ],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 4000,
  },
  resolve: {
    alias: {
      '@': path.resolve('./src'),
      '@root': path.resolve('.'),
      'monaco-editor/esm/vs/editor/editor.worker.js':
        'monaco-editor/editor/editor.worker',
    },
  },
  define: {
    OS_PLATFORM: `"${process.platform}"`,
  },
})
